package engine

import (
	"fmt"
	"strings"

	"flux/domain"
	"flux/workflow"
	"flux/workflow/nodes"
)

// applySinglePatchToRuntime
// 作用：对真实 runtime 打 patch，并持久化到数据库。
// 用于 fork 新任务 prepare 阶段，把 patch 真正落到新任务 runtime 上。
func (e *Engine) applySinglePatchToRuntime(
	ctx *nodes.Context,
	wf workflow.Workflow,
	runtime *domain.NodeRuntime,
	patch domain.RuntimePatch,
) error {
	fmt.Printf("PATCH BEFORE: %+v\n", runtime.Output)
	if runtime == nil {
		return fmt.Errorf("runtime is nil")
	}
	if runtime.Name != patch.Node {
		return fmt.Errorf("runtime node mismatch: runtime=%s patch=%s", runtime.Name, patch.Node)
	}

	if err := e.applySinglePatchInMemory(ctx, wf, runtime, patch); err != nil {
		return err
	}
	fmt.Printf("PATCH AFTER: %+v\n", runtime.Output)
	return e.nodeRepo.Update(ctx.Ctx, runtime)
}

// applyPatchesToRuntime
// 作用：把 ctx.Patches 全部应用到“当前运行任务”的 runtime，并持久化。
// 一般在 prepareForkReuse 里调用，且应先于 dirty plan 或紧接着父快照注入之后调用。
func (e *Engine) applyPatchesToRuntime(
	ctx *nodes.Context,
	wf workflow.Workflow,
) error {
	if ctx == nil || len(ctx.Patches) == 0 {
		return nil
	}

	for _, patch := range ctx.Patches {
		if patch.Node == "" {
			return fmt.Errorf("patch node is empty")
		}

		runtime := ctx.Runtime[patch.Node]
		if runtime == nil {
			return fmt.Errorf("runtime not found for patch node: %s", patch.Node)
		}

		if err := e.applySinglePatchToRuntime(ctx, wf, runtime, patch); err != nil {
			return fmt.Errorf("apply patch to runtime failed, node=%s path=%s: %w",
				patch.Node, patch.Path, err)
		}
	}

	return nil
}

// applyPatchesToPlanningContext
// 作用：把 patch 应用到 planning ctx 的内存态，不落库。
// 这样 BuildDirtyPlan 在重算下游 input/hash 时，看到的是 patch 后的新状态。
func (e *Engine) applyPatchesToPlanningContext(
	ctx *nodes.Context,
	wf workflow.Workflow,
) error {
	if ctx == nil || len(ctx.Patches) == 0 {
		return nil
	}

	for _, patch := range ctx.Patches {
		if patch.Node == "" {
			return fmt.Errorf("patch node is empty")
		}

		runtime := ctx.Runtime[patch.Node]
		if runtime == nil {
			return fmt.Errorf("planning runtime not found for patch node: %s", patch.Node)
		}

		if err := e.applySinglePatchInMemory(ctx, wf, runtime, patch); err != nil {
			return fmt.Errorf("apply patch to planning context failed, node=%s path=%s: %w",
				patch.Node, patch.Path, err)
		}
	}

	return nil
}

// applySinglePatchInMemory
// 作用：只改内存里的 runtime / ctx.Output，不落库。
// 既可给 planning ctx 用，也可给真实 runCtx 复用。
func (e *Engine) applySinglePatchInMemory(
	ctx *nodes.Context,
	wf workflow.Workflow,
	runtime *domain.NodeRuntime,
	patch domain.RuntimePatch,
) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if runtime == nil {
		return fmt.Errorf("runtime is nil")
	}
	if patch.Node == "" {
		return fmt.Errorf("patch node is empty")
	}
	if runtime.Name != "" && runtime.Name != patch.Node {
		return fmt.Errorf("runtime node mismatch: runtime=%s patch=%s", runtime.Name, patch.Node)
	}

	switch patch.Target {
	case domain.PatchTargetNodeOutput:
		if runtime.Output == nil {
			runtime.Output = map[string]any{}
		}

		if err := applyPatchToMap(runtime.Output, patch); err != nil {
			return err
		}

		// 对 set / merge 做一次读回校验，避免“看起来成功其实没打进去”
		if patch.Op == domain.PatchOpSet || patch.Op == domain.PatchOpMerge {
			if strings.TrimSpace(patch.Path) != "" {
				if _, ok := GetByPath(runtime.Output, patch.Path); !ok {
					return fmt.Errorf("patch applied but path not found after write: %s", patch.Path)
				}
			}
		}

		defNode := findNode(wf.Nodes(), patch.Node)
		if defNode == nil {
			return fmt.Errorf("node def not found: %s", patch.Node)
		}

		if err := ctx.SetNodeOutput(
			patch.Node,
			deepCloneMap(runtime.Output),
			defNode.Step.OutputSchema(),
		); err != nil {
			return err
		}

		runtime.OutputHash = ctx.CalculateOutputHash(runtime.Output)

	case domain.PatchTargetNodeCheckpoint:
		if runtime.Checkpoint == nil {
			runtime.Checkpoint = map[string]any{}
		}

		if err := applyPatchToMap(runtime.Checkpoint, patch); err != nil {
			return err
		}

		if patch.Op == domain.PatchOpSet || patch.Op == domain.PatchOpMerge {
			if strings.TrimSpace(patch.Path) != "" {
				if _, ok := GetByPath(runtime.Checkpoint, patch.Path); !ok {
					return fmt.Errorf("checkpoint patch applied but path not found after write: %s", patch.Path)
				}
			}
		}

		if err := e.rebuildNodeOutputFromCheckpoint(ctx, wf, runtime); err != nil {
			return fmt.Errorf("rebuild output from checkpoint failed: %w", err)
		}

	default:
		return fmt.Errorf("unsupported patch target: %s", patch.Target)
	}

	runtime.IsDirty = true
	runtime.IsInjected = false
	runtime.DirtyReason = DirtyReasonPatchedState
	runtime.ReusedFromTaskID = nil
	runtime.ReusedFromNode = nil

	if runtime.ReuseKind == domain.ReuseNode {
		runtime.ReuseKind = domain.ReuseNone
	}

	if ctx.PatchedNodes == nil {
		ctx.PatchedNodes = map[string]bool{}
	}
	ctx.PatchedNodes[patch.Node] = true

	return nil
}

func applyPatchToMap(root map[string]any, patch domain.RuntimePatch) error {
	switch patch.Op {
	case domain.PatchOpSet:
		return SetByPath(root, patch.Path, deepCloneAny(patch.Value))
	case domain.PatchOpDelete:
		return DeleteByPath(root, patch.Path)
	case domain.PatchOpMerge:
		return MergeByPath(root, patch.Path, deepCloneAny(patch.Value))
	default:
		return fmt.Errorf("unsupported patch op: %s", patch.Op)
	}
}
