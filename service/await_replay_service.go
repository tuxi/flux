package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"flux/domain"
	"flux/engine"
	"flux/eventbus"
	"flux/repository"
	"flux/tool"
	"gorm.io/gorm"
)

type AwaitReplayService interface {
	ReplayProviderPayload(ctx context.Context, provider string, payload map[string]any) (*AwaitReplayResult, error)
	ReplayProviderByPolling(ctx context.Context, provider string, providerTaskID string, apiTaskID string) (*AwaitReplayResult, error)
}

type AwaitReplayResult struct {
	Matched   bool
	Status    string
	BindingID int64
	TaskID    int64
	NodeName  string
	Provider  string
	Source    string
}

type awaitReplayService struct {
	engine           *engine.Engine
	awaitBindingRepo repository.AwaitBindingRepository
	tools            *tool.Registry
	eventBus         *eventbus.EventBus
}

func NewAwaitReplayService(
	engine *engine.Engine,
	awaitBindingRepo repository.AwaitBindingRepository,
	tools *tool.Registry,
	eventBus *eventbus.EventBus,
) AwaitReplayService {
	return &awaitReplayService{
		engine:           engine,
		awaitBindingRepo: awaitBindingRepo,
		tools:            tools,
		eventBus:         eventBus,
	}
}

func (s *awaitReplayService) ReplayProviderPayload(ctx context.Context, provider string, payload map[string]any) (*AwaitReplayResult, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, errors.New("provider required")
	}
	if payload == nil {
		payload = map[string]any{}
	}

	binding, err := findAwaitBindingForWebhookPayload(ctx, s.awaitBindingRepo, provider, payload)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &AwaitReplayResult{
				Matched:  false,
				Status:   "noop",
				Provider: provider,
				Source:   "replay:" + provider,
			}, nil
		}
		return nil, err
	}
	if binding == nil {
		result := &AwaitReplayResult{
			Matched:  false,
			Status:   "noop",
			Provider: provider,
			Source:   "replay:" + provider,
		}
		return result, nil
	}

	normalizedPayload, eventErr, terminal, err := normalizeProviderWebhookPayload(provider, binding, payload)
	if err != nil {
		return nil, err
	}
	s.publishReplayEvent(binding, "await_replay_requested", "await replay requested", "info", map[string]any{
		"mode":              "payload_replay",
		"provider":          provider,
		"provider_task_id":  valueOrEmpty(binding.ProviderTaskID),
		"api_task_id":       valueOrEmpty(binding.APITaskID),
		"payload_keys":      sortedKeys(payload),
		"normalized_source": "replay:" + provider,
	})
	if !terminal {
		s.publishReplayEvent(binding, "await_replay_ignored_non_terminal", "await replay ignored non-terminal payload", "info", map[string]any{
			"mode":             "payload_replay",
			"provider":         provider,
			"provider_task_id": valueOrEmpty(binding.ProviderTaskID),
			"api_task_id":      valueOrEmpty(binding.APITaskID),
		})
		return &AwaitReplayResult{
			Matched:   true,
			Status:    "ignored_non_terminal",
			BindingID: binding.ID,
			TaskID:    binding.TaskID,
			NodeName:  binding.NodeName,
			Provider:  provider,
			Source:    "replay:" + provider,
		}, nil
	}

	result := s.engine.CompleteAwaitNode(
		binding.ID,
		normalizedPayload,
		eventErr,
		"replay:"+provider,
	)
	if result.Status == engine.RunFailed {
		return nil, result.Err
	}
	s.publishReplayEvent(binding, "await_replay_completed", "await replay completed", "info", map[string]any{
		"mode":             "payload_replay",
		"provider":         provider,
		"provider_task_id": valueOrEmpty(binding.ProviderTaskID),
		"api_task_id":      valueOrEmpty(binding.APITaskID),
		"result_status":    string(result.Status),
	})

	return &AwaitReplayResult{
		Matched:   true,
		Status:    string(result.Status),
		BindingID: binding.ID,
		TaskID:    binding.TaskID,
		NodeName:  binding.NodeName,
		Provider:  provider,
		Source:    "replay:" + provider,
	}, nil
}

func (s *awaitReplayService) ReplayProviderByPolling(ctx context.Context, provider string, providerTaskID string, apiTaskID string) (*AwaitReplayResult, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, errors.New("provider required")
	}

	binding, err := s.findBindingForReplayPoll(ctx, provider, providerTaskID, apiTaskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &AwaitReplayResult{
				Matched:  false,
				Status:   "noop",
				Provider: provider,
				Source:   "replay:" + provider,
			}, nil
		}
		return nil, err
	}
	if binding == nil {
		return &AwaitReplayResult{
			Matched:  false,
			Status:   "noop",
			Provider: provider,
			Source:   "replay:" + provider,
		}, nil
	}
	if s.tools == nil {
		return nil, fmt.Errorf("await replay tools registry is nil")
	}
	if binding.FallbackPollTool == nil || strings.TrimSpace(*binding.FallbackPollTool) == "" {
		return nil, fmt.Errorf("await replay fallback poll tool is not configured")
	}

	s.publishReplayEvent(binding, "await_replay_requested", "await replay requested", "info", map[string]any{
		"mode":             "poll_and_replay",
		"provider":         provider,
		"provider_task_id": valueOrEmpty(binding.ProviderTaskID),
		"api_task_id":      valueOrEmpty(binding.APITaskID),
		"poll_tool":        *binding.FallbackPollTool,
	})

	pollResult, executedToolName, err := s.executeReplayPollTool(ctx, binding)
	if err != nil {
		return nil, err
	}

	payload, eventErr, terminal, err := synthesizeReplayPayload(provider, binding, pollResult)
	if err != nil {
		return nil, err
	}
	if !terminal {
		s.publishReplayEvent(binding, "await_replay_ignored_non_terminal", "await replay ignored non-terminal poll result", "info", map[string]any{
			"mode":             "poll_and_replay",
			"provider":         provider,
			"provider_task_id": valueOrEmpty(binding.ProviderTaskID),
			"api_task_id":      valueOrEmpty(binding.APITaskID),
			"poll_tool":        executedToolName,
		})
		return &AwaitReplayResult{
			Matched:   true,
			Status:    "ignored_non_terminal",
			BindingID: binding.ID,
			TaskID:    binding.TaskID,
			NodeName:  binding.NodeName,
			Provider:  provider,
			Source:    "replay:" + provider,
		}, nil
	}

	normalizedPayload, normalizedErr, _, err := normalizeProviderWebhookPayload(provider, binding, payload)
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
		"replay:"+provider,
	)
	if result.Status == engine.RunFailed {
		return nil, result.Err
	}

	s.publishReplayEvent(binding, "await_replay_completed", "await replay completed", "info", map[string]any{
		"mode":             "poll_and_replay",
		"provider":         provider,
		"provider_task_id": valueOrEmpty(binding.ProviderTaskID),
		"api_task_id":      valueOrEmpty(binding.APITaskID),
		"poll_tool":        executedToolName,
		"result_status":    string(result.Status),
	})

	return &AwaitReplayResult{
		Matched:   true,
		Status:    string(result.Status),
		BindingID: binding.ID,
		TaskID:    binding.TaskID,
		NodeName:  binding.NodeName,
		Provider:  provider,
		Source:    "replay:" + provider,
	}, nil
}

var _ AwaitReplayService = (*awaitReplayService)(nil)

func (s *awaitReplayService) findBindingForReplayPoll(ctx context.Context, provider, providerTaskID, apiTaskID string) (*domain.AwaitBinding, error) {
	if strings.TrimSpace(providerTaskID) != "" {
		binding, err := s.awaitBindingRepo.FindWaitingByProviderTaskID(ctx, provider, strings.TrimSpace(providerTaskID))
		if err == nil {
			return binding, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if strings.TrimSpace(apiTaskID) != "" {
		return s.awaitBindingRepo.FindWaitingByAPITaskID(ctx, provider, strings.TrimSpace(apiTaskID))
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *awaitReplayService) executeReplayPollTool(ctx context.Context, binding *domain.AwaitBinding) (*tool.Result, string, error) {
	toolName := strings.TrimSpace(valueOrEmpty(binding.FallbackPollTool))
	resolvedToolName, toolImpl, ok := tool.ResolvePreferredPollTool(s.tools, toolName)
	if !ok {
		return nil, "", fmt.Errorf("await replay fallback poll tool not found: %s", toolName)
	}

	input := buildReplayPollInput(binding)
	input["max_retry"] = 1
	input["poll_interval_ms"] = 0

	execCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	result, err := toolImpl.Execute(execCtx, input, replayNoopToolEmitter{})
	return result, resolvedToolName, err
}

func buildReplayPollInput(binding *domain.AwaitBinding) map[string]any {
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
	return input
}

type replayNoopToolEmitter struct{}

func (replayNoopToolEmitter) EmitToolEvent(event tool.ToolEvent) {}

func synthesizeReplayPayload(provider string, binding *domain.AwaitBinding, pollResult *tool.Result) (map[string]any, string, bool, error) {
	if pollResult == nil {
		return nil, "", false, fmt.Errorf("await replay poll result is nil")
	}
	if !pollResult.Success {
		errMsg := strings.TrimSpace(pollResult.Error)
		if errMsg == "" {
			errMsg = "await replay poll failed"
		}
		return map[string]any{
			"task_id":       firstNonEmptyString(valueOrEmpty(binding.ProviderTaskID), valueOrEmpty(binding.APITaskID)),
			"error_message": errMsg,
		}, errMsg, true, nil
	}
	if pollResult.Data == nil {
		return nil, "", false, fmt.Errorf("await replay poll result data is nil")
	}

	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "aliyun":
		imageURL := firstNonEmptyString(pollResult.Data["image_url"])
		if imageURL == "" {
			return nil, "", false, fmt.Errorf("await replay poll result missing image_url")
		}
		return map[string]any{
			"output": map[string]any{
				"task_id":     firstNonEmptyString(pollResult.Data["provider_task_id"], valueOrEmpty(binding.ProviderTaskID), valueOrEmpty(binding.APITaskID)),
				"task_status": "SUCCEEDED",
				"results": []any{
					map[string]any{"url": imageURL},
				},
			},
			"usage": map[string]any{
				"size": formatImageSizeForReplay(pollResult.Data["width"], pollResult.Data["height"]),
			},
			"model": firstNonEmptyString(pollResult.Data["model"]),
		}, "", true, nil
	case "kling":
		videoURL := firstNonEmptyString(pollResult.Data["video_url"])
		if videoURL == "" {
			return nil, "", false, fmt.Errorf("await replay poll result missing video_url")
		}
		return map[string]any{
			"task_id": firstNonEmptyString(
				pollResult.Data["api_task_id"],
				valueOrEmpty(binding.ProviderTaskID),
				valueOrEmpty(binding.APITaskID),
			),
			"data": map[string]any{
				"task_status": "succeed",
				"task_result": map[string]any{
					"videos": []any{
						map[string]any{"url": videoURL},
					},
				},
			},
		}, "", true, nil
	case "volcengine", "volc", "doubao":
		if imageURL := firstNonEmptyString(pollResult.Data["image_url"]); imageURL != "" {
			return map[string]any{
				"task_id": firstNonEmptyString(
					pollResult.Data["provider_task_id"],
					valueOrEmpty(binding.ProviderTaskID),
					valueOrEmpty(binding.APITaskID),
				),
				"data": map[string]any{
					"status":       "success",
					"image_url":    imageURL,
					"width":        pollResult.Data["width"],
					"height":       pollResult.Data["height"],
					"api_provider": firstNonEmptyString(pollResult.Data["api_provider"], valueOrEmpty(binding.Provider), provider),
				},
			}, "", true, nil
		}
		videoURL := firstNonEmptyString(pollResult.Data["video_url"])
		if videoURL == "" {
			return nil, "", false, fmt.Errorf("await replay poll result missing video_url")
		}
		apiProvider := firstNonEmptyString(
			pollResult.Data["api_provider"],
			valueOrEmpty(binding.Provider),
			provider,
		)
		return map[string]any{
			"task_id": firstNonEmptyString(
				pollResult.Data["api_task_id"],
				valueOrEmpty(binding.ProviderTaskID),
				valueOrEmpty(binding.APITaskID),
			),
			"data": map[string]any{
				"status":       "success",
				"video_url":    videoURL,
				"cover_url":    firstNonEmptyString(pollResult.Data["cover_url"]),
				"api_provider": apiProvider,
			},
		}, "", true, nil
	default:
		return deepCloneMap(pollResult.Data), "", true, nil
	}
}

func (s *awaitReplayService) publishReplayEvent(binding *domain.AwaitBinding, eventType, message, level string, meta map[string]any) {
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

func sortedKeys(m map[string]any) []string {
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

func valueOrNilString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func deepCloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = deepCloneAny(value)
	}
	return out
}

func deepCloneAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return deepCloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = deepCloneAny(v[i])
		}
		return out
	default:
		return value
	}
}

func findAwaitBindingForWebhookPayload(
	ctx context.Context,
	awaitBindingRepo repository.AwaitBindingRepository,
	provider string,
	payload map[string]any,
) (*domain.AwaitBinding, error) {
	providerTaskID := firstNonEmptyString(
		payload["provider_task_id"],
		payload["task_id"],
		getNestedValue(payload, "output", "task_id"),
		getNestedValue(payload, "data", "task_id"),
		getNestedValue(payload, "result", "task_id"),
	)
	if providerTaskID != "" {
		binding, err := awaitBindingRepo.FindWaitingByProviderTaskID(ctx, provider, providerTaskID)
		if err == nil {
			return binding, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	apiTaskID := firstNonEmptyString(
		payload["api_task_id"],
		payload["id"],
		getNestedValue(payload, "output", "id"),
		getNestedValue(payload, "data", "id"),
		getNestedValue(payload, "result", "id"),
	)
	if apiTaskID != "" {
		return awaitBindingRepo.FindWaitingByAPITaskID(ctx, provider, apiTaskID)
	}

	return nil, gorm.ErrRecordNotFound
}

func normalizeProviderWebhookPayload(
	provider string,
	binding *domain.AwaitBinding,
	payload map[string]any,
) (map[string]any, string, bool, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "aliyun":
		return normalizeAliyunWebhookPayload(binding, payload)
	case "kling":
		return normalizeKlingWebhookPayload(binding, payload)
	case "volcengine", "volc", "doubao":
		return normalizeVolcengineWebhookPayload(binding, payload)
	default:
		return payload, extractAwaitEventError(payload), true, nil
	}
}

func normalizeKlingWebhookPayload(
	binding *domain.AwaitBinding,
	payload map[string]any,
) (map[string]any, string, bool, error) {
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(
		payload["task_status"],
		getNestedValue(payload, "data", "task_status"),
		payload["status"],
		getNestedValue(payload, "data", "status"),
	)))

	switch status {
	case "succeed", "succeeded", "success":
		videoURL := firstNonEmptyString(
			getNestedValue(payload, "data", "task_result", "videos", 0, "url"),
			getNestedValue(payload, "task_result", "videos", 0, "url"),
			getNestedValue(payload, "result", "videos", 0, "url"),
			payload["video_url"],
		)
		if videoURL == "" {
			return nil, "", false, fmt.Errorf("kling webhook missing video url")
		}

		apiTaskID := firstNonEmptyString(
			payload["api_task_id"],
			payload["task_id"],
			getNestedValue(payload, "data", "task_id"),
			getNestedValue(payload, "data", "id"),
			valueOrEmpty(binding.ProviderTaskID),
			valueOrEmpty(binding.APITaskID),
		)
		if apiTaskID == "" {
			return nil, "", false, fmt.Errorf("kling webhook missing task id")
		}

		return map[string]any{
			"video_url":    videoURL,
			"cover_url":    "",
			"api_task_id":  apiTaskID,
			"api_provider": "kling",
		}, "", true, nil
	case "failed", "fail", "error":
		apiTaskID := firstNonEmptyString(
			payload["api_task_id"],
			payload["task_id"],
			getNestedValue(payload, "data", "task_id"),
			getNestedValue(payload, "data", "id"),
			valueOrEmpty(binding.ProviderTaskID),
			valueOrEmpty(binding.APITaskID),
		)
		errMsg := firstNonEmptyString(
			payload["error_message"],
			payload["error"],
			getNestedValue(payload, "data", "task_status_msg"),
			getNestedValue(payload, "data", "error_message"),
			getNestedValue(payload, "data", "error"),
		)
		if errMsg == "" {
			errMsg = "kling task failed"
		}
		return map[string]any{
			"api_task_id":  apiTaskID,
			"api_provider": "kling",
		}, errMsg, true, nil
	default:
		return nil, "", false, nil
	}
}

func normalizeVolcengineWebhookPayload(
	binding *domain.AwaitBinding,
	payload map[string]any,
) (map[string]any, string, bool, error) {
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(
		payload["status"],
		payload["task_status"],
		getNestedValue(payload, "data", "status"),
		getNestedValue(payload, "data", "task_status"),
	)))

	switch status {
	case "success", "succeed", "succeeded", "completed", "done":
		imageURL := firstNonEmptyString(
			payload["image_url"],
			getNestedValue(payload, "data", "image_url"),
			getNestedValue(payload, "result", "image_url"),
			getNestedValue(payload, "data", "image_urls", 0),
			getNestedValue(payload, "result", "image_urls", 0),
		)
		if imageURL != "" {
			providerTaskID := firstNonEmptyString(
				payload["provider_task_id"],
				payload["task_id"],
				getNestedValue(payload, "data", "task_id"),
				getNestedValue(payload, "data", "id"),
				valueOrEmpty(binding.ProviderTaskID),
				valueOrEmpty(binding.APITaskID),
			)
			if providerTaskID == "" {
				return nil, "", false, fmt.Errorf("volcengine webhook missing task id")
			}
			return map[string]any{
				"image_url":        imageURL,
				"width":            numericOrZero(payload["width"], getNestedValue(payload, "data", "width"), getNestedValue(payload, "result", "width")),
				"height":           numericOrZero(payload["height"], getNestedValue(payload, "data", "height"), getNestedValue(payload, "result", "height")),
				"provider_task_id": providerTaskID,
				"api_provider":     normalizedVolcengineProvider(binding, payload),
			}, "", true, nil
		}

		videoURL := firstNonEmptyString(
			payload["video_url"],
			getNestedValue(payload, "content", "video_url"),
			getNestedValue(payload, "data", "video_url"),
			getNestedValue(payload, "result", "video_url"),
		)
		if videoURL == "" {
			return nil, "", false, fmt.Errorf("volcengine webhook missing video url")
		}
		apiTaskID := firstNonEmptyString(
			payload["api_task_id"],
			payload["task_id"],
			getNestedValue(payload, "data", "task_id"),
			getNestedValue(payload, "data", "id"),
			valueOrEmpty(binding.ProviderTaskID),
			valueOrEmpty(binding.APITaskID),
		)
		if apiTaskID == "" {
			return nil, "", false, fmt.Errorf("volcengine webhook missing task id")
		}
		apiProvider := normalizedVolcengineProvider(binding, payload)
		return map[string]any{
			"video_url":    videoURL,
			"cover_url":    firstNonEmptyString(payload["cover_url"], getNestedValue(payload, "content", "last_frame_url"), getNestedValue(payload, "data", "cover_url")),
			"api_task_id":  apiTaskID,
			"api_provider": apiProvider,
		}, "", true, nil
	case "failed", "fail", "error":
		apiTaskID := firstNonEmptyString(
			payload["api_task_id"],
			payload["task_id"],
			getNestedValue(payload, "data", "task_id"),
			getNestedValue(payload, "data", "id"),
			valueOrEmpty(binding.ProviderTaskID),
			valueOrEmpty(binding.APITaskID),
		)
		errMsg := firstNonEmptyString(
			payload["error_message"],
			payload["error"],
			getNestedValue(payload, "data", "error_message"),
			getNestedValue(payload, "data", "error_msg"),
			getNestedValue(payload, "data", "error"),
			getNestedValue(payload, "error", "message"),
			getNestedValue(payload, "error", "msg"),
			getNestedValue(payload, "error", "code"),
		)
		if errMsg == "" {
			errMsg = "volcengine task failed"
		}
		apiProvider := normalizedVolcengineProvider(binding, payload)
		return map[string]any{
			"api_task_id":  apiTaskID,
			"api_provider": apiProvider,
		}, errMsg, true, nil
	default:
		return nil, "", false, nil
	}
}

func normalizeAliyunWebhookPayload(
	binding *domain.AwaitBinding,
	payload map[string]any,
) (map[string]any, string, bool, error) {
	status := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(
		payload["task_status"],
		payload["status"],
		getNestedValue(payload, "output", "task_status"),
		getNestedValue(payload, "data", "task_status"),
	)))

	switch status {
	case "SUCCEEDED", "SUCCESS", "COMPLETED":
		imageURL := firstNonEmptyString(
			payload["image_url"],
			getNestedValue(payload, "output", "results", 0, "url"),
			getNestedValue(payload, "data", "results", 0, "url"),
			getNestedValue(payload, "result", "results", 0, "url"),
		)
		if imageURL == "" {
			return nil, "", false, fmt.Errorf("aliyun webhook missing image url")
		}
		providerTaskID := firstNonEmptyString(
			payload["provider_task_id"],
			payload["task_id"],
			payload["id"],
			getNestedValue(payload, "output", "task_id"),
			getNestedValue(payload, "data", "task_id"),
			valueOrEmpty(binding.ProviderTaskID),
			valueOrEmpty(binding.APITaskID),
		)
		if providerTaskID == "" {
			return nil, "", false, fmt.Errorf("aliyun webhook missing task id")
		}
		width, height := parseAliyunWebhookSize(payload)
		return map[string]any{
			"image_url":        imageURL,
			"width":            width,
			"height":           height,
			"provider_task_id": providerTaskID,
			"api_provider":     "aliyun",
			"model":            firstNonEmptyString(payload["model"], getNestedValue(payload, "data", "model"), getNestedValue(payload, "result", "model")),
		}, "", true, nil
	case "PENDING", "RUNNING", "":
		return nil, "", false, nil
	case "FAILED", "UNKNOWN", "ERROR":
		providerTaskID := firstNonEmptyString(
			payload["provider_task_id"],
			payload["task_id"],
			payload["id"],
			getNestedValue(payload, "output", "task_id"),
			getNestedValue(payload, "data", "task_id"),
			valueOrEmpty(binding.ProviderTaskID),
			valueOrEmpty(binding.APITaskID),
		)
		errMsg, errCode, errDetail := aliyunFailureDetail(payload, status)
		if errMsg == "" {
			errMsg = "aliyun task failed"
		}
		return map[string]any{
			"provider_task_id": providerTaskID,
			"api_provider":     "aliyun",
			"failure_status":   status,
			"failure_code":     errCode,
			"failure_detail":   errDetail,
		}, errMsg, true, nil
	default:
		return nil, "", false, nil
	}
}

func aliyunFailureDetail(payload map[string]any, status string) (string, string, string) {
	code := firstNonEmptyString(
		payload["error_code"],
		payload["code"],
		getNestedValue(payload, "output", "code"),
		getNestedValue(payload, "output", "results", 0, "code"),
		getNestedValue(payload, "data", "code"),
		getNestedValue(payload, "result", "code"),
	)
	message := firstNonEmptyString(
		payload["error_message"],
		payload["error"],
		payload["message"],
		payload["task_status_msg"],
		getNestedValue(payload, "output", "message"),
		getNestedValue(payload, "output", "task_status_msg"),
		getNestedValue(payload, "output", "results", 0, "message"),
		getNestedValue(payload, "data", "message"),
		getNestedValue(payload, "data", "task_status_msg"),
		getNestedValue(payload, "result", "message"),
	)
	detail := compactAliyunFailureDetail(status, code, message)
	if detail == "" {
		return "", code, message
	}
	return "aliyun task failed: " + detail, code, message
}

func compactAliyunFailureDetail(status, code, message string) string {
	parts := []string{}
	if strings.TrimSpace(status) != "" {
		parts = append(parts, "status="+strings.TrimSpace(status))
	}
	if strings.TrimSpace(code) != "" {
		parts = append(parts, "code="+strings.TrimSpace(code))
	}
	if strings.TrimSpace(message) != "" {
		parts = append(parts, "message="+strings.TrimSpace(message))
	}
	return strings.Join(parts, " ")
}

func parseAliyunWebhookSize(payload map[string]any) (int64, int64) {
	size := firstNonEmptyString(
		payload["size"],
		getNestedValue(payload, "usage", "size"),
		getNestedValue(payload, "data", "size"),
		getNestedValue(payload, "result", "size"),
	)
	if size == "" {
		return 0, 0
	}
	size = strings.ReplaceAll(size, "*", "x")
	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return 0, 0
	}
	return int64(intValue(parts[0])), int64(intValue(parts[1]))
}

func numericOrZero(values ...any) int64 {
	for _, value := range values {
		if n := intValue(value); n > 0 {
			return int64(n)
		}
	}
	return 0
}

func formatImageSizeForReplay(width any, height any) string {
	w := intValue(width)
	h := intValue(height)
	if w <= 0 || h <= 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", w, h)
}

func intValue(raw any) int {
	switch v := raw.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func normalizedVolcengineProvider(binding *domain.AwaitBinding, payload map[string]any) string {
	return firstNonEmptyString(
		payload["api_provider"],
		getNestedValue(payload, "data", "api_provider"),
		getNestedValue(payload, "result", "api_provider"),
		valueOrEmpty(binding.Provider),
		"volcengine",
	)
}

func extractAwaitEventError(payload map[string]any) string {
	return firstNonEmptyString(
		payload["error_message"],
		payload["error"],
		getNestedValue(payload, "data", "error_message"),
		getNestedValue(payload, "data", "error"),
		getNestedValue(payload, "result", "error_message"),
	)
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		switch v := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		case *string:
			if v != nil {
				if trimmed := strings.TrimSpace(*v); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

func getNestedValue(root any, path ...any) any {
	current := root
	for _, segment := range path {
		switch key := segment.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = m[key]
		case int:
			arr, ok := current.([]any)
			if !ok || key < 0 || key >= len(arr) {
				return nil
			}
			current = arr[key]
		default:
			return nil
		}
	}
	return current
}

func valueOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
