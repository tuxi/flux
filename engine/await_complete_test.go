package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"flux/domain"
	"github.com/stretchr/testify/require"
)

type fakeAwaitBindingRepo struct {
	bindings    map[int64]*domain.AwaitBinding
	createCount int
	updateCount int
}

func newFakeAwaitBindingRepo(bindings ...*domain.AwaitBinding) *fakeAwaitBindingRepo {
	repo := &fakeAwaitBindingRepo{bindings: map[int64]*domain.AwaitBinding{}}
	for _, binding := range bindings {
		if binding != nil {
			cloned := *binding
			repo.bindings[binding.ID] = &cloned
		}
	}
	return repo
}

func (r *fakeAwaitBindingRepo) Create(ctx context.Context, b *domain.AwaitBinding) error {
	if b.ID == 0 {
		b.ID = int64(len(r.bindings) + 1)
	}
	if _, exists := r.bindings[b.ID]; exists {
		return fmt.Errorf("duplicate binding id: %d", b.ID)
	}
	cloned := *b
	r.bindings[b.ID] = &cloned
	r.createCount++
	return nil
}
func (r *fakeAwaitBindingRepo) Update(ctx context.Context, b *domain.AwaitBinding) error {
	cloned := *b
	r.bindings[b.ID] = &cloned
	r.updateCount++
	return nil
}
func (r *fakeAwaitBindingRepo) GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error) {
	if b, ok := r.bindings[id]; ok {
		cloned := *b
		return &cloned, nil
	}
	return nil, nil
}
func (r *fakeAwaitBindingRepo) ListByTaskID(ctx context.Context, taskID int64) ([]*domain.AwaitBinding, error) {
	out := make([]*domain.AwaitBinding, 0)
	for _, b := range r.bindings {
		if b.TaskID != taskID {
			continue
		}
		cloned := *b
		out = append(out, &cloned)
	}
	return out, nil
}
func (r *fakeAwaitBindingRepo) GetByTaskAndNode(ctx context.Context, taskID int64, nodeName string) (*domain.AwaitBinding, error) {
	for _, b := range r.bindings {
		if b.TaskID == taskID && b.NodeName == nodeName {
			cloned := *b
			return &cloned, nil
		}
	}
	return nil, nil
}
func (r *fakeAwaitBindingRepo) FindWaitingByProviderTaskID(ctx context.Context, provider, providerTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitBindingRepo) FindWaitingByAPITaskID(ctx context.Context, provider, apiTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitBindingRepo) FindWaitingBySignal(ctx context.Context, signalName, callbackToken string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitBindingRepo) TransitionStatus(ctx context.Context, id int64, from domain.AwaitBindingStatus, to domain.AwaitBindingStatus) (bool, error) {
	b, ok := r.bindings[id]
	if !ok || b.Status != from {
		return false, nil
	}
	if !domain.IsAllowedAwaitBindingTransition(from, to) {
		return false, nil
	}
	b.Status = to
	return true, nil
}
func (r *fakeAwaitBindingRepo) ClaimCompleting(ctx context.Context, id int64, expectedStatuses []domain.AwaitBindingStatus) (bool, error) {
	b, ok := r.bindings[id]
	if !ok {
		return false, nil
	}
	for _, status := range expectedStatuses {
		if b.Status == status {
			b.Status = domain.AwaitBindingCompleting
			return true, nil
		}
	}
	return false, nil
}
func (r *fakeAwaitBindingRepo) FindPollDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitBindingRepo) FindTimeoutDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return nil, nil
}

func TestCompleteAwaitNode_ResumesSuspendedTask(t *testing.T) {
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)

	task := &domain.Task{
		ID:                9001,
		RootID:            9001,
		Status:            domain.TaskSuspended,
		WorkflowVersionID: version.ID,
	}
	taskRepo := newFakeTaskRepo(task)
	nodeRepo := newFakeNodeRepo()
	nodeRepo.nodes[task.ID] = map[string]*domain.NodeRuntime{}
	nodeRepo.nodes[task.ID]["start"] = &domain.NodeRuntime{
		TaskID:         task.ID,
		Name:           "start",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"start->async_generate": true},
		Output:         map[string]any{},
		ResolvedInput:  map[string]any{},
	}
	nodeRepo.nodes[task.ID]["async_generate"] = &domain.NodeRuntime{
		TaskID:         task.ID,
		Name:           "async_generate",
		State:          domain.NodeAwaiting,
		ActivatedEdges: map[string]bool{"async_generate->end": true},
	}
	nodeRepo.nodes[task.ID]["end"] = &domain.NodeRuntime{
		TaskID: task.ID,
		Name:   "end",
		State:  domain.NodePending,
	}

	awaitRepo := newFakeAwaitBindingRepo(&domain.AwaitBinding{
		ID:                7001,
		TaskID:            task.ID,
		RootTaskID:        task.RootID,
		NodeName:          "async_generate",
		WorkflowVersionID: version.ID,
		AwaitType:         domain.AwaitTypeExternalTask,
		Source:            domain.AwaitSourceWebhook,
		Status:            domain.AwaitBindingWaiting,
	})

	e := newEngineForTests(builder, taskRepo, nodeRepo, newFakeWorkflowVersionRepo(version), newFakeWorkflowRepo(dbWorkflow))
	e.awaitBindingRepo = awaitRepo

	result := e.CompleteAwaitNode(7001, map[string]any{"url": "https://example.com/from-webhook.mp4"}, "", "webhook:test")
	require.Equal(t, RunSuccess, result.Status)

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskSuccess, updatedTask.Status)

	runtime, err := nodeRepo.FindByTaskIDAndNode(context.Background(), task.ID, "async_generate")
	require.NoError(t, err)
	require.Equal(t, domain.NodeSuccess, runtime.State)
	require.Equal(t, "https://example.com/from-webhook.mp4", runtime.Output["url"])

	binding, err := awaitRepo.GetByID(context.Background(), 7001)
	require.NoError(t, err)
	require.Equal(t, domain.AwaitBindingCompleted, binding.Status)

	var output map[string]any
	err = json.Unmarshal(updatedTask.OutputJSON, &output)
	require.NoError(t, err)
	final := output["final"].(map[string]any)
	require.Equal(t, "https://example.com/from-webhook.mp4", final["primary_file_url"])
}
