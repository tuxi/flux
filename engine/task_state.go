package engine

import (
	"errors"
	"fmt"

	"flux/domain"
	"flux/workflow/nodes"
)

var ErrTaskCanceled = errors.New("task already canceled")

func (e *Engine) transitionTaskStatus(
	ctx *nodes.Context,
	newStatus domain.TaskStatus,
) error {
	if ctx == nil || ctx.Task == nil {
		return fmt.Errorf("task context is nil")
	}
	current, err := e.taskRepo.GetByID(ctx.Ctx, ctx.Task.ID)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("task not found: %d", ctx.Task.ID)
	}
	if current.Status == domain.TaskCanceled && newStatus != domain.TaskCanceled {
		ctx.Task.Status = domain.TaskCanceled
		return ErrTaskCanceled
	}
	if ctx.Task.Status == newStatus {
		return nil
	}
	if !domain.IsAllowedTaskTransition(ctx.Task.Status, newStatus) {
		return fmt.Errorf("illegal task status transition: %s -> %s", ctx.Task.Status, newStatus)
	}
	ctx.Task.Status = newStatus
	return e.taskRepo.Update(ctx.Ctx, ctx.Task)
}
