package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"flux/domain"
	"flux/engine"
	"flux/eventbus"
	"flux/tool"
	"github.com/stretchr/testify/require"
)

type fakeAwaitPollBindingRepo struct {
	bindings map[int64]*domain.AwaitBinding
}

func newFakeAwaitPollBindingRepo(bindings ...*domain.AwaitBinding) *fakeAwaitPollBindingRepo {
	repo := &fakeAwaitPollBindingRepo{bindings: map[int64]*domain.AwaitBinding{}}
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		cloned := *binding
		repo.bindings[binding.ID] = &cloned
	}
	return repo
}

func (r *fakeAwaitPollBindingRepo) Create(ctx context.Context, b *domain.AwaitBinding) error {
	return nil
}
func (r *fakeAwaitPollBindingRepo) Update(ctx context.Context, b *domain.AwaitBinding) error {
	cloned := *b
	r.bindings[b.ID] = &cloned
	return nil
}
func (r *fakeAwaitPollBindingRepo) GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error) {
	binding := r.bindings[id]
	if binding == nil {
		return nil, nil
	}
	cloned := *binding
	return &cloned, nil
}
func (r *fakeAwaitPollBindingRepo) ListByTaskID(ctx context.Context, taskID int64) ([]*domain.AwaitBinding, error) {
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
func (r *fakeAwaitPollBindingRepo) GetByTaskAndNode(ctx context.Context, taskID int64, nodeName string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitPollBindingRepo) FindWaitingByProviderTaskID(ctx context.Context, provider, providerTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitPollBindingRepo) FindWaitingByAPITaskID(ctx context.Context, provider, apiTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitPollBindingRepo) FindWaitingBySignal(ctx context.Context, signalName, callbackToken string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (r *fakeAwaitPollBindingRepo) TransitionStatus(ctx context.Context, id int64, from domain.AwaitBindingStatus, to domain.AwaitBindingStatus) (bool, error) {
	binding := r.bindings[id]
	if binding == nil || binding.Status != from {
		return false, nil
	}
	if !domain.IsAllowedAwaitBindingTransition(from, to) {
		return false, nil
	}
	binding.Status = to
	return true, nil
}
func (r *fakeAwaitPollBindingRepo) ClaimCompleting(ctx context.Context, id int64, expectedStatuses []domain.AwaitBindingStatus) (bool, error) {
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
func (r *fakeAwaitPollBindingRepo) FindPollDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	var out []*domain.AwaitBinding
	for _, binding := range r.bindings {
		if binding.Status != domain.AwaitBindingWaiting || binding.NextPollAt == nil || binding.NextPollAt.After(now) {
			continue
		}
		cloned := *binding
		out = append(out, &cloned)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (r *fakeAwaitPollBindingRepo) FindTimeoutDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	var out []*domain.AwaitBinding
	for _, binding := range r.bindings {
		if binding.Status != domain.AwaitBindingWaiting || binding.TimeoutAt == nil || binding.TimeoutAt.After(now) {
			continue
		}
		cloned := *binding
		out = append(out, &cloned)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

type fakePollTool struct {
	name   string
	result *tool.Result
	err    error
	input  map[string]any
}

func (t *fakePollTool) Name() string        { return t.name }
func (t *fakePollTool) Description() string { return "fake poll tool" }
func (t *fakePollTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{}
}
func (t *fakePollTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{}
}
func (t *fakePollTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	t.input = input
	return t.result, t.err
}
func (t *fakePollTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func (t *fakePollTool) UsageSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"resource_type":       {Type: "string", Required: true},
			"provider":            {Type: "string", Required: true},
			"provider_request_id": {Type: "string", Required: true},
			"usage_quantity":      {Type: "number", Required: true},
			"usage_unit":          {Type: "string", Required: true},
			"billable":            {Type: "bool", Required: true},
			"billable_stage":      {Type: "string", Required: true},
		},
	}
}

func (t *fakePollTool) BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error) {
	_ = input
	return []map[string]any{
		{
			"resource_type":       "video_generation",
			"provider":            "volcengine",
			"provider_request_id": output["api_task_id"],
			"usage_quantity":      1,
			"usage_unit":          "jobs",
			"billable":            true,
			"billable_stage":      "completed",
		},
	}, nil
}

type fakeAwaitCompleter struct {
	calls          []fakeAwaitCompletionCall
	reconcileCalls []int64
}

type fakeAwaitCompletionCall struct {
	bindingID    int64
	eventPayload map[string]any
	eventErr     string
	source       string
}

func (f *fakeAwaitCompleter) CompleteAwaitNode(bindingID int64, eventPayload map[string]any, eventErr string, source string) engine.RunResult {
	f.calls = append(f.calls, fakeAwaitCompletionCall{
		bindingID:    bindingID,
		eventPayload: eventPayload,
		eventErr:     eventErr,
		source:       source,
	})
	return engine.RunResult{Status: engine.RunSuccess}
}

func (f *fakeAwaitCompleter) ReconcileSubWorkflowBinding(bindingID int64) engine.RunResult {
	f.reconcileCalls = append(f.reconcileCalls, bindingID)
	return engine.RunResult{Status: engine.RunSuccess}
}

func TestAwaitPollWorker_RunOnce_CompletesSuccessfulPoll(t *testing.T) {
	now := time.Now()
	toolName := "fake_poll"
	provider := "kling"
	apiTaskID := "task-1"
	repo := newFakeAwaitPollBindingRepo(&domain.AwaitBinding{
		ID:                  1,
		Status:              domain.AwaitBindingWaiting,
		Provider:            &provider,
		APITaskID:           &apiTaskID,
		FallbackPollEnabled: true,
		FallbackPollTool:    &toolName,
		NextPollAt:          timePtr(now.Add(-time.Minute)),
		Correlation:         map[string]any{"api_task_id": apiTaskID},
		Config: map[string]any{
			"fallback_poll": map[string]any{"interval": "5m"},
		},
	})

	reg := tool.NewRegistry()
	pollTool := &fakePollTool{
		name: toolName,
		result: &tool.Result{
			Success: true,
			Data: map[string]any{
				"video_url":    "https://example.com/result.mp4",
				"api_task_id":  apiTaskID,
				"api_provider": provider,
			},
		},
	}
	reg.Register(pollTool)

	completer := &fakeAwaitCompleter{}
	worker := NewAwaitPollWorker(repo, reg, completer, nil, nil)
	worker.pollTimeout = time.Second

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, completer.calls, 1)
	require.Equal(t, int64(1), completer.calls[0].bindingID)
	require.Equal(t, "poll:"+toolName, completer.calls[0].source)
	require.Equal(t, "https://example.com/result.mp4", completer.calls[0].eventPayload["video_url"])
	usageFacts, ok := completer.calls[0].eventPayload[engine.AwaitUsageFactsMetaKey].([]map[string]any)
	require.True(t, ok)
	require.Len(t, usageFacts, 1)
	require.Equal(t, "video_generation", usageFacts[0]["resource_type"])
	require.Equal(t, 1, repo.bindings[1].PollAttempts)
	require.NotNil(t, repo.bindings[1].LastPolledAt)
	require.Equal(t, 1, pollTool.input["max_retry"])
	require.Equal(t, 0, pollTool.input["poll_interval_ms"])
}

func TestBuildAwaitPollInput_IncludesModelPassthrough(t *testing.T) {
	provider := "aliyun"
	apiTaskID := "task-model"
	input := buildAwaitPollInput(&domain.AwaitBinding{
		Provider:  &provider,
		APITaskID: &apiTaskID,
		Correlation: map[string]any{
			"api_task_id": apiTaskID,
			"model":       "wan2.7-image",
		},
	})

	require.Equal(t, apiTaskID, input["api_task_id"])
	require.Equal(t, provider, input["api_provider"])
	require.Equal(t, "wan2.7-image", input["model"])
}

func TestAwaitPollWorker_RunOnce_ReschedulesNonTerminalPoll(t *testing.T) {
	now := time.Now()
	toolName := "fake_poll"
	provider := "volcengine"
	apiTaskID := "task-2"
	repo := newFakeAwaitPollBindingRepo(&domain.AwaitBinding{
		ID:                  2,
		Status:              domain.AwaitBindingWaiting,
		Provider:            &provider,
		APITaskID:           &apiTaskID,
		FallbackPollEnabled: true,
		FallbackPollTool:    &toolName,
		NextPollAt:          timePtr(now.Add(-time.Minute)),
		Correlation:         map[string]any{"api_task_id": apiTaskID},
		Config: map[string]any{
			"fallback_poll": map[string]any{"interval": "3m"},
		},
	})

	reg := tool.NewRegistry()
	reg.Register(&fakePollTool{
		name: toolName,
		err:  errors.New("轮询超时"),
	})

	bus := eventbus.NewEventBus(nil, nil)
	missCh := bus.Subscribe("await_poll_miss")
	completer := &fakeAwaitCompleter{}
	worker := NewAwaitPollWorker(repo, reg, completer, bus, nil)
	worker.pollTimeout = time.Second

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Empty(t, completer.calls)
	require.Equal(t, 1, repo.bindings[2].PollAttempts)
	require.NotNil(t, repo.bindings[2].NextPollAt)
	require.True(t, repo.bindings[2].NextPollAt.After(now))

	select {
	case evt := <-missCh:
		require.Equal(t, "await_poll_miss", evt.Type)
		require.Equal(t, "volcengine", evt.Meta["provider"])
		require.Equal(t, float64(0), evt.Progress)
	default:
		t.Fatal("expected await_poll_miss event")
	}
}

func TestAwaitPollWorker_RunOnce_PrefersPollOnceAlias(t *testing.T) {
	now := time.Now()
	legacyToolName := "aliyun_image_generate_wait"
	preferredToolName := "aliyun_image_generate_poll_once"
	provider := "aliyun"
	apiTaskID := "task-alias-1"
	repo := newFakeAwaitPollBindingRepo(&domain.AwaitBinding{
		ID:                  21,
		Status:              domain.AwaitBindingWaiting,
		Provider:            &provider,
		APITaskID:           &apiTaskID,
		ProviderTaskID:      &apiTaskID,
		FallbackPollEnabled: true,
		FallbackPollTool:    &legacyToolName,
		NextPollAt:          timePtr(now.Add(-time.Minute)),
		Correlation:         map[string]any{"api_task_id": apiTaskID},
		Config: map[string]any{
			"fallback_poll": map[string]any{"interval": "5m"},
		},
	})

	reg := tool.NewRegistry()
	pollTool := &fakePollTool{
		name: preferredToolName,
		result: &tool.Result{
			Success: true,
			Data: map[string]any{
				"image_url":        "https://example.com/result.png",
				"provider_task_id": apiTaskID,
				"api_provider":     provider,
			},
		},
	}
	reg.Register(pollTool)

	completer := &fakeAwaitCompleter{}
	worker := NewAwaitPollWorker(repo, reg, completer, nil, nil)
	worker.pollTimeout = time.Second

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, completer.calls, 1)
	require.Equal(t, "poll:"+preferredToolName, completer.calls[0].source)
	require.Equal(t, 1, pollTool.input["max_retry"])
}

func TestAwaitPollWorker_RunOnce_EmitsTimeoutEvent(t *testing.T) {
	now := time.Now()
	provider := "kling"
	apiTaskID := "task-3"
	repo := newFakeAwaitPollBindingRepo(&domain.AwaitBinding{
		ID:             3,
		TaskID:         3001,
		RootTaskID:     3001,
		NodeName:       "await_kling",
		Status:         domain.AwaitBindingWaiting,
		Provider:       &provider,
		APITaskID:      &apiTaskID,
		ProviderTaskID: &apiTaskID,
		TimeoutAt:      timePtr(now.Add(-time.Minute)),
	})

	bus := eventbus.NewEventBus(nil, nil)
	timeoutCh := bus.Subscribe("await_timeout")
	completer := &fakeAwaitCompleter{}
	worker := NewAwaitPollWorker(repo, tool.NewRegistry(), completer, bus, nil)

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, completer.calls, 1)
	require.Equal(t, "timeout", completer.calls[0].source)
	require.Equal(t, "await timeout", completer.calls[0].eventErr)

	select {
	case evt := <-timeoutCh:
		require.Equal(t, "await_timeout", evt.Type)
		require.Equal(t, "await_kling", evt.Step)
		require.Equal(t, provider, evt.Meta["provider"])
	default:
		t.Fatal("expected await_timeout event")
	}
}

func TestAwaitPollWorker_RunOnce_EmitsMaxAttemptsExceededEvent(t *testing.T) {
	now := time.Now()
	toolName := "fake_poll"
	provider := "kling"
	apiTaskID := "task-4"
	repo := newFakeAwaitPollBindingRepo(&domain.AwaitBinding{
		ID:                  4,
		TaskID:              4001,
		RootTaskID:          4001,
		NodeName:            "await_kling",
		Status:              domain.AwaitBindingWaiting,
		Provider:            &provider,
		APITaskID:           &apiTaskID,
		FallbackPollEnabled: true,
		FallbackPollTool:    &toolName,
		PollAttempts:        1,
		MaxPollAttempts:     1,
		NextPollAt:          timePtr(now.Add(-time.Minute)),
		Correlation:         map[string]any{"api_task_id": apiTaskID},
	})

	reg := tool.NewRegistry()
	reg.Register(&fakePollTool{
		name: toolName,
		err:  errors.New("轮询超时"),
	})

	bus := eventbus.NewEventBus(nil, nil)
	maxCh := bus.Subscribe("await_poll_max_attempts_exceeded")
	completer := &fakeAwaitCompleter{}
	worker := NewAwaitPollWorker(repo, reg, completer, bus, nil)

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, completer.calls, 1)
	require.Equal(t, "poll:max_attempts", completer.calls[0].source)
	require.Equal(t, "await fallback poll attempts exceeded", completer.calls[0].eventErr)

	select {
	case evt := <-maxCh:
		require.Equal(t, "await_poll_max_attempts_exceeded", evt.Type)
		require.Equal(t, float64(2), toFloat64(evt.Meta["poll_attempts"]))
		require.Equal(t, float64(1), toFloat64(evt.Meta["max_poll_attempts"]))
	default:
		t.Fatal("expected await_poll_max_attempts_exceeded event")
	}
}

func TestAwaitPollWorker_RunOnce_TreatsMissingImageURLAsTerminal(t *testing.T) {
	now := time.Now()
	toolName := "aliyun_image_to_image_poll_once"
	provider := "aliyun"
	apiTaskID := "task-missing-url"
	repo := newFakeAwaitPollBindingRepo(&domain.AwaitBinding{
		ID:                  5,
		TaskID:              5001,
		RootTaskID:          5001,
		NodeName:            "aliyun_wait",
		Status:              domain.AwaitBindingWaiting,
		Provider:            &provider,
		APITaskID:           &apiTaskID,
		ProviderTaskID:      &apiTaskID,
		FallbackPollEnabled: true,
		FallbackPollTool:    &toolName,
		NextPollAt:          timePtr(now.Add(-time.Minute)),
		Correlation:         map[string]any{"api_task_id": apiTaskID},
	})

	reg := tool.NewRegistry()
	reg.Register(&fakePollTool{
		name: toolName,
		err:  errors.New("阿里云百炼图片任务成功但未返回图片 URL"),
	})

	completer := &fakeAwaitCompleter{}
	worker := NewAwaitPollWorker(repo, reg, completer, nil, nil)

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, completer.calls, 1)
	require.Equal(t, int64(5), completer.calls[0].bindingID)
	require.Equal(t, "poll:"+toolName, completer.calls[0].source)
	require.Equal(t, "阿里云百炼图片任务成功但未返回图片 URL", completer.calls[0].eventErr)
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func toFloat64(v any) float64 {
	switch typed := v.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	default:
		return 0
	}
}
