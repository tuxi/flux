package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"flux/domain"
	"flux/engine"
	"flux/eventbus"
	"flux/repository"
	"flux/tool"
	"gorm.io/gorm"
)

type AliyunEventBridgeService interface {
	HandleAsyncTaskFinish(ctx context.Context, payload map[string]any) (*AliyunEventBridgeResult, error)
}

type AliyunEventBridgeResult struct {
	Matched   bool
	Status    string
	BindingID int64
	TaskID    int64
	NodeName  string
	Source    string
}

type aliyunEventBridgeService struct {
	engine           *engine.Engine
	awaitBindingRepo repository.AwaitBindingRepository
	tools            *tool.Registry
	eventBus         *eventbus.EventBus
}

func NewAliyunEventBridgeService(
	engine *engine.Engine,
	awaitBindingRepo repository.AwaitBindingRepository,
	tools *tool.Registry,
	eventBus *eventbus.EventBus,
) AliyunEventBridgeService {
	return &aliyunEventBridgeService{
		engine:           engine,
		awaitBindingRepo: awaitBindingRepo,
		tools:            tools,
		eventBus:         eventBus,
	}
}

func (s *aliyunEventBridgeService) HandleAsyncTaskFinish(ctx context.Context, payload map[string]any) (*AliyunEventBridgeResult, error) {
	if payload == nil {
		payload = map[string]any{}
	}

	taskID := strings.TrimSpace(firstNonEmptyString(
		getNestedValue(payload, "data", "task_id"),
		payload["task_id"],
	))
	if taskID == "" {
		return nil, errors.New("aliyun eventbridge payload missing data.task_id")
	}

	binding, err := s.awaitBindingRepo.FindWaitingByProviderTaskID(ctx, "aliyun", taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &AliyunEventBridgeResult{Matched: false, Status: "noop", Source: "eventbridge:aliyun"}, nil
		}
		return nil, err
	}
	if binding == nil {
		return &AliyunEventBridgeResult{Matched: false, Status: "noop", Source: "eventbridge:aliyun"}, nil
	}

	status := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(
		getNestedValue(payload, "data", "task_status"),
		payload["task_status"],
	)))
	if status == "" {
		return nil, errors.New("aliyun eventbridge payload missing data.task_status")
	}

	s.publishEventBridgeEvent(binding, "await_eventbridge_received", "aliyun eventbridge event received", "info", map[string]any{
		"provider_task_id": taskID,
		"event_status":     status,
		"payload_keys":     sortedAliyunEventBridgeKeys(payload),
	})

	switch status {
	case "PENDING", "RUNNING":
		s.publishEventBridgeEvent(binding, "await_eventbridge_ignored_non_terminal", "aliyun eventbridge ignored non-terminal event", "info", map[string]any{
			"provider_task_id": taskID,
			"event_status":     status,
		})
		return &AliyunEventBridgeResult{
			Matched:   true,
			Status:    "ignored_non_terminal",
			BindingID: binding.ID,
			TaskID:    binding.TaskID,
			NodeName:  binding.NodeName,
			Source:    "eventbridge:aliyun",
		}, nil
	case "FAILED", "CANCELED", "CANCELLED", "UNKNOWN":
		eventErr := strings.TrimSpace(firstNonEmptyString(
			getNestedValue(payload, "data", "message"),
			getNestedValue(payload, "data", "error_message"),
			payload["message"],
			payload["error_message"],
		))
		if eventErr == "" {
			eventErr = fmt.Sprintf("aliyun task %s", strings.ToLower(status))
		}
		result := s.engine.CompleteAwaitNode(
			binding.ID,
			map[string]any{
				"provider_task_id": taskID,
				"api_task_id":      firstNonEmptyString(valueOrEmpty(binding.APITaskID), taskID),
				"api_provider":     "aliyun",
			},
			eventErr,
			"eventbridge:aliyun",
		)
		if result.Status == engine.RunFailed {
			return nil, result.Err
		}
		s.publishEventBridgeEvent(binding, "await_eventbridge_completed", "aliyun eventbridge completed await with terminal failure", "warn", map[string]any{
			"provider_task_id": taskID,
			"event_status":     status,
			"result_status":    string(result.Status),
		})
		return &AliyunEventBridgeResult{
			Matched:   true,
			Status:    string(result.Status),
			BindingID: binding.ID,
			TaskID:    binding.TaskID,
			NodeName:  binding.NodeName,
			Source:    "eventbridge:aliyun",
		}, nil
	case "SUCCEEDED":
	default:
		return nil, fmt.Errorf("unsupported aliyun eventbridge task_status: %s", status)
	}

	if s.tools == nil {
		return nil, fmt.Errorf("aliyun eventbridge tools registry is nil")
	}
	if binding.FallbackPollTool == nil || strings.TrimSpace(*binding.FallbackPollTool) == "" {
		return nil, fmt.Errorf("aliyun eventbridge fallback poll tool is not configured")
	}

	pollResult, executedToolName, err := s.executeEventBridgePollTool(ctx, binding)
	if err != nil {
		return nil, err
	}

	replayPayload, eventErr, terminal, err := synthesizeReplayPayload("aliyun", binding, pollResult)
	if err != nil {
		return nil, err
	}
	if !terminal {
		s.publishEventBridgeEvent(binding, "await_eventbridge_ignored_non_terminal", "aliyun eventbridge poll result not terminal", "info", map[string]any{
			"provider_task_id": taskID,
			"event_status":     status,
			"poll_tool":        executedToolName,
		})
		return &AliyunEventBridgeResult{
			Matched:   true,
			Status:    "ignored_non_terminal",
			BindingID: binding.ID,
			TaskID:    binding.TaskID,
			NodeName:  binding.NodeName,
			Source:    "eventbridge:aliyun",
		}, nil
	}

	normalizedPayload, normalizedErr, _, err := normalizeProviderWebhookPayload("aliyun", binding, replayPayload)
	if err != nil {
		return nil, err
	}
	if normalizedErr == "" {
		normalizedErr = eventErr
	}

	result := s.engine.CompleteAwaitNode(
		binding.ID,
		normalizedPayload,
		normalizedErr,
		"eventbridge:aliyun",
	)
	if result.Status == engine.RunFailed {
		return nil, result.Err
	}

	s.publishEventBridgeEvent(binding, "await_eventbridge_completed", "aliyun eventbridge completed await", "info", map[string]any{
		"provider_task_id": taskID,
		"event_status":     status,
		"poll_tool":        executedToolName,
		"result_status":    string(result.Status),
	})

	return &AliyunEventBridgeResult{
		Matched:   true,
		Status:    string(result.Status),
		BindingID: binding.ID,
		TaskID:    binding.TaskID,
		NodeName:  binding.NodeName,
		Source:    "eventbridge:aliyun",
	}, nil
}

func (s *aliyunEventBridgeService) executeEventBridgePollTool(ctx context.Context, binding *domain.AwaitBinding) (*tool.Result, string, error) {
	toolName := strings.TrimSpace(valueOrEmpty(binding.FallbackPollTool))
	resolvedToolName, toolImpl, ok := tool.ResolvePreferredPollTool(s.tools, toolName)
	if !ok {
		return nil, "", fmt.Errorf("aliyun eventbridge fallback poll tool not found: %s", toolName)
	}

	input := buildReplayPollInput(binding)
	input["max_retry"] = 1
	input["poll_interval_ms"] = 0

	execCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	result, err := toolImpl.Execute(execCtx, input, replayNoopToolEmitter{})
	return result, resolvedToolName, err
}

func (s *aliyunEventBridgeService) publishEventBridgeEvent(binding *domain.AwaitBinding, eventType, message, level string, meta map[string]any) {
	if s == nil || s.eventBus == nil || binding == nil {
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

	s.eventBus.Publish(binding.RootTaskID, &domain.TaskEvent{
		TaskID:     binding.TaskID,
		RootTaskID: binding.RootTaskID,
		Step:       binding.NodeName,
		Message:    message,
		Meta:       meta,
		CreatedAt:  time.Now(),
		Type:       eventType,
		Level:      level,
	})
}

func sortedAliyunEventBridgeKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
