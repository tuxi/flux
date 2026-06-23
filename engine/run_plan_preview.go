package engine

import (
	"context"
	"encoding/json"
	"flux/domain"
	"flux/workflow"
	"fmt"
)

// engine/run_plan_preview.go

// PreviewRunPlan
//
// 用“源任务 + 临时 resumeSpec + overrideInput”构造一个纯内存 preview runCtx，
// 然后直接复用真实 BuildRunPlan 逻辑，返回可视化调试用的 RunPlan。
// 注意：
// 1. 不创建 task
// 2. 不写数据库
// 3. 不 materialize
func (e *Engine) PreviewRunPlan(
	ctx context.Context,
	sourceTask *domain.Task,
	resumeSpec *domain.ResumeSpec,
	overrideInput map[string]any,
) (*RunPlan, workflow.Workflow, error) {
	if sourceTask == nil {
		return nil, nil, fmt.Errorf("source task is nil")
	}

	// 1. 加载 workflow 定义
	wf, _, err := e.loadWorkflowForTask(ctx, sourceTask)
	if err != nil {
		return nil, nil, err
	}

	// 2. 先做 resumeSpec 校验，保证和真实 fork 一致
	if err := e.validateResumeSpec(wf, resumeSpec); err != nil {
		return nil, nil, err
	}

	// 3. 合并 input（预览只做内存态）
	newInput := parseTaskInput(sourceTask.InputJSON)
	if newInput == nil {
		newInput = map[string]any{}
	}
	for k, v := range overrideInput {
		newInput[k] = v
	}

	// 4. 构造虚拟 preview task
	previewTask := &domain.Task{
		ID:                   0, // preview 不落库，不需要真实 ID
		UserID:               sourceTask.UserID,
		Type:                 sourceTask.Type,
		Status:               domain.TaskPending,
		WorkflowVersionID:    sourceTask.WorkflowVersionID,
		WorkflowDefinitionID: sourceTask.WorkflowDefinitionID,
		BaseRunID:            sourceTask.BaseRunID,
		ForkedFrom:           &sourceTask.ID, // 关键：把 sourceTask 当成 fork parent
		RunDepth:             sourceTask.RunDepth + 1,
		RootID:               sourceTask.RootID,
		EditAction:           sourceTask.EditAction,
		EditLabel:            sourceTask.EditLabel,

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

	if previewTask.BaseRunID == 0 {
		previewTask.BaseRunID = sourceTask.ID
	}

	inputJSON, err := json.Marshal(newInput)
	if err != nil {
		return nil, nil, err
	}
	previewTask.InputJSON = inputJSON

	if resumeSpec != nil {
		if resumeSpec.ResumeFrom != "" {
			previewTask.ResumeFrom = &resumeSpec.ResumeFrom
		}
		if len(resumeSpec.Patches) > 0 {
			patchJSON, err := json.Marshal(resumeSpec.Patches)
			if err != nil {
				return nil, nil, err
			}
			previewTask.PatchJSON = patchJSON
		}
	}

	// 5. 构造 preview runCtx
	runCtx := e.newRunContext(ctx, previewTask, wf)

	// 6. 加载 parent snapshot（来自 sourceTask）
	if err := e.LoadForkParentSnapshot(runCtx); err != nil {
		return nil, nil, err
	}

	// 7. 直接走真实 planning
	plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	if err != nil {
		return nil, nil, err
	}

	return plan, wf, nil
}
