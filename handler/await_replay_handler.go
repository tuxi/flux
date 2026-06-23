package handler

import (
	"net/http"
	"strings"

	"flux/pkg/response"
	"flux/service"
	"github.com/gin-gonic/gin"
)

type AwaitReplayHandler struct {
	service service.AwaitReplayService
	enabled bool
}

type awaitReplayRequest struct {
	Mode           string         `json:"mode"`
	Payload        map[string]any `json:"payload"`
	ProviderTaskID string         `json:"provider_task_id"`
	APITaskID      string         `json:"api_task_id"`
}

func NewAwaitReplayHandler(service service.AwaitReplayService, enabled bool) *AwaitReplayHandler {
	return &AwaitReplayHandler{
		service: service,
		enabled: enabled,
	}
}

func (h *AwaitReplayHandler) HandleProviderReplay(c *gin.Context) {
	if h == nil || !h.enabled || h.service == nil {
		response.Error(c, http.StatusNotFound, "await replay disabled")
		return
	}

	provider := strings.TrimSpace(c.Param("provider"))
	if provider == "" {
		response.Error(c, http.StatusBadRequest, "provider required")
		return
	}

	var req awaitReplayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if strings.TrimSpace(req.Mode) == "" {
		req.Mode = "payload_replay"
	}
	if req.Mode != "payload_replay" && req.Mode != "poll_and_replay" {
		response.Error(c, http.StatusBadRequest, "unsupported replay mode")
		return
	}

	var (
		result *service.AwaitReplayResult
		err    error
	)
	switch req.Mode {
	case "payload_replay":
		result, err = h.service.ReplayProviderPayload(c.Request.Context(), provider, req.Payload)
	case "poll_and_replay":
		result, err = h.service.ReplayProviderByPolling(c.Request.Context(), provider, req.ProviderTaskID, req.APITaskID)
	}
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"matched":    result.Matched,
		"status":     result.Status,
		"binding_id": result.BindingID,
		"task_id":    result.TaskID,
		"node_name":  result.NodeName,
		"provider":   result.Provider,
		"source":     result.Source,
		"mode":       req.Mode,
	})
}
