package engine

import (
	"context"
	"flux/domain"
	"flux/workflow"
	"flux/workflow/nodes"
)

// engine/run_context.go

func (e *Engine) newRunContext(
	ctx context.Context,
	task *domain.Task,
	wf workflow.Workflow,
) *nodes.Context {
	patches, _ := parseTaskPatches(task.PatchJSON)
	resumeFrom := ""
	if task.ResumeFrom != nil {
		resumeFrom = *task.ResumeFrom
	}

	runCtx := &nodes.Context{
		Workflow: wf.Source(),
		Ctx:      ctx,
		Task:     task,
		Input:    parseTaskInput(task.InputJSON),
		Output:   make(map[string]any),
		Runtime:  make(map[string]*domain.NodeRuntime),
		EventBus: e.eventBus,

		Patches:      patches,
		ResumeFrom:   resumeFrom,
		PatchedNodes: map[string]bool{},
	}

	runCtx.Output = map[string]any{
		"input": runCtx.Input,
		"nodes": map[string]any{},
	}
	runCtx.EnsureOutputInitialized()
	return runCtx
}
