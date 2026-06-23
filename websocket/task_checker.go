package websocket

import (
	"context"
	"errors"
	"flux/domain"
	"flux/repository"
	"fmt"

	"gorm.io/gorm"
)

type RepositoryTaskAccessChecker struct {
	taskRepo repository.TaskRepository
}

func NewRepositoryTaskAccessChecker(taskRepo repository.TaskRepository) *RepositoryTaskAccessChecker {
	return &RepositoryTaskAccessChecker{taskRepo: taskRepo}
}

func (c *RepositoryTaskAccessChecker) CheckTaskSubscription(ctx context.Context, userID int64, taskID int64) error {
	task, err := c.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return newAckError(ErrCodeTaskNotFound, fmt.Sprintf("task %d not found", taskID))
		}
		return err
	}

	if task == nil {
		return newAckError(ErrCodeTaskNotFound, fmt.Sprintf("task %d not found", taskID))
	}

	if task.UserID != userID {
		return newAckError(ErrCodeTaskForbidden, fmt.Sprintf("task %d forbidden", taskID))
	}

	if isTaskFinished(task) {
		return newAckError(ErrCodeTaskAlreadyFinished, fmt.Sprintf("task %d already finished", taskID))
	}

	return nil
}

func isTaskFinished(task *domain.Task) bool {
	return task != nil && task.IsTerminal()
}
