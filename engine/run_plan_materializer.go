package engine

import (
	"fmt"

	"flux/domain"
	"flux/workflow"
	"flux/workflow/nodes"
)

// engine/run_plan_materializer.go

func (e *Engine) MaterializeRunPlan(
	runCtx *nodes.Context,
	wf workflow.Workflow,
	plan *RunPlan,
) error {
	if runCtx == nil {
		return fmt.Errorf("runCtx is nil")
	}
	if plan == nil {
		return nil
	}

	if runCtx.DirtyNodes == nil {
		runCtx.DirtyNodes = map[string]bool{}
	}
	if runCtx.MapItemReuse == nil {
		runCtx.MapItemReuse = map[string]map[int]bool{}
	}
	if runCtx.PatchedNodes == nil {
		runCtx.PatchedNodes = map[string]bool{}
	}

	parentSnapshot := runCtx.ParentSnapshot

	for _, nodeName := range plan.TopoOrder {
		nodePlan := plan.Nodes[nodeName]
		if nodePlan == nil {
			continue
		}

		runtime := runCtx.Runtime[nodeName]
		if runtime == nil {
			return fmt.Errorf("runtime not found: %s", nodeName)
		}

		var parentNode *domain.NodeRuntime
		if parentSnapshot != nil {
			parentNode = parentSnapshot.Nodes[nodeName]
		}

		switch nodePlan.Action {
		case PlanActionReuse:
			if parentNode == nil {
				return fmt.Errorf("reuse node %s missing parent snapshot", nodeName)
			}
			if parentNode.State == domain.NodeSkipped {
				if err := e.materializeReusedSkippedNode(runCtx, runtime, parentNode, nodePlan); err != nil {
					return err
				}
				continue
			}
			if parentNode.State != domain.NodeSuccess {
				return fmt.Errorf("reuse node %s parent state is not success: %s", nodeName, parentNode.State)
			}
			if err := e.materializeReusedNode(runCtx, wf, nodeName, parentNode, nodePlan); err != nil {
				return err
			}

		case PlanActionPatch:
			if parentNode == nil {
				return fmt.Errorf("patched node %s missing parent snapshot", nodeName)
			}
			if parentNode.State != domain.NodeSuccess {
				return fmt.Errorf("patched node %s parent state is not success: %s", nodeName, parentNode.State)
			}
			if err := e.materializePatchedNode(runCtx, wf, nodeName, runtime, parentNode, nodePlan); err != nil {
				return err
			}

		case PlanActionExecute:
			if nodePlan.Reason != ExecutionReasonNone {
				runCtx.DirtyNodes[nodeName] = true
			}
			if len(nodePlan.MapItemReuse) > 0 {
				runCtx.MapItemReuse[nodeName] = cloneIntBoolMap(nodePlan.MapItemReuse)
			}
			if err := e.materializeExecutableNode(runCtx, runtime, parentNode, nodeName, nodePlan); err != nil {
				return err
			}

		default:
			return fmt.Errorf("unsupported plan action %q for node %s", nodePlan.Action, nodeName)
		}
	}

	return nil
}

func (e *Engine) materializeReusedNode(
	runCtx *nodes.Context,
	wf workflow.Workflow,
	nodeName string,
	parentNode *domain.NodeRuntime,
	nodePlan *NodePlan,
) error {
	if err := e.injectReusedNodeOutput(runCtx, wf, nodeName, parentNode); err != nil {
		return err
	}

	runtime := runCtx.Runtime[nodeName]
	if runtime == nil {
		return fmt.Errorf("runtime not found: %s", nodeName)
	}

	runtime.ReuseKind = nodePlan.ReuseKind
	runtime.IsInjected = true
	runtime.IsDirty = false
	runtime.DirtyReason = ""
	runtime.ExecutionReason = string(nodePlan.Reason)
	return e.nodeRepo.Update(runCtx.Ctx, runtime)
}

func (e *Engine) materializeReusedSkippedNode(
	runCtx *nodes.Context,
	runtime *domain.NodeRuntime,
	parentNode *domain.NodeRuntime,
	nodePlan *NodePlan,
) error {
	now := parentNode.CheckpointedAt
	runtime.State = domain.NodeSkipped
	runtime.Progress = 1
	runtime.StartedAt = parentNode.StartedAt
	runtime.FinishedAt = parentNode.FinishedAt
	runtime.LastHeartbeat = nil
	runtime.Error = parentNode.Error
	runtime.Output = deepCloneMap(parentNode.Output)
	runtime.Checkpoint = deepCloneMap(parentNode.Checkpoint)
	runtime.InputHash = parentNode.InputHash
	runtime.ResolvedInput = deepCloneMap(parentNode.ResolvedInput)
	runtime.OutputHash = parentNode.OutputHash
	runtime.IsInjected = true
	runtime.ReuseKind = nodePlan.ReuseKind
	runtime.IsDirty = false
	runtime.DirtyReason = ""
	runtime.ExecutionReason = string(nodePlan.Reason)
	runtime.CheckpointedAt = now
	runtime.ReusedFromTaskID = &parentNode.TaskID

	reusedNode := parentNode.Name
	runtime.ReusedFromNode = &reusedNode
	runtime.ActivatedEdges = cloneBoolMap(parentNode.ActivatedEdges)

	runCtx.UpdateNodeStatus(runtime.Name, string(domain.NodeSkipped))
	runCtx.ActivatedEdgesMerge(runtime.ActivatedEdges)

	return e.nodeRepo.Update(runCtx.Ctx, runtime)
}

func (e *Engine) materializePatchedNode(
	runCtx *nodes.Context,
	wf workflow.Workflow,
	nodeName string,
	runtime *domain.NodeRuntime,
	parentNode *domain.NodeRuntime,
	nodePlan *NodePlan,
) error {
	if err := e.injectReusedNodeOutput(runCtx, wf, nodeName, parentNode); err != nil {
		return err
	}

	for _, patch := range nodePlan.Patches {
		if err := e.applySinglePatchToRuntime(runCtx, wf, runtime, patch); err != nil {
			return err
		}
	}

	if runCtx.PatchedNodes == nil {
		runCtx.PatchedNodes = map[string]bool{}
	}
	runCtx.PatchedNodes[nodeName] = true
	runtime.ExecutionReason = string(nodePlan.Reason)

	return e.finalizePatchedRuntimeState(runCtx, runtime)
}

func (e *Engine) materializeExecutableNode(
	runCtx *nodes.Context,
	runtime *domain.NodeRuntime,
	parentNode *domain.NodeRuntime,
	nodeName string,
	nodePlan *NodePlan,
) error {
	runtime.ExecutionReason = string(nodePlan.Reason)

	reason := mapExecutionReasonToDirtyReason(nodePlan.Reason)

	if nodePlan.Action == PlanActionExecute && nodePlan.Reason == ExecutionReasonNone {
		// 非 fork 纯初始执行场景，不强行覆盖 dirty metadata
		return e.nodeRepo.Update(runCtx.Ctx, runtime)
	}

	return e.prepareDirtyRuntime(
		runCtx,
		runtime,
		parentNode,
		nodeName,
		reason,
	)
}

func mapExecutionReasonToDirtyReason(reason ExecutionReason) string {
	switch reason {
	case ExecutionReasonResumeBoundary:
		return DirtyReasonResumeBoundary
	case ExecutionReasonUpstreamDirty:
		return DirtyReasonUpstreamDirty
	case ExecutionReasonInputChanged:
		return DirtyReasonInputChanged
	case ExecutionReasonMissingParent:
		return DirtyReasonMissingParent
	case ExecutionReasonParentNotReady:
		return DirtyReasonParentNotReady
	case ExecutionReasonInputResolveFail:
		return DirtyReasonInputResolve
	case ExecutionReasonPatchedNode:
		return DirtyReasonPatchedState
	default:
		return string(reason)
	}
}
