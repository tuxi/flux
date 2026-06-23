package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/workflow"
	"flux/workflow/nodes"
)

func (e *Engine) loadWorkflowForTask(
	ctx context.Context,
	task *domain.Task,
) (workflow.Workflow, *definition.WorkflowDefinition, error) {
	if task == nil {
		return nil, nil, fmt.Errorf("task is nil")
	}

	dbVersion, err := e.WorkflowVersionRepo.Get(ctx, task.WorkflowVersionID)
	if err != nil {
		return nil, nil, err
	}

	var def definition.WorkflowDefinition
	if err := json.Unmarshal(dbVersion.DefinitionJSON, &def); err != nil {
		return nil, nil, err
	}

	wf, err := e.builder.Build(&def)
	if err != nil {
		return nil, nil, err
	}

	return wf, &def, nil
}

func (e *Engine) executeTask(
	runCtx *nodes.Context,
	wf workflow.Workflow,
	emitStarted bool,
) RunResult {
	if runCtx == nil {
		return RunResult{
			Status: RunFailed,
			Err:    fmt.Errorf("runCtx is nil"),
		}
	}
	if wf == nil {
		return RunResult{
			Status: RunFailed,
			Err:    fmt.Errorf("workflow is nil"),
		}
	}

	if emitStarted {
		runCtx.EventBus.Publish(runCtx.Task.RootID, &domain.TaskEvent{
			TaskID:     runCtx.Task.ID,
			RootTaskID: runCtx.Task.RootID,
			Step:       "task",
			Type:       domain.TaskEventStarted,
			Message:    "任务开始执行",
			CreatedAt:  time.Now(),
		})
	}

	result := e.runDAG(runCtx, wf)

	switch result.Status {
	case RunSuccess:
		if err := e.transitionTaskStatus(runCtx, domain.TaskSuccess); err != nil {
			if errors.Is(err, ErrTaskCanceled) {
				return RunResult{Status: RunNoop}
			}
			return RunResult{Status: RunFailed, Err: err}
		}
		runCtx.Task.ErrorMessage = ""
		runCtx.Task.Progress = 1
		_ = e.taskRepo.Update(runCtx.Ctx, runCtx.Task)

		final, _ := runCtx.Output["final"].(map[string]any)
		message := "任务执行成功"
		if runCtx.Task.ID != runCtx.Task.RootID {
			message = "子任务执行成功，请耐心等待"
		}
		runCtx.EventBus.Publish(runCtx.Task.RootID, &domain.TaskEvent{
			Step:       "task",
			TaskID:     runCtx.Task.ID,
			RootTaskID: runCtx.Task.RootID,
			Type:       domain.TaskEventSucceeded,
			Message:    message,
			Meta:       final,
			CreatedAt:  time.Now(),
		})
		return result

	case RunFailed:
		if err := e.transitionTaskStatus(runCtx, domain.TaskFailed); err != nil {
			if errors.Is(err, ErrTaskCanceled) {
				return RunResult{Status: RunNoop}
			}
			return RunResult{Status: RunFailed, Err: err}
		}
		if result.Err != nil {
			runCtx.Task.ErrorMessage = result.Err.Error()
		}
		_ = e.taskRepo.Update(runCtx.Ctx, runCtx.Task)

		runCtx.EventBus.Publish(runCtx.Task.RootID, &domain.TaskEvent{
			Step:       "task",
			TaskID:     runCtx.Task.ID,
			RootTaskID: runCtx.Task.RootID,
			Type:       domain.TaskEventFailed,
			Message:    "任务执行失败",
			Meta:       runCtx.Output,
			CreatedAt:  time.Now(),
		})
		return result

	case RunSuspended:
		if err := e.transitionTaskStatus(runCtx, domain.TaskSuspended); err != nil {
			if errors.Is(err, ErrTaskCanceled) {
				return RunResult{Status: RunNoop}
			}
			return RunResult{Status: RunFailed, Err: err}
		}
		runCtx.EventBus.Publish(runCtx.Task.RootID, &domain.TaskEvent{
			Step:       "task",
			TaskID:     runCtx.Task.ID,
			RootTaskID: runCtx.Task.RootID,
			Type:       domain.TaskEventSuspended,
			Message:    "任务已就绪",
			Meta:       nil,
			CreatedAt:  time.Now(),
		})
		return result

	default:
		return result
	}
}
