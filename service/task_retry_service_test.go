package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"flux/domain"
	"flux/dto"
	"github.com/stretchr/testify/require"
)

type fakeRetryAwaitBindingRepo struct {
	bindings map[int64]*domain.AwaitBinding
}

func newFakeRetryAwaitBindingRepo(bindings ...*domain.AwaitBinding) *fakeRetryAwaitBindingRepo {
	repo := &fakeRetryAwaitBindingRepo{bindings: map[int64]*domain.AwaitBinding{}}
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		cloned := *binding
		repo.bindings[binding.ID] = &cloned
	}
	return repo
}

func (r *fakeRetryAwaitBindingRepo) Create(ctx context.Context, b *domain.AwaitBinding) error {
	cloned := *b
	r.bindings[b.ID] = &cloned
	return nil
}

func (r *fakeRetryAwaitBindingRepo) Update(ctx context.Context, b *domain.AwaitBinding) error {
	cloned := *b
	r.bindings[b.ID] = &cloned
	return nil
}

func (r *fakeRetryAwaitBindingRepo) GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error) {
	binding := r.bindings[id]
	if binding == nil {
		return nil, nil
	}
	cloned := *binding
	return &cloned, nil
}

func (r *fakeRetryAwaitBindingRepo) ListByTaskID(ctx context.Context, taskID int64) ([]*domain.AwaitBinding, error) {
	out := make([]*domain.AwaitBinding, 0)
	for _, binding := range r.bindings {
		if binding.TaskID != taskID {
			continue
		}
		cloned := *binding
		out = append(out, &cloned)
	}
	return out, nil
}

func (r *fakeRetryAwaitBindingRepo) GetByTaskAndNode(ctx context.Context, taskID int64, nodeName string) (*domain.AwaitBinding, error) {
	return nil, nil
}

func (r *fakeRetryAwaitBindingRepo) FindWaitingByProviderTaskID(ctx context.Context, provider, providerTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}

func (r *fakeRetryAwaitBindingRepo) FindWaitingByAPITaskID(ctx context.Context, provider, apiTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}

func (r *fakeRetryAwaitBindingRepo) FindWaitingBySignal(ctx context.Context, signalName, callbackToken string) (*domain.AwaitBinding, error) {
	return nil, nil
}

func (r *fakeRetryAwaitBindingRepo) TransitionStatus(ctx context.Context, id int64, from domain.AwaitBindingStatus, to domain.AwaitBindingStatus) (bool, error) {
	return false, nil
}

func (r *fakeRetryAwaitBindingRepo) ClaimCompleting(ctx context.Context, id int64, expectedStatuses []domain.AwaitBindingStatus) (bool, error) {
	return false, nil
}

func (r *fakeRetryAwaitBindingRepo) FindPollDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return nil, nil
}

func (r *fakeRetryAwaitBindingRepo) FindTimeoutDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return nil, nil
}

func TestResetAwaitBindingsForRetry_CancelsInFlightBindingsOnly(t *testing.T) {
	repo := newFakeRetryAwaitBindingRepo(
		&domain.AwaitBinding{
			ID:       1,
			TaskID:   99,
			NodeName: "volcengine_wait",
			Status:   domain.AwaitBindingWaiting,
		},
		&domain.AwaitBinding{
			ID:       2,
			TaskID:   99,
			NodeName: "volcengine_wait",
			Status:   domain.AwaitBindingFailed,
		},
		&domain.AwaitBinding{
			ID:       3,
			TaskID:   99,
			NodeName: "other_wait",
			Status:   domain.AwaitBindingWaiting,
		},
	)
	svc := &taskRetryService{awaitBindingRepo: repo}

	err := svc.resetAwaitBindingsForRetry(context.Background(), 99, map[string]struct{}{
		"volcengine_wait": {},
	})
	require.NoError(t, err)

	waiting, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, domain.AwaitBindingCanceled, waiting.Status)
	require.Equal(t, "canceled by task retry", waiting.ErrorMessage)
	require.NotNil(t, waiting.CanceledAt)

	failed, err := repo.GetByID(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, domain.AwaitBindingFailed, failed.Status)

	other, err := repo.GetByID(context.Background(), 3)
	require.NoError(t, err)
	require.Equal(t, domain.AwaitBindingWaiting, other.Status)
}

type fakeRetryTaskRepo struct {
	tasks    []*domain.Task
	enqueued []int64
}

func (r *fakeRetryTaskRepo) Create(ctx context.Context, task *domain.Task) error { return nil }
func (r *fakeRetryTaskRepo) GetByID(ctx context.Context, id int64) (*domain.Task, error) {
	for _, task := range r.tasks {
		if task != nil && task.ID == id {
			return task, nil
		}
	}
	return nil, nil
}
func (r *fakeRetryTaskRepo) Update(ctx context.Context, task *domain.Task) error { return nil }
func (r *fakeRetryTaskRepo) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (r *fakeRetryTaskRepo) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	return nil, nil
}
func (r *fakeRetryTaskRepo) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (r *fakeRetryTaskRepo) ListByUser(ctx context.Context, userID int64, params dto.PageRequest) ([]*domain.Task, int64, error) {
	return nil, 0, nil
}
func (r *fakeRetryTaskRepo) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	out := make([]*domain.Task, 0)
	for _, task := range r.tasks {
		if task == nil || task.ParentID == nil || *task.ParentID != parentID {
			continue
		}
		out = append(out, task)
	}
	return out, nil
}
func (r *fakeRetryTaskRepo) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	return nil
}
func (r *fakeRetryTaskRepo) Enqueue(ctx context.Context, taskID int64) error {
	r.enqueued = append(r.enqueued, taskID)
	return nil
}
func (r *fakeRetryTaskRepo) TryClaimTask(ctx context.Context, taskID int64, workerID string) (bool, error) {
	return false, nil
}
func (r *fakeRetryTaskRepo) FindBySubKey(ctx context.Context, subKey string) (*domain.Task, error) {
	for _, task := range r.tasks {
		if task != nil && task.SubKey != nil && *task.SubKey == subKey {
			return task, nil
		}
	}
	return nil, nil
}
func (r *fakeRetryTaskRepo) ListByParentNode(ctx context.Context, parentID int64, nodeName string) ([]*domain.Task, error) {
	out := make([]*domain.Task, 0)
	for _, task := range r.tasks {
		if task == nil || task.ParentID == nil || task.ParentNode == nil {
			continue
		}
		if *task.ParentID != parentID || *task.ParentNode != nodeName {
			continue
		}
		out = append(out, task)
	}
	return out, nil
}
func (r *fakeRetryTaskRepo) CreateFork(ctx context.Context, source *domain.Task, newTaskID int64, newInput []byte, editAction, editLabel string) (*domain.Task, error) {
	return nil, nil
}
func (r *fakeRetryTaskRepo) ListByUserV2(ctx context.Context, userID int64, req dto.TaskListReq) ([]*dto.Task, int64, error) {
	return nil, 0, nil
}
func (r *fakeRetryTaskRepo) GetRootTaskByIDAndUser(ctx context.Context, taskID int64, userID int64) (*domain.Task, error) {
	return nil, nil
}
func (r *fakeRetryTaskRepo) GetTaskDetail(ctx context.Context, taskID int64) (*dto.TaskDetail, error) {
	return nil, nil
}

func TestRepairMapCheckpointForRetry_ProtectsUnresolvedChildSubKeys(t *testing.T) {
	parentID := int64(77)
	parentNode := "map_render"
	subKeyFailed := "map-child-failed"
	subKeySuspended := "map-child-suspended"
	subKeyDone := "map-child-done"
	subKeyStale := "map-child-stale"

	repo := &fakeRetryTaskRepo{
		tasks: []*domain.Task{
			{
				ID:         1,
				ParentID:   &parentID,
				ParentNode: &parentNode,
				SubKey:     &subKeyDone,
				Status:     domain.TaskSuccess,
				MapIndex:   intPtr(0),
			},
			{
				ID:         2,
				ParentID:   &parentID,
				ParentNode: &parentNode,
				SubKey:     &subKeyFailed,
				Status:     domain.TaskFailed,
				MapIndex:   intPtr(1),
			},
			{
				ID:         3,
				ParentID:   &parentID,
				ParentNode: &parentNode,
				SubKey:     &subKeySuspended,
				Status:     domain.TaskSuspended,
				MapIndex:   intPtr(2),
			},
			{
				ID:         4,
				ParentID:   &parentID,
				ParentNode: &parentNode,
				SubKey:     &subKeyStale,
				Status:     domain.TaskFailed,
				InputJSON:  mustRetryJSON(t, map[string]any{"index": 0}),
			},
		},
	}
	svc := &taskRetryService{taskRepo: repo}
	runtime := &domain.NodeRuntime{
		Name:       parentNode,
		State:      domain.NodeFailed,
		Output:     map[string]any{"results": []any{"old"}},
		OutputHash: "old-hash",
		Checkpoint: map[string]any{
			"total": 3,
			"done":  1,
			"results": map[string]any{
				"0": map[string]any{"primary_file_url": "https://example.com/a.mp4"},
			},
		},
	}

	changed, protected, err := svc.repairMapCheckpointForRetry(context.Background(), parentID, runtime)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, runtime.Output)
	require.Empty(t, runtime.OutputHash)
	require.ElementsMatch(t, []string{subKeyFailed, subKeySuspended}, protected)
}

func TestReviveProtectedChildrenForRetry_RequeuesPendingMapChild(t *testing.T) {
	parentID := int64(77)
	parentNode := "augment_missing_assets_multi"
	subKeyPending := "map-child-pending"
	subKeyDone := "map-child-done"

	repo := &fakeRetryTaskRepo{
		tasks: []*domain.Task{
			{
				ID:         10,
				ParentID:   &parentID,
				ParentNode: &parentNode,
				SubKey:     &subKeyPending,
				Status:     domain.TaskPending,
				MapIndex:   intPtr(0),
			},
			{
				ID:         11,
				ParentID:   &parentID,
				ParentNode: &parentNode,
				SubKey:     &subKeyDone,
				Status:     domain.TaskSuccess,
				MapIndex:   intPtr(1),
			},
		},
	}
	svc := &taskRetryService{taskRepo: repo}

	err := svc.reviveProtectedChildrenForRetry(
		context.Background(),
		parentID,
		map[string]struct{}{subKeyPending: {}},
		RetryTriggerRecovery,
	)

	require.NoError(t, err)
	require.Equal(t, []int64{10}, repo.enqueued)
	require.Equal(t, domain.TaskPending, repo.tasks[0].Status)
}

func intPtr(v int) *int {
	return &v
}

func mustRetryJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
