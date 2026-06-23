package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"flux/domain"
	"flux/engine"
	"flux/eventbus"
	"flux/pkg/lock"
	"flux/repository"
	"flux/tool"
)

type awaitCompleter interface {
	CompleteAwaitNode(bindingID int64, eventPayload map[string]any, eventErr string, source string) engine.RunResult
	// ReconcileSubWorkflowBinding 对 subworkflow binding 做 poll 对账（P2 兜底）：
	// 直接查子任务终态，完成/重排 binding。逻辑落在 engine（持有 taskRepo + CompleteAwaitNode）。
	ReconcileSubWorkflowBinding(bindingID int64) engine.RunResult
}

type AwaitPollWorker struct {
	awaitBindingRepo repository.AwaitBindingRepository
	tools            *tool.Registry
	completer        awaitCompleter
	eventBus         *eventbus.EventBus
	dLocker          lock.DistributedLock
	scanInterval     time.Duration
	pollTimeout      time.Duration
	batchSize        int
}

func NewAwaitPollWorker(
	awaitBindingRepo repository.AwaitBindingRepository,
	tools *tool.Registry,
	completer awaitCompleter,
	eventBus *eventbus.EventBus,
	dLocker lock.DistributedLock,
) *AwaitPollWorker {
	return &AwaitPollWorker{
		awaitBindingRepo: awaitBindingRepo,
		tools:            tools,
		completer:        completer,
		eventBus:         eventBus,
		dLocker:          dLocker,
		scanInterval:     15 * time.Second,
		pollTimeout:      20 * time.Second,
		batchSize:        32,
	}
}

func StartAwaitPollWorkers(ctx context.Context, worker *AwaitPollWorker, n int) {
	if worker == nil || n <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		go worker.Start(ctx)
	}
}

func (w *AwaitPollWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	ticker := time.NewTicker(w.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = w.RunOnce(ctx)
		}
	}
}

func (w *AwaitPollWorker) RunOnce(ctx context.Context) (int, error) {
	if w.awaitBindingRepo == nil || w.tools == nil || w.completer == nil {
		return 0, nil
	}

	now := time.Now()
	processed := 0

	timeoutDue, err := w.awaitBindingRepo.FindTimeoutDue(ctx, now, w.batchSize)
	if err != nil {
		return processed, err
	}
	for _, binding := range timeoutDue {
		ok, err := w.processTimeoutBinding(ctx, binding, now)
		if err != nil {
			return processed, err
		}
		if ok {
			processed++
		}
	}

	// FindPollDue 返回所有 waiting 且 next_poll_at 到期的 binding（含 subworkflow）。
	// subworkflow binding 走对账分支（查子任务终态），其余走原 fallback-poll（执行工具）。
	pollDue, err := w.awaitBindingRepo.FindPollDue(ctx, now, w.batchSize)
	if err != nil {
		return processed, err
	}
	for _, binding := range pollDue {
		if binding == nil {
			continue
		}
		if binding.AwaitType == domain.AwaitTypeSubWorkflow {
			if w.completer.ReconcileSubWorkflowBinding(binding.ID).Status != engine.RunFailed {
				processed++
			}
			continue
		}
		ok, err := w.processPollBinding(ctx, binding, now)
		if err != nil {
			return processed, err
		}
		if ok {
			processed++
		}
	}

	return processed, nil
}

func (w *AwaitPollWorker) processTimeoutBinding(ctx context.Context, binding *domain.AwaitBinding, now time.Time) (bool, error) {
	if binding == nil || binding.Status != domain.AwaitBindingWaiting {
		return false, nil
	}

	unlock, ok, err := w.lockBinding(ctx, binding.ID, "await-timeout")
	if err != nil || !ok {
		return false, err
	}
	defer unlock()

	refreshed, err := w.awaitBindingRepo.GetByID(ctx, binding.ID)
	if err != nil || refreshed == nil || refreshed.Status != domain.AwaitBindingWaiting {
		return false, err
	}
	w.publishBindingEvent(refreshed, "await_timeout", "await timeout reached", "warn", map[string]any{
		"timeout_at": refreshed.TimeoutAt,
	})

	eventErr := "await timeout"
	if awaitBoolConfig(refreshed.Config, "failure_as_output") {
		eventErr = ""
	}
	result := w.completer.CompleteAwaitNode(
		refreshed.ID,
		buildAwaitFailurePayload(refreshed),
		eventErr,
		"timeout",
	)
	if result.Status == engine.RunFailed {
		return true, result.Err
	}
	return true, nil
}

func (w *AwaitPollWorker) processPollBinding(ctx context.Context, binding *domain.AwaitBinding, now time.Time) (bool, error) {
	if binding == nil || binding.Status != domain.AwaitBindingWaiting || !binding.FallbackPollEnabled || binding.FallbackPollTool == nil {
		return false, nil
	}

	unlock, ok, err := w.lockBinding(ctx, binding.ID, "await-poll")
	if err != nil || !ok {
		return false, err
	}
	defer unlock()

	refreshed, err := w.awaitBindingRepo.GetByID(ctx, binding.ID)
	if err != nil {
		return false, err
	}
	if refreshed == nil || refreshed.Status != domain.AwaitBindingWaiting || !refreshed.FallbackPollEnabled || refreshed.FallbackPollTool == nil {
		return false, nil
	}

	refreshed.LastPolledAt = &now
	refreshed.PollAttempts++

	if refreshed.MaxPollAttempts > 0 && refreshed.PollAttempts > refreshed.MaxPollAttempts {
		if updateErr := w.awaitBindingRepo.Update(ctx, refreshed); updateErr != nil {
			return false, updateErr
		}
		w.publishBindingEvent(refreshed, "await_poll_max_attempts_exceeded", "await fallback poll max attempts exceeded", "warn", map[string]any{
			"poll_attempts":      refreshed.PollAttempts,
			"max_poll_attempts":  refreshed.MaxPollAttempts,
			"fallback_poll_tool": valueOrNilString(refreshed.FallbackPollTool),
		})
		eventErr := "await fallback poll attempts exceeded"
		if awaitBoolConfig(refreshed.Config, "failure_as_output") {
			eventErr = ""
		}
		result := w.completer.CompleteAwaitNode(
			refreshed.ID,
			buildAwaitFailurePayload(refreshed),
			eventErr,
			"poll:max_attempts",
		)
		if result.Status == engine.RunFailed {
			return true, result.Err
		}
		return true, nil
	}

	result, usageFacts, executedToolName, execErr := w.executePollTool(ctx, refreshed)
	if result != nil && result.Data != nil && execErr == nil {
		if updateErr := w.awaitBindingRepo.Update(ctx, refreshed); updateErr != nil {
			return false, updateErr
		}
		eventPayload := cloneAwaitPayload(result.Data)
		if len(usageFacts) > 0 {
			eventPayload[engine.AwaitUsageFactsMetaKey] = usageFacts
		}
		complete := w.completer.CompleteAwaitNode(
			refreshed.ID,
			eventPayload,
			"",
			"poll:"+executedToolName,
		)
		if complete.Status == engine.RunFailed {
			return true, complete.Err
		}
		return true, nil
	}

	if execErr != nil && isTerminalPollError(execErr) {
		if updateErr := w.awaitBindingRepo.Update(ctx, refreshed); updateErr != nil {
			return false, updateErr
		}
		eventErr := execErr.Error()
		if awaitBoolConfig(refreshed.Config, "failure_as_output") {
			eventErr = ""
		}
		complete := w.completer.CompleteAwaitNode(
			refreshed.ID,
			buildAwaitFailurePayload(refreshed),
			eventErr,
			"poll:"+executedToolName,
		)
		if complete.Status == engine.RunFailed {
			return true, complete.Err
		}
		return true, nil
	}

	nextPollAt := now.Add(awaitPollInterval(refreshed))
	refreshed.NextPollAt = &nextPollAt
	if err := w.awaitBindingRepo.Update(ctx, refreshed); err != nil {
		return false, err
	}
	w.publishBindingEvent(refreshed, "await_poll_miss", "await fallback poll did not reach terminal state", "info", map[string]any{
		"poll_attempts":      refreshed.PollAttempts,
		"max_poll_attempts":  refreshed.MaxPollAttempts,
		"fallback_poll_tool": valueOrNilString(refreshed.FallbackPollTool),
		"next_poll_at":       refreshed.NextPollAt,
		"last_polled_at":     refreshed.LastPolledAt,
		"poll_error":         errorString(execErr),
	})
	return true, nil
}

func (w *AwaitPollWorker) executePollTool(ctx context.Context, binding *domain.AwaitBinding) (*tool.Result, []map[string]any, string, error) {
	toolName := *binding.FallbackPollTool
	resolvedToolName, toolImpl, ok := tool.ResolvePreferredPollTool(w.tools, toolName)
	if !ok {
		return nil, nil, "", fmt.Errorf("await fallback poll tool not found: %s", toolName)
	}

	input := buildAwaitPollInput(binding)
	input["max_retry"] = 1
	input["poll_interval_ms"] = 0

	execCtx := ctx
	var cancel context.CancelFunc
	if w.pollTimeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, w.pollTimeout)
		defer cancel()
	}

	emitter := &eventBusEmitter{
		eventBus:   w.eventBus,
		taskID:     binding.TaskID,
		rootTaskID: binding.RootTaskID,
		nodeName:   binding.NodeName,
	}
	result, err := toolImpl.Execute(execCtx, input, emitter)
	if err != nil || result == nil || result.Data == nil {
		return result, nil, resolvedToolName, err
	}

	var usageFacts []map[string]any
	if aware, ok := toolImpl.(tool.UsageAware); ok {
		usageFacts, _ = aware.BuildUsageFacts(input, result.Data)
	}

	return result, usageFacts, resolvedToolName, err
}

func (w *AwaitPollWorker) lockBinding(ctx context.Context, bindingID int64, prefix string) (func(), bool, error) {
	if w.dLocker == nil {
		return func() {}, true, nil
	}
	key := fmt.Sprintf("%s:%d", prefix, bindingID)
	locked, unlock, err := w.dLocker.Lock(ctx, key, 30*time.Second)
	if err != nil || !locked {
		return nil, false, err
	}
	return unlock, true, nil
}

func buildAwaitPollInput(binding *domain.AwaitBinding) map[string]any {
	input := map[string]any{}
	for key, value := range binding.Correlation {
		input[key] = value
	}
	if binding.Provider != nil {
		input["api_provider"] = *binding.Provider
	}
	if binding.ProviderTaskID != nil && input["provider_task_id"] == nil {
		input["provider_task_id"] = *binding.ProviderTaskID
	}
	if binding.APITaskID != nil && input["api_task_id"] == nil {
		input["api_task_id"] = *binding.APITaskID
	}
	if binding.ExternalTaskID != nil && input["external_task_id"] == nil {
		input["external_task_id"] = *binding.ExternalTaskID
	}
	if binding.TimeoutAt != nil && !binding.TimeoutAt.IsZero() {
		input["timeout_at"] = binding.TimeoutAt.Unix()
	}
	return input
}

func buildAwaitFailurePayload(binding *domain.AwaitBinding) map[string]any {
	payload := map[string]any{}
	if binding.Provider != nil {
		payload["api_provider"] = *binding.Provider
	}
	if binding.APITaskID != nil {
		payload["api_task_id"] = *binding.APITaskID
	} else if binding.ProviderTaskID != nil {
		payload["api_task_id"] = *binding.ProviderTaskID
	}
	if binding.ProviderTaskID != nil {
		payload["provider_task_id"] = *binding.ProviderTaskID
	}
	return payload
}

func cloneAwaitPayload(data map[string]any) map[string]any {
	if data == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(data))
	for k, v := range data {
		cloned[k] = v
	}
	return cloned
}

func awaitPollInterval(binding *domain.AwaitBinding) time.Duration {
	if binding == nil {
		return 3 * time.Minute
	}
	fallback, ok := binding.Config["fallback_poll"].(map[string]any)
	if ok {
		if duration, ok := parseAwaitDuration(fallback["interval"]); ok && duration > 0 {
			return duration
		}
		if duration, ok := parseAwaitDuration(fallback["start_after"]); ok && duration > 0 {
			return duration
		}
	}
	return 3 * time.Minute
}

func awaitBoolConfig(config map[string]any, key string) bool {
	if config == nil {
		return false
	}
	switch v := config[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func parseAwaitDuration(raw any) (time.Duration, bool) {
	switch v := raw.(type) {
	case nil:
		return 0, false
	case time.Duration:
		return v, true
	case int:
		return time.Duration(v) * time.Second, true
	case int32:
		return time.Duration(v) * time.Second, true
	case int64:
		return time.Duration(v) * time.Second, true
	case float32:
		return time.Duration(v * float32(time.Second)), true
	case float64:
		return time.Duration(v * float64(time.Second)), true
	case string:
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, false
		}
		return d, true
	default:
		return 0, false
	}
}

func isTerminalPollError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "失败"),
		strings.Contains(msg, "failed"),
		strings.Contains(msg, "任务失败"),
		strings.Contains(msg, "生成失败"),
		strings.Contains(msg, "未返回图片 url"),
		strings.Contains(msg, "未返回图片url"),
		strings.Contains(msg, "empty image url"),
		strings.Contains(msg, "missing image url"):
		return true
	case strings.Contains(msg, "timeout"),
		strings.Contains(msg, "超时"),
		strings.Contains(msg, "deadline exceeded"):
		return false
	default:
		return false
	}
}

type noopToolEmitter struct{}

func (noopToolEmitter) EmitToolEvent(event tool.ToolEvent) {}

// eventBusEmitter converts ToolEvent to TaskEvent and publishes via EventBus.
type eventBusEmitter struct {
	eventBus   *eventbus.EventBus
	taskID     int64
	rootTaskID int64
	nodeName   string
}

func (e *eventBusEmitter) EmitToolEvent(event tool.ToolEvent) {
	if e.eventBus == nil {
		return
	}
	eventType := event.CustomType
	if eventType == "" {
		eventType = "tool_" + event.Type
	}
	errMsg := ""
	if v, ok := event.Data["error"].(string); ok {
		errMsg = v
	}
	e.eventBus.Publish(e.rootTaskID, &domain.TaskEvent{
		TaskID:     e.taskID,
		RootTaskID: e.rootTaskID,
		Step:       e.nodeName,
		Type:       eventType,
		Message:    event.Message,
		Error:      errMsg,
		Progress:   event.Progress,
		Meta:       event.Data,
		CreatedAt:  time.Now(),
		Level:      event.LogLevel,
		Grade:      domain.GradeTransient,
	})
}

func (w *AwaitPollWorker) publishBindingEvent(binding *domain.AwaitBinding, eventType, message, level string, meta map[string]any) {
	if w == nil || w.eventBus == nil || binding == nil {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["binding_id"] = binding.ID
	meta["await_type"] = binding.AwaitType
	meta["await_source"] = binding.Source
	meta["binding_status"] = binding.Status
	meta["provider"] = valueOrNilString(binding.Provider)
	meta["provider_task_id"] = valueOrNilString(binding.ProviderTaskID)
	meta["api_task_id"] = valueOrNilString(binding.APITaskID)
	meta["external_task_id"] = valueOrNilString(binding.ExternalTaskID)
	meta["node_name"] = binding.NodeName
	meta["task_id"] = binding.TaskID
	meta["root_task_id"] = binding.RootTaskID

	w.eventBus.Publish(binding.RootTaskID, &domain.TaskEvent{
		TaskID:     binding.TaskID,
		RootTaskID: binding.RootTaskID,
		Step:       binding.NodeName,
		Message:    message,
		Meta:       meta,
		CreatedAt:  time.Now(),
		Type:       eventType,
		Level:      level,
		Grade:      awaitBindingEventGrade(eventType),
	})
}

// awaitBindingEventGrade 为 publishBindingEvent 显式设定 Grade，避免依赖 inferGrade 的字符串匹配。
//
//   - await_poll_miss：fallback_poll 每次轮询未到终态时发出，频率与 interval 配置相同（短剧 30s/次）。
//     纯诊断信息，无需持久化，定为 Transient（WS only）。
//   - await_timeout / await_poll_max_attempts_exceeded：任务生命周期终态事件，定为 Persistent（DB + WS）。
func awaitBindingEventGrade(eventType string) domain.EventGrade {
	switch eventType {
	case "await_poll_miss":
		return domain.GradeTransient
	default:
		return domain.GradePersistent
	}
}

func valueOrNilString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
