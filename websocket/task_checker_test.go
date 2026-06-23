package websocket

import (
	"context"
	"flux/domain"
	aidto "flux/dto"
	repository2 "flux/repository"
	"testing"
	"time"

	"gorm.io/gorm"
)

type stubTaskRepo struct {
	task *domain.Task
	err  error
}

func (s stubTaskRepo) Create(ctx context.Context, task *domain.Task) error { return nil }
func (s stubTaskRepo) GetByID(ctx context.Context, id int64) (*domain.Task, error) {
	return s.task, s.err
}
func (s stubTaskRepo) Update(ctx context.Context, task *domain.Task) error { return nil }
func (s stubTaskRepo) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) ListByUser(ctx context.Context, userID int64, params aidto.PageRequest) ([]*domain.Task, int64, error) {
	return nil, 0, nil
}
func (s stubTaskRepo) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	return nil
}
func (s stubTaskRepo) Enqueue(ctx context.Context, taskID int64) error { return nil }
func (s stubTaskRepo) TryClaimTask(ctx context.Context, taskID int64, workerID string) (bool, error) {
	return false, nil
}
func (s stubTaskRepo) FindBySubKey(ctx context.Context, subKey string) (*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) ListByParentNode(ctx context.Context, parentID int64, nodeName string) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) CreateFork(ctx context.Context, source *domain.Task, newTaskID int64, newInput []byte, editAction, editLabel string) (*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) ListByUserV2(ctx context.Context, userID int64, req aidto.TaskListReq) ([]*aidto.Task, int64, error) {
	return nil, 0, nil
}
func (s stubTaskRepo) GetRootTaskByIDAndUser(ctx context.Context, taskID int64, userID int64) (*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepo) GetTaskDetail(ctx context.Context, taskID int64) (*aidto.TaskDetail, error) {
	return nil, nil
}

var _ repository2.TaskRepository = (*stubTaskRepo)(nil)

func TestRepositoryTaskAccessCheckerReturnsAlreadyFinishedForTerminalTask(t *testing.T) {
	checker := NewRepositoryTaskAccessChecker(stubTaskRepo{
		task: &domain.Task{
			ID:     123,
			UserID: 1,
			Status: domain.TaskSuccess,
		},
	})

	err := checker.CheckTaskSubscription(context.Background(), 1, 123)
	if err == nil {
		t.Fatal("expected already finished error")
	}

	ackErr := ackErrorFrom(err)
	if ackErr.Code != ErrCodeTaskAlreadyFinished {
		t.Fatalf("unexpected error code: %s", ackErr.Code)
	}
}

func TestRepositoryTaskAccessCheckerAllowsSuspendedTask(t *testing.T) {
	checker := NewRepositoryTaskAccessChecker(stubTaskRepo{
		task: &domain.Task{
			ID:     123,
			UserID: 1,
			Status: domain.TaskSuspended,
		},
	})

	if err := checker.CheckTaskSubscription(context.Background(), 1, 123); err != nil {
		t.Fatalf("expected suspended task to be subscribable, got %v", err)
	}
}

func TestRepositoryTaskAccessCheckerReturnsNotFound(t *testing.T) {
	checker := NewRepositoryTaskAccessChecker(stubTaskRepo{err: gorm.ErrRecordNotFound})

	err := checker.CheckTaskSubscription(context.Background(), 1, 123)
	if err == nil {
		t.Fatal("expected not found error")
	}

	ackErr := ackErrorFrom(err)
	if ackErr.Code != ErrCodeTaskNotFound {
		t.Fatalf("unexpected error code: %s", ackErr.Code)
	}
}
