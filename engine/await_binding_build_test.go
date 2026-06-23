package engine

import (
	"context"
	"testing"

	"flux/definition"
	"flux/domain"
	"flux/eventbus"
	"flux/pkg/uuid"
	"flux/workflow/nodes"
	"github.com/stretchr/testify/require"
)

func TestBuildAwaitBinding_ResolvesProviderFromInput(t *testing.T) {
	e := &Engine{}
	runCtx := &nodes.Context{
		Task: &domain.Task{
			ID:                101,
			RootID:            101,
			WorkflowVersionID: 11,
		},
	}
	node := nodes.Node{
		Name: "video_generate_wait",
		Type: definition.NodeAwait,
		Config: map[string]any{
			"await_type": "external_task",
			"source":     "webhook_or_poll",
			"provider":   "api_provider",
			"correlation": map[string]any{
				"provider_task_id": "api_task_id",
				"api_task_id":      "api_task_id",
			},
		},
	}
	execCtx := &nodes.NodeExecContext{
		Input: map[string]any{
			"api_provider": "doubao",
			"api_task_id":  "task-123",
		},
	}

	binding, err := e.buildAwaitBinding(runCtx, node, execCtx)
	require.NoError(t, err)
	require.NotNil(t, binding.Provider)
	require.Equal(t, "doubao", *binding.Provider)
	require.NotNil(t, binding.ProviderTaskID)
	require.Equal(t, "task-123", *binding.ProviderTaskID)
}

func TestExecuteAwaitNode_ReusesFailedBindingWithoutCreate(t *testing.T) {
	repo := newFakeAwaitBindingRepo(&domain.AwaitBinding{
		ID:                12,
		TaskID:            101,
		RootTaskID:        101,
		NodeName:          "volcengine_wait",
		WorkflowVersionID: 11,
		AwaitType:         domain.AwaitTypeExternalTask,
		Source:            domain.AwaitSourceWebhookOrPoll,
		Status:            domain.AwaitBindingFailed,
		ErrorMessage:      "old failure",
		ProviderTaskID:    optionalStringPtr("old-task"),
		APITaskID:         optionalStringPtr("old-task"),
		PollAttempts:      3,
	})
	e := &Engine{
		awaitBindingRepo: repo,
		nodeRepo:         newFakeNodeRepo(),
		taskRepo:         newFakeTaskRepo(&domain.Task{ID: 101, RootID: 101}),
		iSrv:             *uuid.NewNode(3),
	}
	runCtx := &nodes.Context{
		Ctx: context.Background(),
		Task: &domain.Task{
			ID:                101,
			RootID:            101,
			WorkflowVersionID: 11,
		},
		Runtime:        map[string]*domain.NodeRuntime{},
		Output:         map[string]any{"nodes": map[string]any{}},
		EventBus:       eventbus.NewEventBus(nil, nil),
		ActivatedEdges: map[string]bool{},
	}
	runtime := &domain.NodeRuntime{
		TaskID: 101,
		Name:   "volcengine_wait",
		State:  domain.NodeRunning,
	}
	runCtx.Runtime[runtime.Name] = runtime
	node := nodes.Node{
		Name: "volcengine_wait",
		Type: definition.NodeAwait,
		Config: map[string]any{
			"await_type": "external_task",
			"source":     "webhook_or_poll",
			"provider":   "api_provider",
			"correlation": map[string]any{
				"provider_task_id": "api_task_id",
				"api_task_id":      "api_task_id",
			},
		},
	}
	execCtx := &nodes.NodeExecContext{
		Input: map[string]any{
			"api_provider": "volcengine",
			"api_task_id":  "task-456",
		},
	}

	err := e.executeAwaitNode(runCtx, runtime, node, execCtx)
	var suspended *domain.WorkflowSuspendedError
	require.ErrorAs(t, err, &suspended)
	require.Equal(t, 0, repo.createCount)
	require.Equal(t, 1, repo.updateCount)
	require.Equal(t, domain.NodeAwaiting, runtime.State)

	binding, getErr := repo.GetByID(runCtx.Ctx, 12)
	require.NoError(t, getErr)
	require.NotNil(t, binding)
	require.Equal(t, domain.AwaitBindingWaiting, binding.Status)
	require.Empty(t, binding.ErrorMessage)
	require.Equal(t, 0, binding.PollAttempts)
	require.NotNil(t, binding.ProviderTaskID)
	require.Equal(t, "task-456", *binding.ProviderTaskID)
	require.NotNil(t, binding.APITaskID)
	require.Equal(t, "task-456", *binding.APITaskID)
}

func TestExecuteAwaitNode_LeavesInFlightBindingUntouched(t *testing.T) {
	repo := newFakeAwaitBindingRepo(&domain.AwaitBinding{
		ID:                18,
		TaskID:            102,
		RootTaskID:        102,
		NodeName:          "volcengine_wait",
		WorkflowVersionID: 11,
		AwaitType:         domain.AwaitTypeExternalTask,
		Source:            domain.AwaitSourceWebhookOrPoll,
		Status:            domain.AwaitBindingWaiting,
	})
	e := &Engine{
		awaitBindingRepo: repo,
		nodeRepo:         newFakeNodeRepo(),
		taskRepo:         newFakeTaskRepo(&domain.Task{ID: 102, RootID: 102}),
		iSrv:             *uuid.NewNode(3),
	}
	runCtx := &nodes.Context{
		Ctx: context.Background(),
		Task: &domain.Task{
			ID:                102,
			RootID:            102,
			WorkflowVersionID: 11,
		},
		Runtime:        map[string]*domain.NodeRuntime{},
		Output:         map[string]any{"nodes": map[string]any{}},
		EventBus:       eventbus.NewEventBus(nil, nil),
		ActivatedEdges: map[string]bool{},
	}
	runtime := &domain.NodeRuntime{
		TaskID: 102,
		Name:   "volcengine_wait",
		State:  domain.NodeRunning,
	}
	runCtx.Runtime[runtime.Name] = runtime
	node := nodes.Node{
		Name: "volcengine_wait",
		Type: definition.NodeAwait,
		Config: map[string]any{
			"await_type": "external_task",
			"source":     "webhook_or_poll",
		},
	}
	execCtx := &nodes.NodeExecContext{Input: map[string]any{}}

	err := e.executeAwaitNode(runCtx, runtime, node, execCtx)
	var suspended *domain.WorkflowSuspendedError
	require.ErrorAs(t, err, &suspended)
	require.Equal(t, 0, repo.createCount)
	require.Equal(t, 0, repo.updateCount)
	require.Equal(t, domain.NodeAwaiting, runtime.State)
}

func TestFakeAwaitBindingRepo_CreateRejectsDuplicateID(t *testing.T) {
	repo := newFakeAwaitBindingRepo(&domain.AwaitBinding{ID: 7, TaskID: 1, NodeName: "wait"})
	err := repo.Create(nil, &domain.AwaitBinding{ID: 7, TaskID: 1, NodeName: "wait"})
	require.Error(t, err)
}
