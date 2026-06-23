package handler

import (
	"context"
	"encoding/json"
	"errors"
	"flux/internal/consts"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flux/domain"
	aidto "flux/dto"
	repository2 "flux/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type stubTaskRepository struct {
	getByID func(ctx context.Context, id int64) (*domain.Task, error)
}

func (s stubTaskRepository) Create(ctx context.Context, task *domain.Task) error { return nil }
func (s stubTaskRepository) GetByID(ctx context.Context, id int64) (*domain.Task, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, gorm.ErrRecordNotFound
}
func (s stubTaskRepository) Update(ctx context.Context, task *domain.Task) error { return nil }
func (s stubTaskRepository) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) ListByUser(ctx context.Context, userID int64, params aidto.PageRequest) ([]*domain.Task, int64, error) {
	return nil, 0, nil
}
func (s stubTaskRepository) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	return nil
}
func (s stubTaskRepository) Enqueue(ctx context.Context, taskID int64) error { return nil }
func (s stubTaskRepository) TryClaimTask(ctx context.Context, taskID int64, workerID string) (bool, error) {
	return false, nil
}
func (s stubTaskRepository) FindBySubKey(ctx context.Context, subKey string) (*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) ListByParentNode(ctx context.Context, parentID int64, nodeName string) ([]*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) CreateFork(ctx context.Context, source *domain.Task, newTaskID int64, newInput []byte, editAction, editLabel string) (*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) ListByUserV2(ctx context.Context, userID int64, req aidto.TaskListReq) ([]*aidto.Task, int64, error) {
	return nil, 0, nil
}
func (s stubTaskRepository) GetRootTaskByIDAndUser(ctx context.Context, taskID int64, userID int64) (*domain.Task, error) {
	return nil, nil
}
func (s stubTaskRepository) GetTaskDetail(ctx context.Context, taskID int64) (*aidto.TaskDetail, error) {
	return nil, nil
}

var _ repository2.TaskRepository = (*stubTaskRepository)(nil)

type stubNodeRuntimeRepository struct {
	findByTaskID func(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error)
}

func (s stubNodeRuntimeRepository) Create(ctx context.Context, n *domain.NodeRuntime) error {
	return nil
}
func (s stubNodeRuntimeRepository) Update(ctx context.Context, n *domain.NodeRuntime) error {
	return nil
}
func (s stubNodeRuntimeRepository) FindByTaskID(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error) {
	if s.findByTaskID != nil {
		return s.findByTaskID(ctx, taskID)
	}
	return nil, nil
}
func (s stubNodeRuntimeRepository) FindByTaskIDAndNode(ctx context.Context, taskID int64, node string) (*domain.NodeRuntime, error) {
	return nil, nil
}
func (s stubNodeRuntimeRepository) MarkRunningAsRetrying(ctx context.Context, taskID int64) error {
	return nil
}
func (s stubNodeRuntimeRepository) MarkAsRetrying(ctx context.Context, taskID int64, name string) error {
	return nil
}
func (s stubNodeRuntimeRepository) MarkFailed(ctx context.Context, taskID int64, name string, errMessage string) error {
	return nil
}
func (s stubNodeRuntimeRepository) FindExpiredRunningNodes(ctx context.Context, expire time.Time) ([]*domain.NodeRuntime, error) {
	return nil, nil
}
func (s stubNodeRuntimeRepository) AttemptCompletePendingEdges(ctx context.Context, taskID int64, nodeName string, output map[string]any, errMsg string) (bool, error) {
	return false, nil
}
func (s stubNodeRuntimeRepository) CloneCheckpoint(ctx context.Context, fromTaskID, toTaskID int64) error {
	return nil
}

var _ repository2.NodeRuntimeRepository = (*stubNodeRuntimeRepository)(nil)

type stubEventRepository struct {
	findByTaskID func(ctx context.Context, taskID int64) ([]domain.TaskEvent, error)
}

func (s stubEventRepository) Create(ctx context.Context, event *domain.TaskEvent) error { return nil }
func (s stubEventRepository) FindByTaskID(ctx context.Context, taskID int64, isByRoot bool) ([]domain.TaskEvent, error) {
	if s.findByTaskID != nil {
		return s.findByTaskID(ctx, taskID)
	}
	return nil, nil
}

func (s stubEventRepository) FindByTaskIDAndTypePrefixes(ctx context.Context, taskID int64, prefixes []string, isByRoot bool) ([]domain.TaskEvent, error) {
	return s.FindByTaskID(ctx, taskID, false)
}

func (s stubEventRepository) FindPersistentByTaskID(ctx context.Context, taskID int64, afterSequence int64, limit int, isByRoot bool) ([]domain.TaskEvent, error) {
	return s.FindByTaskID(ctx, taskID, isByRoot)
}

var _ repository2.EventRepository = (*stubEventRepository)(nil)

func performGetTask(handler *WorkflowHandler, taskID string, userID int64) (*httptest.ResponseRecorder, aidto.ApiResponse) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID, nil)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: taskID}}
	c.Set(consts.UserID, userID)

	handler.GetTask(c)

	var resp aidto.ApiResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return w, resp
}

func TestGetTaskRejectsForbiddenTask(t *testing.T) {
	h := &WorkflowHandler{
		taskRepo: stubTaskRepository{
			getByID: func(ctx context.Context, id int64) (*domain.Task, error) {
				return &domain.Task{ID: id, UserID: 99}, nil
			},
		},
		nodeRuntimeRepo: stubNodeRuntimeRepository{},
		eventRepo:       stubEventRepository{},
	}

	w, resp := performGetTask(h, "123", 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if resp.Code != http.StatusForbidden {
		t.Fatalf("unexpected response code: %d", resp.Code)
	}
	if resp.Message != "forbidden" {
		t.Fatalf("unexpected message: %s", resp.Message)
	}
}

func TestGetTaskReturnsTaskForOwner(t *testing.T) {
	h := &WorkflowHandler{
		taskRepo: stubTaskRepository{
			getByID: func(ctx context.Context, id int64) (*domain.Task, error) {
				return &domain.Task{ID: id, UserID: 1}, nil
			},
		},
		nodeRuntimeRepo: stubNodeRuntimeRepository{
			findByTaskID: func(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error) {
				return []*domain.NodeRuntime{}, nil
			},
		},
		eventRepo: stubEventRepository{
			findByTaskID: func(ctx context.Context, taskID int64) ([]domain.TaskEvent, error) {
				return []domain.TaskEvent{}, nil
			},
		},
	}

	w, resp := performGetTask(h, "123", 1)
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected response code: %d", resp.Code)
	}
}

func TestGetTaskRejectsInvalidTaskID(t *testing.T) {
	h := &WorkflowHandler{
		taskRepo:        stubTaskRepository{},
		nodeRuntimeRepo: stubNodeRuntimeRepository{},
		eventRepo:       stubEventRepository{},
	}

	w, resp := performGetTask(h, "abc", 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if resp.Message != "invalid task id" {
		t.Fatalf("unexpected message: %s", resp.Message)
	}
}

func TestGetTaskReturnsNotFoundWhenTaskMissing(t *testing.T) {
	h := &WorkflowHandler{
		taskRepo: stubTaskRepository{
			getByID: func(ctx context.Context, id int64) (*domain.Task, error) {
				return nil, errors.New("not found")
			},
		},
		nodeRuntimeRepo: stubNodeRuntimeRepository{},
		eventRepo:       stubEventRepository{},
	}

	w, resp := performGetTask(h, "123", 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if resp.Message != "task not found" {
		t.Fatalf("unexpected message: %s", resp.Message)
	}
}
