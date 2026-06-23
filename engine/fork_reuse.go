package engine

import (
	"context"
	"encoding/json"
	"flux/definition"
	"flux/domain"
	"flux/workflow"
	"flux/workflow/nodes"
	"fmt"
	"time"
)

func (e *Engine) CreateForkRun(
	ctx context.Context,
	sourceTaskID int64,
	userID int64,
	overrideInput map[string]any,
	resumeSpec *domain.ResumeSpec,
	editAction string,
	editLabel string,
) (*domain.Task, error) {
	sourceTask, err := e.taskRepo.GetByID(ctx, sourceTaskID)
	if err != nil {
		return nil, err
	}
	if sourceTask == nil {
		return nil, fmt.Errorf("source task not found")
	}

	// 读取 workflow definition，做 patch validation
	dbVersion, err := e.WorkflowVersionRepo.Get(ctx, sourceTask.WorkflowVersionID)
	if err != nil {
		return nil, err
	}

	var def definition.WorkflowDefinition
	if err := json.Unmarshal(dbVersion.DefinitionJSON, &def); err != nil {
		return nil, err
	}

	wf, err := e.builder.Build(&def)
	if err != nil {
		return nil, err
	}

	if err := e.validateResumeSpec(wf, resumeSpec); err != nil {
		return nil, err
	}

	newInput := parseTaskInput(sourceTask.InputJSON)
	if newInput == nil {
		newInput = map[string]any{}
	}
	for k, v := range overrideInput {
		newInput[k] = v
	}

	inputJSON, err := json.Marshal(newInput)
	if err != nil {
		return nil, err
	}

	var patchJSON []byte
	var resumeFrom *string
	if resumeSpec != nil {
		patchJSON, err = json.Marshal(resumeSpec.Patches)
		if err != nil {
			return nil, err
		}
		if resumeSpec.ResumeFrom != "" {
			resumeFrom = &resumeSpec.ResumeFrom
		}
	}

	baseRunID := sourceTask.BaseRunID
	if baseRunID == 0 {
		baseRunID = sourceTask.ID
	}

	newTask := &domain.Task{
		ID:                   e.iSrv.GenSnowID(),
		UserID:               userID,
		Type:                 sourceTask.Type,
		Status:               domain.TaskPending,
		InputJSON:            inputJSON,
		WorkflowVersionID:    sourceTask.WorkflowVersionID,
		WorkflowDefinitionID: sourceTask.WorkflowDefinitionID,
		BaseRunID:            baseRunID,
		ForkedFrom:           &sourceTask.ID,
		RunDepth:             sourceTask.RunDepth + 1,
		Progress:             0,
		StartedAt:            time.Now(),
		ResumeFrom:           resumeFrom,
		PatchJSON:            patchJSON,

		// 业务归属字段全部继承
		EntryType:         sourceTask.EntryType,
		ToolDefinitionID:  sourceTask.ToolDefinitionID,
		ToolModeID:        sourceTask.ToolModeID,
		ToolModeVersionID: sourceTask.ToolModeVersionID,
		TemplateID:        sourceTask.TemplateID,
		TemplateVersionID: sourceTask.TemplateVersionID,
		EntryTitle:        sourceTask.EntryTitle,
		EntrySubtitle:     sourceTask.EntrySubtitle,
		RouteKey:          sourceTask.RouteKey,
		ModeKey:           sourceTask.ModeKey,
	}
	newTask.RootID = newTask.ID
	if newTask.RootID == 0 {
		newTask.RootID = sourceTask.ID
	}

	if editAction != "" {
		newTask.EditAction = &editAction
	}
	if editLabel != "" {
		newTask.EditLabel = &editLabel
	}

	if err := e.taskRepo.Create(ctx, newTask); err != nil {
		return nil, err
	}
	return newTask, nil
}

func (e *Engine) injectReusedNodeOutput(
	ctx *nodes.Context,
	wf workflow.Workflow,
	nodeName string,
	parentNode *domain.NodeRuntime,
) error {
	defNode := findNode(wf.Nodes(), nodeName)
	if defNode == nil {
		return fmt.Errorf("node def not found: %s", nodeName)
	}
	if parentNode.Output == nil {
		return nil
	}

	cloned := deepCloneMap(parentNode.Output)

	if err := ctx.SetNodeOutput(
		nodeName,
		cloned,
		defNode.Step.OutputSchema(),
	); err != nil {
		return err
	}

	if ctx.InjectedOutputs == nil {
		ctx.InjectedOutputs = map[string]map[string]any{}
	}
	ctx.InjectedOutputs[nodeName] = deepCloneMap(cloned)

	runtime := ctx.Runtime[nodeName]
	now := time.Now()

	runtime.State = domain.NodeSuccess
	runtime.Progress = 1
	runtime.Output = deepCloneMap(cloned)
	runtime.Checkpoint = deepCloneMap(parentNode.Checkpoint)
	runtime.InputHash = parentNode.InputHash
	runtime.ResolvedInput = deepCloneMap(parentNode.ResolvedInput)
	runtime.OutputHash = parentNode.OutputHash
	runtime.IsInjected = true
	runtime.ReuseKind = domain.ReuseNode
	runtime.IsDirty = false
	runtime.DirtyReason = ""
	runtime.CheckpointedAt = &now
	runtime.ReusedFromTaskID = &parentNode.TaskID

	reusedNode := parentNode.Name
	runtime.ReusedFromNode = &reusedNode
	runtime.ActivatedEdges = cloneBoolMap(parentNode.ActivatedEdges)

	ctx.UpdateNodeStatus(nodeName, string(domain.NodeSuccess))
	ctx.ActivatedEdgesMerge(runtime.ActivatedEdges)

	return e.nodeRepo.Update(ctx.Ctx, runtime)
}

// prepareForkReuse 兼容旧入口；新版请直接使用：
// LoadForkParentSnapshot -> BuildRunPlan -> MaterializeRunPlan
func (e *Engine) prepareForkReuse(
	runCtx *nodes.Context,
	wf workflow.Workflow,
) error {
	if runCtx.Task.ForkedFrom == nil {
		return nil
	}

	if err := e.LoadForkParentSnapshot(runCtx); err != nil {
		return err
	}

	plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	if err != nil {
		return err
	}

	return e.MaterializeRunPlan(runCtx, wf, plan)
}

func (e *Engine) prepareDirtyRuntime(
	runCtx *nodes.Context,
	runtime *domain.NodeRuntime,
	parentNode *domain.NodeRuntime,
	nodeName string,
	reason string,
) error {
	now := time.Now()

	runtime.State = domain.NodePending
	runtime.Progress = 0
	runtime.StartedAt = nil
	runtime.FinishedAt = nil
	runtime.LastHeartbeat = nil
	runtime.Error = ""
	runtime.Output = nil
	runtime.OutputHash = ""
	runtime.ActivatedEdges = map[string]bool{}

	// 节点被重置回 pending → 它原先在全局 ctx.ActivatedEdges 中写过的 outgoing edge
	// 都已经失效，必须从 ctx 里清掉，否则下游 depsMet 会读到过期的 false/true 标记。
	e.clearOutgoingActivatedEdges(runCtx, nodeName)

	runCtx.ClearNodeOutput(nodeName)

	// 🔥 不再依赖 runCtx.MapItemReuse
	if runtime.ReuseKind == domain.ReuseMapItems && parentNode != nil {
		runtime.Checkpoint = deepCloneMap(parentNode.Checkpoint)
	} else {
		runtime.Checkpoint = nil
		runtime.ReuseKind = domain.ReuseNone
	}

	runtime.IsDirty = true
	runtime.IsInjected = false
	runtime.DirtyReason = reason
	runtime.ReusedFromTaskID = nil
	runtime.ReusedFromNode = nil
	runtime.CheckpointedAt = &now

	runCtx.UpdateNodeStatus(nodeName, string(domain.NodePending))

	return e.nodeRepo.Update(runCtx.Ctx, runtime)
}

func (e *Engine) finalizePatchedRuntimeState(
	runCtx *nodes.Context,
	runtime *domain.NodeRuntime,
) error {
	now := time.Now()

	runtime.State = domain.NodeSuccess
	runtime.Progress = 1
	runtime.IsInjected = false
	runtime.IsDirty = false
	runtime.DirtyReason = ""
	runtime.ReusedFromTaskID = nil
	runtime.ReusedFromNode = nil
	runtime.ReuseKind = domain.ReuseNone
	runtime.CheckpointedAt = &now

	runCtx.UpdateNodeStatus(runtime.Name, string(domain.NodeSuccess))
	return e.nodeRepo.Update(runCtx.Ctx, runtime)
}
