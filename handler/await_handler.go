package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"flux/domain"
	"flux/engine"
	"flux/pkg/response"
	"flux/repository"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AwaitHandler struct {
	engine           *engine.Engine
	awaitBindingRepo repository.AwaitBindingRepository
}

type awaitSignalRequest struct {
	SignalName    string         `json:"signal_name"`
	CallbackToken string         `json:"callback_token"`
	Payload       map[string]any `json:"payload"`
	Error         string         `json:"error"`
}

func NewAwaitHandler(
	engine *engine.Engine,
	awaitBindingRepo repository.AwaitBindingRepository,
) *AwaitHandler {
	return &AwaitHandler{
		engine:           engine,
		awaitBindingRepo: awaitBindingRepo,
	}
}

func (h *AwaitHandler) HandleProviderWebhook(c *gin.Context) {
	provider := strings.TrimSpace(c.Param("provider"))
	if provider == "" {
		response.Error(c, http.StatusBadRequest, "provider required")
		return
	}

	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	binding, err := h.findBindingForWebhook(c, provider, payload)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Success(c, gin.H{"matched": false, "status": "noop"})
			return
		}
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if binding == nil {
		response.Success(c, gin.H{"matched": false, "status": "noop"})
		return
	}

	normalizedPayload, eventErr, terminal, err := normalizeProviderWebhookPayload(provider, binding, payload)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if !terminal {
		response.Success(c, gin.H{
			"matched":    true,
			"binding_id": binding.ID,
			"task_id":    binding.TaskID,
			"node_name":  binding.NodeName,
			"status":     "ignored_non_terminal",
		})
		return
	}

	result := h.engine.CompleteAwaitNode(
		binding.ID,
		normalizedPayload,
		eventErr,
		"webhook:"+provider,
	)
	if result.Status == engine.RunFailed {
		response.Error(c, http.StatusInternalServerError, result.Err.Error())
		return
	}

	response.Success(c, gin.H{
		"matched":    true,
		"binding_id": binding.ID,
		"task_id":    binding.TaskID,
		"node_name":  binding.NodeName,
		"status":     result.Status,
	})
}

func (h *AwaitHandler) HandleSignal(c *gin.Context) {
	var req awaitSignalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	req.SignalName = strings.TrimSpace(req.SignalName)
	req.CallbackToken = strings.TrimSpace(req.CallbackToken)
	if req.SignalName == "" {
		response.Error(c, http.StatusBadRequest, "signal_name required")
		return
	}
	if req.CallbackToken == "" {
		response.Error(c, http.StatusBadRequest, "callback_token required")
		return
	}
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}

	binding, err := h.awaitBindingRepo.FindWaitingBySignal(c.Request.Context(), req.SignalName, req.CallbackToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Success(c, gin.H{"matched": false, "status": "noop"})
			return
		}
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if binding == nil {
		response.Success(c, gin.H{"matched": false, "status": "noop"})
		return
	}

	result := h.engine.CompleteAwaitNode(
		binding.ID,
		req.Payload,
		strings.TrimSpace(req.Error),
		"signal:"+req.SignalName,
	)
	if result.Status == engine.RunFailed {
		response.Error(c, http.StatusInternalServerError, result.Err.Error())
		return
	}

	response.Success(c, gin.H{
		"matched":    true,
		"binding_id": binding.ID,
		"task_id":    binding.TaskID,
		"node_name":  binding.NodeName,
		"status":     result.Status,
		"signal":     req.SignalName,
	})
}

func (h *AwaitHandler) findBindingForWebhook(c *gin.Context, provider string, payload map[string]any) (*domain.AwaitBinding, error) {
	providerTaskID := firstNonEmptyString(
		payload["provider_task_id"],
		payload["task_id"],
		getNestedValue(payload, "output", "task_id"),
		getNestedValue(payload, "data", "task_id"),
		getNestedValue(payload, "result", "task_id"),
	)
	if providerTaskID != "" {
		binding, err := h.awaitBindingRepo.FindWaitingByProviderTaskID(c.Request.Context(), provider, providerTaskID)
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
		return h.awaitBindingRepo.FindWaitingByAPITaskID(c.Request.Context(), provider, apiTaskID)
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
			payload["task_status_msg"],
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

	case "", "submitted", "pending", "processing", "running":
		return nil, "", false, nil

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
		getNestedValue(payload, "data", "status"),
		getNestedValue(payload, "result", "status"),
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
				payload["id"],
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
			payload["id"],
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
			"cover_url":    firstNonEmptyString(payload["cover_url"], getNestedValue(payload, "content", "last_frame_url"), getNestedValue(payload, "data", "cover_url"), getNestedValue(payload, "result", "cover_url")),
			"api_task_id":  apiTaskID,
			"api_provider": apiProvider,
		}, "", true, nil

	case "failed", "fail", "error":
		apiTaskID := firstNonEmptyString(
			payload["api_task_id"],
			payload["task_id"],
			payload["id"],
			getNestedValue(payload, "data", "task_id"),
			getNestedValue(payload, "data", "id"),
			valueOrEmpty(binding.ProviderTaskID),
			valueOrEmpty(binding.APITaskID),
		)
		errMsg := firstNonEmptyString(
			payload["error_message"],
			payload["error"],
			getNestedValue(payload, "data", "error_msg"),
			getNestedValue(payload, "data", "error_message"),
			getNestedValue(payload, "result", "error_msg"),
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

	case "", "submitted", "pending", "processing", "running", "queued":
		return nil, "", false, nil

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
		if boolConfig(binding.Config, "failure_as_output") {
			return map[string]any{
				"image_url":        "",
				"provider_task_id": providerTaskID,
				"api_provider":     "aliyun",
				"model":            firstNonEmptyString(payload["model"], getNestedValue(payload, "data", "model"), getNestedValue(payload, "result", "model")),
				"failure_reason":   errMsg,
				"failure_status":   status,
				"failure_code":     errCode,
				"failure_detail":   errDetail,
			}, "", true, nil
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

func boolConfig(config map[string]any, key string) bool {
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
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(
		payload["status"],
		getNestedValue(payload, "data", "status"),
		getNestedValue(payload, "result", "status"),
	)))
	if status == "failed" || status == "error" {
		return firstNonEmptyString(
			payload["error_message"],
			payload["error"],
			getNestedValue(payload, "data", "error_message"),
			getNestedValue(payload, "data", "error"),
			getNestedValue(payload, "result", "error_message"),
		)
	}
	return ""
}

func getNestedValue(root any, keys ...any) any {
	var current any = root
	for _, key := range keys {
		switch typedKey := key.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = m[typedKey]
		case int:
			items, ok := current.([]any)
			if !ok || typedKey < 0 || typedKey >= len(items) {
				return nil
			}
			current = items[typedKey]
		default:
			return nil
		}
	}
	return current
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		switch v := value.(type) {
		case nil:
			continue
		case string:
			s := strings.TrimSpace(v)
			if s != "" {
				return s
			}
		default:
			if raw := strings.TrimSpace(fmt.Sprintf("%v", v)); raw != "" && raw != "<nil>" {
				return raw
			}
		}
	}
	return ""
}
