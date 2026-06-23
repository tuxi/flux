package service

import (
	"context"
	"testing"
	"time"

	"flux/domain"
	"flux/dto"
	"github.com/stretchr/testify/require"
)

func TestCancelForSupersededRevision_CancelsTaskNodesAndAwaitBindings(t *testing.T) {
	now := time.Now()
	taskRepo := newFakeRunCancellationTaskRepo(
		&domain.Task{ID: 10, RootID: 10, Status: domain.TaskSuspended, OutputJSON: []byte(`{"old":true}`), Progress: 0.7},
	)
	nodeRepo := newFakeRunCancellationNodeRepo(
		&domain.NodeRuntime{ID: 101, TaskID: 10, Name: "review_prompt", State: domain.NodeAwaiting, LastHeartbeat: &now, Progress: 0.8},
		&domain.NodeRuntime{ID: 102, TaskID: 10, Name: "done", State: domain.NodeSuccess, Progress: 1},
	)
	nextPollAt := now.Add(-time.Minute)
	lastEventID := "evt-1"
	lastEventSource := "poll"
	awaitRepo := newFakeRunCancellationAwaitBindingRepo(
		&domain.AwaitBinding{
			ID:              201,
			TaskID:          10,
			NodeName:        "review_prompt",
			Status:          domain.AwaitBindingWaiting,
			NextPollAt:      &nextPollAt,
			LastEventID:     &lastEventID,
			LastEventSource: &lastEventSource,
			LastEventPayload: map[string]any{
				"payload": true,
			},
		},
		&domain.AwaitBinding{ID: 202, TaskID: 10, NodeName: "done", Status: domain.AwaitBindingCompleted},
	)

	svc := NewRunCancellationService(taskRepo, nodeRepo, awaitRepo)
	result, err := svc.CancelForSupersededRevision(context.Background(), 10)
	require.NoError(t, err)

	require.False(t, result.AlreadyCanceled)
	require.Equal(t, []int64{10}, result.CanceledTaskIDs)
	require.Equal(t, []int64{101}, result.CanceledNodeIDs)
	require.Equal(t, []int64{201}, result.CanceledAwaitBindingIDs)

	task := taskRepo.tasks[10]
	require.Equal(t, domain.TaskCanceled, task.Status)
	require.Equal(t, CancelReasonSupersededByRevision, task.ErrorMessage)
	require.Nil(t, task.OutputJSON)
	require.Zero(t, task.Progress)

	awaiting := nodeRepo.nodes[101]
	require.Equal(t, domain.NodeCanceled, awaiting.State)
	require.Equal(t, CancelReasonSupersededByRevision, awaiting.Error)
	require.Nil(t, awaiting.LastHeartbeat)
	require.NotNil(t, awaiting.FinishedAt)
	require.Zero(t, awaiting.Progress)

	success := nodeRepo.nodes[102]
	require.Equal(t, domain.NodeSuccess, success.State)

	binding := awaitRepo.bindings[201]
	require.Equal(t, domain.AwaitBindingCanceled, binding.Status)
	require.Equal(t, CancelReasonSupersededByRevision, binding.ErrorMessage)
	require.NotNil(t, binding.CanceledAt)
	require.Nil(t, binding.NextPollAt)
	require.Nil(t, binding.LastEventID)
	require.Nil(t, binding.LastEventSource)
	require.Nil(t, binding.LastEventPayload)

	completed := awaitRepo.bindings[202]
	require.Equal(t, domain.AwaitBindingCompleted, completed.Status)
}

func TestCancelForSupersededRevision_IsIdempotent(t *testing.T) {
	taskRepo := newFakeRunCancellationTaskRepo(
		&domain.Task{ID: 10, RootID: 10, Status: domain.TaskSuspended},
	)
	nodeRepo := newFakeRunCancellationNodeRepo(
		&domain.NodeRuntime{ID: 101, TaskID: 10, Name: "review_prompt", State: domain.NodeAwaiting},
	)
	awaitRepo := newFakeRunCancellationAwaitBindingRepo(
		&domain.AwaitBinding{ID: 201, TaskID: 10, NodeName: "review_prompt", Status: domain.AwaitBindingWaiting},
	)

	svc := NewRunCancellationService(taskRepo, nodeRepo, awaitRepo)
	first, err := svc.CancelForSupersededRevision(context.Background(), 10)
	require.NoError(t, err)
	second, err := svc.CancelForSupersededRevision(context.Background(), 10)
	require.NoError(t, err)

	require.False(t, first.AlreadyCanceled)
	require.True(t, second.AlreadyCanceled)
	require.Equal(t, 1, taskRepo.updateCount[10])
	require.Equal(t, 1, nodeRepo.updateCount[101])
	require.Equal(t, 1, awaitRepo.updateCount[201])
	require.Empty(t, second.CanceledTaskIDs)
	require.Empty(t, second.CanceledNodeIDs)
	require.Empty(t, second.CanceledAwaitBindingIDs)
}

func TestCancelForSupersededRevision_MakesAwaitBindingUnrecoverable(t *testing.T) {
	now := time.Now()
	nextPollAt := now.Add(-time.Minute)
	timeoutAt := now.Add(-time.Minute)
	signalName := "prompt_review"
	callbackToken := "review-card-token"
	taskRepo := newFakeRunCancellationTaskRepo(
		&domain.Task{ID: 10, RootID: 10, Status: domain.TaskSuspended},
	)
	nodeRepo := newFakeRunCancellationNodeRepo(
		&domain.NodeRuntime{ID: 101, TaskID: 10, Name: "review_prompt", State: domain.NodeAwaiting},
	)
	awaitRepo := newFakeRunCancellationAwaitBindingRepo(
		&domain.AwaitBinding{
			ID:            201,
			TaskID:        10,
			NodeName:      "review_prompt",
			Status:        domain.AwaitBindingWaiting,
			SignalName:    &signalName,
			CallbackToken: &callbackToken,
			NextPollAt:    &nextPollAt,
			TimeoutAt:     &timeoutAt,
		},
	)

	svc := NewRunCancellationService(taskRepo, nodeRepo, awaitRepo)
	_, err := svc.CancelForSupersededRevision(context.Background(), 10)
	require.NoError(t, err)

	staleSignal, err := awaitRepo.FindWaitingBySignal(context.Background(), signalName, callbackToken)
	require.NoError(t, err)
	require.Nil(t, staleSignal)

	claimed, err := awaitRepo.ClaimCompleting(context.Background(), 201, []domain.AwaitBindingStatus{domain.AwaitBindingWaiting})
	require.NoError(t, err)
	require.False(t, claimed)

	pollDue, err := awaitRepo.FindPollDue(context.Background(), now, 10)
	require.NoError(t, err)
	require.Empty(t, pollDue)

	timeoutDue, err := awaitRepo.FindTimeoutDue(context.Background(), now, 10)
	require.NoError(t, err)
	require.Empty(t, timeoutDue)
}

func TestCancelForSupersededRevision_CancelsChildren(t *testing.T) {
	parentID := int64(10)
	taskRepo := newFakeRunCancellationTaskRepo(
		&domain.Task{ID: parentID, RootID: parentID, Status: domain.TaskSuspended},
		&domain.Task{ID: 11, RootID: parentID, ParentID: &parentID, Status: domain.TaskRunning},
		&domain.Task{ID: 12, RootID: parentID, ParentID: &parentID, Status: domain.TaskSuccess},
	)
	nodeRepo := newFakeRunCancellationNodeRepo(
		&domain.NodeRuntime{ID: 101, TaskID: parentID, Name: "review_prompt", State: domain.NodeAwaiting},
		&domain.NodeRuntime{ID: 111, TaskID: 11, Name: "render", State: domain.NodeRunning},
	)
	awaitRepo := newFakeRunCancellationAwaitBindingRepo(
		&domain.AwaitBinding{ID: 201, TaskID: parentID, NodeName: "review_prompt", Status: domain.AwaitBindingWaiting},
		&domain.AwaitBinding{ID: 211, TaskID: 11, NodeName: "render", Status: domain.AwaitBindingPending},
	)

	svc := NewRunCancellationService(taskRepo, nodeRepo, awaitRepo)
	result, err := svc.CancelForSupersededRevision(context.Background(), parentID)
	require.NoError(t, err)

	require.ElementsMatch(t, []int64{parentID, 11}, result.CanceledTaskIDs)
	require.ElementsMatch(t, []int64{101, 111}, result.CanceledNodeIDs)
	require.ElementsMatch(t, []int64{201, 211}, result.CanceledAwaitBindingIDs)
	require.Equal(t, domain.TaskCanceled, taskRepo.tasks[parentID].Status)
	require.Equal(t, domain.TaskCanceled, taskRepo.tasks[11].Status)
	require.Equal(t, domain.TaskSuccess, taskRepo.tasks[12].Status)
}

func TestCancelForSupersededRevision_RejectsCompletedTask(t *testing.T) {
	taskRepo := newFakeRunCancellationTaskRepo(&domain.Task{ID: 10, RootID: 10, Status: domain.TaskSuccess})
	svc := NewRunCancellationService(taskRepo, nil, nil)

	_, err := svc.CancelForSupersededRevision(context.Background(), 10)
	require.ErrorIs(t, err, ErrRunCancellationNotAllowed)
	require.Equal(t, domain.TaskSuccess, taskRepo.tasks[10].Status)
}

type fakeRunCancellationTaskRepo struct {
	tasks       map[int64]*domain.Task
	updateCount map[int64]int
}

func newFakeRunCancellationTaskRepo(tasks ...*domain.Task) *fakeRunCancellationTaskRepo {
	repo := &fakeRunCancellationTaskRepo{tasks: map[int64]*domain.Task{}, updateCount: map[int64]int{}}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		cloned := *task
		repo.tasks[task.ID] = &cloned
	}
	return repo
}

func (r *fakeRunCancellationTaskRepo) Create(ctx context.Context, task *domain.Task) error {
	return nil
}

func (r *fakeRunCancellationTaskRepo) GetByID(ctx context.Context, id int64) (*domain.Task, error) {
	task := r.tasks[id]
	if task == nil {
		return nil, nil
	}
	return task, nil
}

func (r *fakeRunCancellationTaskRepo) Update(ctx context.Context, task *domain.Task) error {
	cloned := *task
	r.tasks[task.ID] = &cloned
	r.updateCount[task.ID]++
	return nil
}

func (r *fakeRunCancellationTaskRepo) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}

func (r *fakeRunCancellationTaskRepo) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	return nil, nil
}

func (r *fakeRunCancellationTaskRepo) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	return nil, nil
}

func (r *fakeRunCancellationTaskRepo) ListByUser(ctx context.Context, userID int64, params dto.PageRequest) ([]*domain.Task, int64, error) {
	return nil, 0, nil
}

func (r *fakeRunCancellationTaskRepo) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	out := make([]*domain.Task, 0)
	for _, task := range r.tasks {
		if task == nil || task.ParentID == nil || *task.ParentID != parentID {
			continue
		}
		out = append(out, task)
	}
	return out, nil
}

func (r *fakeRunCancellationTaskRepo) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	return nil
}

func (r *fakeRunCancellationTaskRepo) Enqueue(ctx context.Context, taskID int64) error {
	return nil
}

func (r *fakeRunCancellationTaskRepo) TryClaimTask(ctx context.Context, taskID int64, workerID string) (bool, error) {
	return false, nil
}

func (r *fakeRunCancellationTaskRepo) FindBySubKey(ctx context.Context, subKey string) (*domain.Task, error) {
	return nil, nil
}

func (r *fakeRunCancellationTaskRepo) ListByParentNode(ctx context.Context, parentID int64, nodeName string) ([]*domain.Task, error) {
	return nil, nil
}

func (r *fakeRunCancellationTaskRepo) CreateFork(ctx context.Context, source *domain.Task, newTaskID int64, newInput []byte, editAction, editLabel string) (*domain.Task, error) {
	return nil, nil
}

func (r *fakeRunCancellationTaskRepo) ListByUserV2(ctx context.Context, userID int64, req dto.TaskListReq) ([]*dto.Task, int64, error) {
	return nil, 0, nil
}

func (r *fakeRunCancellationTaskRepo) GetRootTaskByIDAndUser(ctx context.Context, taskID int64, userID int64) (*domain.Task, error) {
	return nil, nil
}

func (r *fakeRunCancellationTaskRepo) GetTaskDetail(ctx context.Context, taskID int64) (*dto.TaskDetail, error) {
	return nil, nil
}

type fakeRunCancellationNodeRepo struct {
	nodes       map[int64]*domain.NodeRuntime
	updateCount map[int64]int
}

func newFakeRunCancellationNodeRepo(nodes ...*domain.NodeRuntime) *fakeRunCancellationNodeRepo {
	repo := &fakeRunCancellationNodeRepo{nodes: map[int64]*domain.NodeRuntime{}, updateCount: map[int64]int{}}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		cloned := *node
		repo.nodes[node.ID] = &cloned
	}
	return repo
}

func (r *fakeRunCancellationNodeRepo) Create(ctx context.Context, n *domain.NodeRuntime) error {
	return nil
}

func (r *fakeRunCancellationNodeRepo) Update(ctx context.Context, n *domain.NodeRuntime) error {
	cloned := *n
	r.nodes[n.ID] = &cloned
	r.updateCount[n.ID]++
	return nil
}

func (r *fakeRunCancellationNodeRepo) FindByTaskID(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error) {
	out := make([]*domain.NodeRuntime, 0)
	for _, node := range r.nodes {
		if node == nil || node.TaskID != taskID {
			continue
		}
		out = append(out, node)
	}
	return out, nil
}

func (r *fakeRunCancellationNodeRepo) FindByTaskIDAndNode(ctx context.Context, taskID int64, node string) (*domain.NodeRuntime, error) {
	for _, runtime := range r.nodes {
		if runtime != nil && runtime.TaskID == taskID && runtime.Name == node {
			return runtime, nil
		}
	}
	return nil, nil
}

func (r *fakeRunCancellationNodeRepo) MarkRunningAsRetrying(ctx context.Context, taskID int64) error {
	return nil
}

func (r *fakeRunCancellationNodeRepo) MarkAsRetrying(ctx context.Context, taskID int64, name string) error {
	return nil
}

func (r *fakeRunCancellationNodeRepo) MarkFailed(ctx context.Context, taskID int64, name string, errMessage string) error {
	return nil
}

func (r *fakeRunCancellationNodeRepo) FindExpiredRunningNodes(ctx context.Context, expire time.Time) ([]*domain.NodeRuntime, error) {
	out := make([]*domain.NodeRuntime, 0)
	for _, node := range r.nodes {
		if node != nil && node.State == domain.NodeRunning && node.LastHeartbeat != nil && node.LastHeartbeat.Before(expire) {
			out = append(out, node)
		}
	}
	return out, nil
}

func (r *fakeRunCancellationNodeRepo) AttemptCompletePendingEdges(ctx context.Context, taskID int64, nodeName string, output map[string]any, errMsg string) (bool, error) {
	return false, nil
}

func (r *fakeRunCancellationNodeRepo) CloneCheckpoint(ctx context.Context, fromTaskID, toTaskID int64) error {
	return nil
}

type fakeRunCancellationAwaitBindingRepo struct {
	bindings    map[int64]*domain.AwaitBinding
	updateCount map[int64]int
}

func newFakeRunCancellationAwaitBindingRepo(bindings ...*domain.AwaitBinding) *fakeRunCancellationAwaitBindingRepo {
	repo := &fakeRunCancellationAwaitBindingRepo{bindings: map[int64]*domain.AwaitBinding{}, updateCount: map[int64]int{}}
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		cloned := *binding
		repo.bindings[binding.ID] = &cloned
	}
	return repo
}

func (r *fakeRunCancellationAwaitBindingRepo) Create(ctx context.Context, b *domain.AwaitBinding) error {
	return nil
}

func (r *fakeRunCancellationAwaitBindingRepo) Update(ctx context.Context, b *domain.AwaitBinding) error {
	cloned := *b
	r.bindings[b.ID] = &cloned
	r.updateCount[b.ID]++
	return nil
}

func (r *fakeRunCancellationAwaitBindingRepo) GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error) {
	return r.bindings[id], nil
}

func (r *fakeRunCancellationAwaitBindingRepo) ListByTaskID(ctx context.Context, taskID int64) ([]*domain.AwaitBinding, error) {
	out := make([]*domain.AwaitBinding, 0)
	for _, binding := range r.bindings {
		if binding == nil || binding.TaskID != taskID {
			continue
		}
		out = append(out, binding)
	}
	return out, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) GetByTaskAndNode(ctx context.Context, taskID int64, nodeName string) (*domain.AwaitBinding, error) {
	return nil, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) FindWaitingByProviderTaskID(ctx context.Context, provider, providerTaskID string) (*domain.AwaitBinding, error) {
	for _, binding := range r.bindings {
		if binding != nil && binding.Status == domain.AwaitBindingWaiting && binding.Provider != nil && *binding.Provider == provider && binding.ProviderTaskID != nil && *binding.ProviderTaskID == providerTaskID {
			return binding, nil
		}
	}
	return nil, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) FindWaitingByAPITaskID(ctx context.Context, provider, apiTaskID string) (*domain.AwaitBinding, error) {
	for _, binding := range r.bindings {
		if binding != nil && binding.Status == domain.AwaitBindingWaiting && binding.Provider != nil && *binding.Provider == provider && binding.APITaskID != nil && *binding.APITaskID == apiTaskID {
			return binding, nil
		}
	}
	return nil, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) FindWaitingBySignal(ctx context.Context, signalName, callbackToken string) (*domain.AwaitBinding, error) {
	for _, binding := range r.bindings {
		if binding != nil && binding.Status == domain.AwaitBindingWaiting && binding.SignalName != nil && *binding.SignalName == signalName && binding.CallbackToken != nil && *binding.CallbackToken == callbackToken {
			return binding, nil
		}
	}
	return nil, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) TransitionStatus(ctx context.Context, id int64, from domain.AwaitBindingStatus, to domain.AwaitBindingStatus) (bool, error) {
	return false, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) ClaimCompleting(ctx context.Context, id int64, expectedStatuses []domain.AwaitBindingStatus) (bool, error) {
	binding := r.bindings[id]
	if binding == nil {
		return false, nil
	}
	for _, status := range expectedStatuses {
		if binding.Status == status {
			binding.Status = domain.AwaitBindingCompleting
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) FindPollDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	out := make([]*domain.AwaitBinding, 0)
	for _, binding := range r.bindings {
		if binding != nil && binding.Status == domain.AwaitBindingWaiting && binding.NextPollAt != nil && !binding.NextPollAt.After(now) {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (r *fakeRunCancellationAwaitBindingRepo) FindTimeoutDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	out := make([]*domain.AwaitBinding, 0)
	for _, binding := range r.bindings {
		if binding != nil && binding.Status == domain.AwaitBindingWaiting && binding.TimeoutAt != nil && !binding.TimeoutAt.After(now) {
			out = append(out, binding)
		}
	}
	return out, nil
}
