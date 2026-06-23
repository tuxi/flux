package handler

import (
	"net/http"

	"flux/pkg/response"
	"flux/service"

	"github.com/gin-gonic/gin"
)

type AliyunEventBridgeHandler struct {
	service service.AliyunEventBridgeService
}

func NewAliyunEventBridgeHandler(service service.AliyunEventBridgeService) *AliyunEventBridgeHandler {
	return &AliyunEventBridgeHandler{service: service}
}

func (h *AliyunEventBridgeHandler) HandleAsyncTaskFinish(c *gin.Context) {
	if h == nil || h.service == nil {
		response.Error(c, http.StatusNotFound, "aliyun eventbridge handler disabled")
		return
	}

	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.service.HandleAsyncTaskFinish(c.Request.Context(), payload)
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
		"source":     result.Source,
	})
}
