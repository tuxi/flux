package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/dto"
	engine2 "flux/engine"
	"flux/internal/consts"
	repository2 "flux/repository"
	internalservice "flux/service"
	"flux/workflow"
	"github.com/tuxi/dream-ai/ai-engine/service"
	"github.com/tuxi/dream-ai/pkg/response"
	"github.com/tuxi/dream-ai/utils/uuid"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// WorkflowHandler 负责工作流相关操作，包括:
// 1. 创建/编辑工作流
// 2. 添加/删除节点
// 3. 查询工作流状态
// 4. WebSocket 实时订阅节点状态
type WorkflowHandler struct {
	workflowRepo        repository2.WorkflowRepository // 自定义接口，存 workflow 元信息
	workflowVersionRepo repository2.WorkflowVersionRepository
	taskRepo            repository2.TaskRepository
	taskCostTraceRepo   repository2.TaskCostTraceRepository
	nodeRuntimeRepo     repository2.NodeRuntimeRepository
	eventRepo           repository2.EventRepository
	mu                  sync.Mutex
	iSrv                uuid.SnowNode
	builder             *workflow.Builder
	taskForkService     internalservice.RunRedoService
	eng                 *engine2.Engine

	taskRetryService  internalservice.TaskRetryService
	creativeDetailSvc service.CreativeDetailService
	videoTimelineSvc  service.VideoTimelineService
	nodeReplaySvc     internalservice.NodeReplayService
	billingTaskSvc    internalservice.BillingTaskService
	assetSigner       internalservice.StorageAssetService
}

// NewWorkflowHandler 初始化
func NewWorkflowHandler(
	workflowRepo repository2.WorkflowRepository,
	workflowVersionRepo repository2.WorkflowVersionRepository,
	taskRepo repository2.TaskRepository,
	taskCostTraceRepo repository2.TaskCostTraceRepository,
	eventRepo repository2.EventRepository,
	nodeRuntimeRepo repository2.NodeRuntimeRepository,
	builder *workflow.Builder,
	taskForkService internalservice.RunRedoService,
	taskRetryService internalservice.TaskRetryService,
	billingTaskSvc internalservice.BillingTaskService,
	eng *engine2.Engine,
) *WorkflowHandler {

	return &WorkflowHandler{
		workflowRepo:        workflowRepo,
		taskRepo:            taskRepo,
		taskCostTraceRepo:   taskCostTraceRepo,
		eventRepo:           eventRepo,
		workflowVersionRepo: workflowVersionRepo,
		nodeRuntimeRepo:     nodeRuntimeRepo,
		builder:             builder,
		taskForkService:     taskForkService,
		iSrv:                *uuid.NewNode(3),
		taskRetryService:    taskRetryService,
		billingTaskSvc:      billingTaskSvc,
		eng:                 eng,
	}
}

func (h *WorkflowHandler) WithAssetSigner(assetSigner internalservice.StorageAssetService) *WorkflowHandler {
	h.assetSigner = assetSigner
	return h
}

func (h *WorkflowHandler) WithCreativeDetailService(creativeDetailSvc service.CreativeDetailService) *WorkflowHandler {
	h.creativeDetailSvc = creativeDetailSvc
	return h
}

func (h *WorkflowHandler) WithVideoTimelineService(videoTimelineSvc service.VideoTimelineService) *WorkflowHandler {
	h.videoTimelineSvc = videoTimelineSvc
	return h
}

func (h *WorkflowHandler) WithNodeReplayService(nodeReplaySvc internalservice.NodeReplayService) *WorkflowHandler {
	h.nodeReplaySvc = nodeReplaySvc
	return h
}

// ------------------- CRUD --------------------

// CreateWorkflow 创建工作流
func (h *WorkflowHandler) CreateWorkflow(c *gin.Context) {
	var req dto.CreateWorkflowReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID := c.GetInt64(consts.UserID)
	if userID == 0 {
		response.Error(c, http.StatusBadRequest, "user_id required")
		return
	}
	wf := domain.Workflow{
		Name:        req.Name,
		UserID:      &userID,
		Description: req.Description,
	}
	if err := h.workflowRepo.Create(c.Request.Context(), &wf); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	c.JSON(http.StatusOK, wf)
}

// GetWorkflow 查询工作流详情
func (h *WorkflowHandler) GetWorkflow(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid id")
		return
	}

	ctx := c.Request.Context()

	// 1 查询 workflow definition
	wf, err := h.workflowRepo.GetByID(ctx, id)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "workflow not found")
		return
	}

	// 2 查询最新版本
	version, err := h.workflowVersionRepo.GetLatestByWorkflowID(ctx, id)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "err.Error()")
		return
	}

	var definition map[string]any
	_ = json.Unmarshal(version.DefinitionJSON, &definition)

	response.Success(c, gin.H{
		"workflow":  wf,
		"version":   version.Version,
		"structure": definition,
	})
}

// ListWorkflows 返回当前用户可用的工作流列表
func (h *WorkflowHandler) ListWorkflows(c *gin.Context) {
	ctx := c.Request.Context()

	workflows, err := h.workflowRepo.List(ctx)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, workflows)
}

// GetWorkflowDefinition 获取工作流具体的定义和节点
func (h *WorkflowHandler) GetWorkflowDefinition(c *gin.Context) {
	idStr := c.Param("id")
	wid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid workflow id")
		return
	}

	version, err := h.workflowVersionRepo.GetLatestByWorkflowID(c.Request.Context(), wid)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "workflow version not found")
		return
	}

	var definition map[string]any
	_ = json.Unmarshal(version.DefinitionJSON, &definition)

	response.Success(c, gin.H{
		"workflow_id": wid,
		"version":     version.Version,
		"structure":   definition,
	})
}

func (h *WorkflowHandler) GetTask(c *gin.Context) {

	idStr := c.Param("id")

	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}

	ctx := c.Request.Context()

	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}
	if task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}

	userID := c.GetInt64(consts.UserID)
	if userID != 0 && task.UserID != userID {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	nodes, _ := h.nodeRuntimeRepo.FindByTaskID(ctx, taskID)

	var nodeDefs []*definition.NodeDefinition
	if h.workflowVersionRepo != nil {
		if dbVersion, err := h.workflowVersionRepo.Get(ctx, task.WorkflowVersionID); err == nil && dbVersion != nil {
			var wfDef definition.WorkflowDefinition
			if json.Unmarshal(dbVersion.DefinitionJSON, &wfDef) == nil {
				for i := range wfDef.Nodes {
					nodeDefs = append(nodeDefs, &wfDef.Nodes[i])
				}
			}
		}
	}

	events, _ := h.eventRepo.FindPersistentByTaskID(ctx, taskID, 0, 0, false)
	var costTraces []*domain.TaskCostTrace
	if h.taskCostTraceRepo != nil {
		costTraces, _ = h.taskCostTraceRepo.ListByTaskID(ctx, taskID)
	}

	response.Success(c, gin.H{
		"task":             task,
		"nodes":            nodes,
		"node_definitions": nodeDefs,
		"events":           events,
		"cost_summary": gin.H{
			"estimated_cost_total": task.EstimatedCostTotal,
			"actual_cost_total":    task.ActualCostTotal,
			"cost_status":          task.CostStatus,
		},
		"cost_traces": costTraces,
	})
}

func (h *WorkflowHandler) GetTaskCreativeDetail(c *gin.Context) {
	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}
	if h.creativeDetailSvc == nil {
		response.Error(c, http.StatusServiceUnavailable, "creative detail service unavailable")
		return
	}

	ctx := c.Request.Context()
	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}
	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	detail, err := h.creativeDetailSvc.BuildTaskCreativeDetail(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Success(c, detail)
}

func (h *WorkflowHandler) GetTaskVideoTimeline(c *gin.Context) {
	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}
	if h.videoTimelineSvc == nil {
		response.Error(c, http.StatusServiceUnavailable, "video timeline service unavailable")
		return
	}

	ctx := c.Request.Context()
	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}
	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	timeline, err := h.videoTimelineSvc.BuildTaskVideoTimeline(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Success(c, timeline)
}

func (h *WorkflowHandler) ReplayTaskNodeInput(c *gin.Context) {
	h.replayTaskNode(c, false)
}

func (h *WorkflowHandler) ReplayTaskNode(c *gin.Context) {
	h.replayTaskNode(c, true)
}

func (h *WorkflowHandler) replayTaskNode(c *gin.Context, execute bool) {
	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}
	nodeName := strings.TrimSpace(c.Param("node"))
	if nodeName == "" {
		response.Error(c, http.StatusBadRequest, "node is required")
		return
	}
	if h.nodeReplaySvc == nil {
		response.Error(c, http.StatusServiceUnavailable, "node replay service unavailable")
		return
	}

	ctx := c.Request.Context()
	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}
	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	result, err := h.nodeReplaySvc.ReplayTaskNode(ctx, taskID, nodeName, execute)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Success(c, result)
}

// GetChildrenByNode 返回父任务某个节点下的所有子任务信息，供 UI 展示子任务节点选择器。
// GET /api/v1/user/works/:task_id/nodes/:node_name/children
func (h *WorkflowHandler) GetChildrenByNode(c *gin.Context) {
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}
	nodeName := strings.TrimSpace(c.Param("node"))
	if nodeName == "" {
		response.Error(c, http.StatusBadRequest, "node_name is required")
		return
	}

	ctx := c.Request.Context()

	// 1. 加载父任务并校验归属
	parentTask, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || parentTask == nil {
		response.Error(c, http.StatusBadRequest, "parent task not found")
		return
	}
	userID := c.GetInt64(consts.UserID)
	if userID != 0 && parentTask.UserID != userID {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	// 2. 查出该节点下的所有子任务
	children, err := h.taskRepo.ListByParentNode(ctx, taskID, nodeName)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	// 3. 缓存：WorkflowVersionID -> NodeDefinitions，避免同 map 节点重复加载
	defCache := map[int64][]*definition.NodeDefinition{}

	result := make([]dto.ChildTaskInfo, 0, len(children))
	for _, child := range children {
		if child == nil {
			continue
		}

		childNodes, _ := h.nodeRuntimeRepo.FindByTaskID(ctx, child.ID)

		nodeDefs, ok := defCache[child.WorkflowVersionID]
		if !ok {
			dbVersion, err := h.workflowVersionRepo.Get(ctx, child.WorkflowVersionID)
			if err == nil && dbVersion != nil {
				var wfDef definition.WorkflowDefinition
				if json.Unmarshal(dbVersion.DefinitionJSON, &wfDef) == nil {
					wfRuntime, buildErr := h.builder.Build(&wfDef)
					if buildErr == nil {
						src := wfRuntime.Source()
						if src != nil {
							nodeDefs = make([]*definition.NodeDefinition, len(src.Nodes))
							for i := range src.Nodes {
								nodeDefs[i] = &src.Nodes[i]
							}
						}
					}
				}
			}
			defCache[child.WorkflowVersionID] = nodeDefs
		}

		result = append(result, dto.ChildTaskInfo{
			Child: dto.ChildTaskDTO{
				TaskID:       child.ID,
				Status:       string(child.Status),
				MapIndex:     child.MapIndex,
				ErrorMessage: child.ErrorMessage,
				RetryCount:   child.RetryCount,
				CreatedAt:    child.CreatedAt.Unix(),
				UpdatedAt:    child.UpdatedAt.Unix(),
			},
			Nodes:           childNodes,
			NodeDefinitions: nodeDefs,
		})
	}

	response.Success(c, dto.GetChildrenByNodeResp{
		NodeName: nodeName,
		Children: result,
	})
}

//func (h *WorkflowHandler) GetAllTask(c *gin.Context) {
//
//	var req dto.PageParams
//	if err := c.ShouldBindJSON(req); err != nil {
//		response.Error(c, http.StatusBadRequest, err.Error())
//		return
//	}
//
//	userID := c.GetInt64(consts.UserID)
//
//	ctx := c.Request.Context()
//
//	tasks, total, err := h.taskRepo.ListByUser(ctx, userID, req)
//
//	if err != nil {
//		response.Error(c, http.StatusInternalServerError, err.Error())
//		return
//	}
//
//	var result []gin.H
//
//	for _, task := range tasks {
//
//		nodes, _ := h.nodeRuntimeRepo.FindByTaskID(ctx, task.ID)
//		if nodes == nil {
//			nodes = make([]*domain.NodeRuntime, 0)
//		}
//		events, _ := h.eventRepo.FindByTaskID(ctx, task.ID)
//		if events == nil {
//			events = make([]domain.TaskEvent, 0)
//		}
//
//		nodeDefs, _ := h.nodeDefRepo.ListByWorkflow(ctx, task.WorkflowDefinitionID)
//
//		result = append(result, gin.H{
//			"task":             task,
//			"nodes":            nodes,
//			"events":           events,
//			"node_definitions": nodeDefs,
//		})
//	}
//
//	response.Success(c, gin.H{
//		"tasks": result,
//	})
//}

func (h *WorkflowHandler) GetTasksByUser(c *gin.Context) {

	var req dto.PageRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	// 参数校验
	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	userID := c.GetInt64(consts.UserID)

	tasks, total, err := h.taskRepo.ListByUser(c, userID, req)

	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"tasks": tasks,
		"total": total,
	})
}

// CreateTaskFromWorkflow 创建任务并运行工作流
func (h *WorkflowHandler) CreateTaskFromWorkflow(c *gin.Context) {
	var req dto.RunWorkflowReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	version, err := h.workflowVersionRepo.GetLatestByWorkflowName(c.Request.Context(), req.WorkflowName)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "workflow version not found")
		return
	}
	taskID := h.iSrv.GenSnowID()
	if req.Input == nil {
		req.Input = make(map[string]any)
	}

	req.Input["callback_token"] = strconv.FormatInt(taskID, 10)
	inputJSON, _ := json.Marshal(req.Input)
	task := domain.Task{
		ID:                   taskID,
		RootID:               taskID,
		SubKey:               nil,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: version.WorkflowID,
		UserID:               c.GetInt64(consts.UserID),
		Status:               domain.TaskPending,
		InputJSON:            inputJSON,
	}

	err = h.taskRepo.Create(c.Request.Context(), &task)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if h.assetSigner != nil {
		if err := h.assetSigner.RegisterTaskInputAssetRefs(c.Request.Context(), task.UserID, task.ID, req.Input); err != nil {
			response.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
	}

	err = h.taskRepo.Enqueue(c.Request.Context(), task.ID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"task_id": taskID,
		"status":  task.Status,
	})
}

// ResumeTask 用于人工恢复已经退出自动重试窗口的任务。
// 支持从指定节点开始恢复（resume_from），可配合 patches 修正上游输出。
// pending/running 不允许恢复；failed 表示自动重试已结束。
func (h *WorkflowHandler) ResumeTask(c *gin.Context) {
	var req dto.ResumeTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	resumeFrom := strings.TrimSpace(req.ResumeFrom)
	ctx := c.Request.Context()

	task, err := h.taskRepo.GetByID(ctx, req.TaskID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}
	if isRecentlyClaimed(task, time.Now()) {
		response.Error(c, http.StatusConflict, "task is being retried")
		return
	}

	if task.Status != domain.TaskFailed && task.Status != domain.TaskSuspended && task.Status != domain.TaskCanceled {
		response.Error(c, http.StatusBadRequest, "task is not retryable")
		return
	}

	// —— 子任务恢复 ——
	if len(req.ChildResumes) > 0 {
		// 1. 校验所有 child 归属 + 状态，确定统一的 parent node
		var parentNodeName string
		for i, cr := range req.ChildResumes {
			child, err := h.taskRepo.GetByID(ctx, cr.ChildTaskID)
			if err != nil || child == nil {
				response.Error(c, http.StatusBadRequest, fmt.Sprintf("child_resumes[%d]: child task %d not found", i, cr.ChildTaskID))
				return
			}
			if child.ParentID == nil || *child.ParentID != task.ID {
				response.Error(c, http.StatusBadRequest, fmt.Sprintf("child_resumes[%d]: child %d does not belong to parent %d", i, cr.ChildTaskID, task.ID))
				return
			}
			if child.RootID != task.RootID {
				response.Error(c, http.StatusBadRequest, fmt.Sprintf("child_resumes[%d]: child %d root mismatch", i, cr.ChildTaskID))
				return
			}
			if child.UserID != task.UserID {
				response.Error(c, http.StatusForbidden, fmt.Sprintf("child_resumes[%d]: child %d user mismatch", i, cr.ChildTaskID))
				return
			}
			if child.ParentNode == nil || strings.TrimSpace(*child.ParentNode) == "" {
				response.Error(c, http.StatusBadRequest, fmt.Sprintf("child_resumes[%d]: child %d has no parent node", i, cr.ChildTaskID))
				return
			}
			nodeName := strings.TrimSpace(*child.ParentNode)
			if parentNodeName == "" {
				parentNodeName = nodeName
			} else if nodeName != parentNodeName {
				response.Error(c, http.StatusBadRequest, fmt.Sprintf("child_resumes[%d]: all children must belong to the same parent node (expected %s, got %s)", i, parentNodeName, nodeName))
				return
			}
			if child.Status != domain.TaskFailed && child.Status != domain.TaskCanceled {
				response.Error(c, http.StatusBadRequest, fmt.Sprintf("child_resumes[%d]: child %d status %s is not retryable (only failed/canceled)", i, cr.ChildTaskID, child.Status))
				return
			}
		}

		// 2. parent resume_from 强制设为 parent_node
		resumeFrom = parentNodeName

		// 3. 逐个恢复子任务 + 清理父 map checkpoint
		for i, cr := range req.ChildResumes {
			// 应用 override_input 到子任务的 input_json
			if len(cr.OverrideInput) > 0 {
				if err := h.applyOverrideInput(ctx, cr.ChildTaskID, cr.OverrideInput); err != nil {
					response.Error(c, http.StatusBadRequest, fmt.Sprintf("child_resumes[%d]: override_input: %v", i, err))
					return
				}
			}

			childPatches := h.toDomainRuntimePatches(cr.Patches)
			childResumeFrom := strings.TrimSpace(cr.ResumeFrom)

			if err := h.taskRetryService.PrepareTaskRetry(ctx, cr.ChildTaskID, internalservice.RetryTriggerManual, childResumeFrom, childPatches); err != nil {
				response.Error(c, http.StatusInternalServerError, fmt.Sprintf("child_resumes[%d]: prepare retry failed: %v", i, err))
				return
			}
			if err := h.taskRepo.Enqueue(ctx, cr.ChildTaskID); err != nil {
				response.Error(c, http.StatusInternalServerError, fmt.Sprintf("child_resumes[%d]: enqueue failed: %v", i, err))
				return
			}

			// 幂等清理 map checkpoint 中对应 index 的结果
			if err := h.taskRetryService.ClearMapChildCheckpointResult(ctx, task.ID, parentNodeName, cr.ChildTaskID); err != nil {
				log.Printf("[child_resume] clear map checkpoint: parent=%d node=%s child=%d err=%v", task.ID, parentNodeName, cr.ChildTaskID, err)
				// 非致命错误：checkpoint 可能已经是空的，不影响恢复流程
			}
		}
	}

	patches := h.toDomainRuntimePatches(req.Patches)

	if h.billingTaskSvc != nil {
		if err := h.billingTaskSvc.RefreezeTask(ctx, task.ID); err != nil {
			response.Error(c, billingResumeErrorStatus(err), err.Error())
			return
		}
		if err := h.billingTaskSvc.AssertTaskResumable(ctx, task.ID); err != nil {
			response.Error(c, billingResumeErrorStatus(err), err.Error())
			return
		}
	}
	err = h.taskRetryService.PrepareTaskRetry(
		ctx,
		task.ID,
		internalservice.RetryTriggerManual,
		resumeFrom,
		patches,
	)
	if err != nil {
		if err.Error() == internalservice.TaskNoRetryFound && (task.Status == domain.TaskFailed || task.Status == domain.TaskCanceled) {
			task.Status = domain.TaskPending
			_ = h.taskRepo.Update(ctx, task)
			_ = h.taskRepo.Enqueue(ctx, task.ID)

			response.Success(c, gin.H{
				"task_id":     task.ID,
				"status":      domain.TaskPending,
				"resume_from": resumeFrom,
			})
			return
		}

		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.taskRepo.Enqueue(ctx, task.ID); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"task_id":     task.ID,
		"status":      domain.TaskPending,
		"resume_from": resumeFrom,
	})
}

func billingResumeErrorStatus(err error) int {
	if err == internalservice.ErrBillingInsufficientPoints {
		return http.StatusPaymentRequired
	}
	return http.StatusInternalServerError
}

func isRecentlyClaimed(task *domain.Task, now time.Time) bool {
	if task == nil || task.WorkerID == "" || task.StartedAt.IsZero() {
		return false
	}
	return task.StartedAt.After(now.Add(-30 * time.Second))
}

// CancelTask 取消任务。仅允许取消 pending（>1min）、running（无活跃心跳）、suspended（无活跃心跳 + >15min）的任务。
func (h *WorkflowHandler) CancelTask(c *gin.Context) {
	var req dto.CancelTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx := c.Request.Context()
	task, err := h.taskRepo.GetByID(ctx, req.TaskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}

	userID := c.GetInt64(consts.UserID)
	if userID != 0 && task.UserID != userID {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	var reason string
	switch task.Status {
	case domain.TaskPending:
		if time.Since(task.CreatedAt) < 1*time.Minute {
			response.Error(c, http.StatusBadRequest, "task is too recent to cancel, please wait at least 1 minute")
			return
		}
		if h.billingTaskSvc != nil {
			if err := h.billingTaskSvc.CancelTaskFreeze(ctx, task.ID, "user canceled pending task"); err != nil {
				response.Error(c, http.StatusInternalServerError, err.Error())
				return
			}
		}
		reason = "user canceled pending task"

	case domain.TaskRunning:
		alive, err := h.isAnyNodeAlive(ctx, task.ID)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
		if alive {
			response.Error(c, http.StatusBadRequest, "task is still active, cannot cancel")
			return
		}
		if h.billingTaskSvc != nil {
			if err := h.billingTaskSvc.RefundTask(ctx, task.ID, "user canceled running task"); err != nil {
				response.Error(c, http.StatusInternalServerError, err.Error())
				return
			}
		}
		reason = "user canceled running task"

	case domain.TaskSuspended:
		alive, err := h.isAnyNodeAlive(ctx, task.ID)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
		if alive {
			response.Error(c, http.StatusBadRequest, "task is still active, cannot cancel")
			return
		}
		if time.Since(task.UpdatedAt) < 15*time.Minute {
			response.Error(c, http.StatusBadRequest, "task suspended recently, please wait at least 15 minutes before canceling")
			return
		}
		if h.billingTaskSvc != nil {
			if err := h.billingTaskSvc.RefundTask(ctx, task.ID, "user canceled suspended task"); err != nil {
				response.Error(c, http.StatusInternalServerError, err.Error())
				return
			}
		}
		reason = "user canceled suspended task"

	default:
		response.Error(c, http.StatusBadRequest, fmt.Sprintf("task status %s cannot be canceled", task.Status))
		return
	}

	task.Status = domain.TaskCanceled
	task.ErrorMessage = reason
	if err := h.taskRepo.Update(ctx, task); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.cancelTaskNodes(ctx, task.ID, reason); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.cancelChildTasks(ctx, task.ID, "canceled by parent task"); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{
		"task_id": task.ID,
		"status":  task.Status,
	})
}

func (h *WorkflowHandler) cancelChildTasks(ctx context.Context, parentTaskID int64, reason string) error {
	return h.cancelChildTasksRecursive(ctx, parentTaskID, reason, map[int64]struct{}{})
}

func (h *WorkflowHandler) cancelChildTasksRecursive(
	ctx context.Context,
	parentTaskID int64,
	reason string,
	visited map[int64]struct{},
) error {
	if _, ok := visited[parentTaskID]; ok {
		return nil
	}
	visited[parentTaskID] = struct{}{}

	children, err := h.taskRepo.ListChildrenByParentID(ctx, parentTaskID)
	if err != nil {
		return err
	}

	for _, child := range children {
		if child == nil {
			continue
		}
		if err := h.cancelChildTasksRecursive(ctx, child.ID, reason, visited); err != nil {
			return err
		}
		switch child.Status {
		case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended:
			child.Status = domain.TaskCanceled
			child.ErrorMessage = reason
			child.OutputJSON = nil
			child.Progress = 0
			if err := h.taskRepo.Update(ctx, child); err != nil {
				return err
			}
			if err := h.cancelTaskNodes(ctx, child.ID, reason); err != nil {
				return err
			}
		}
	}

	return nil
}

func (h *WorkflowHandler) cancelTaskNodes(ctx context.Context, taskID int64, reason string) error {
	if h.nodeRuntimeRepo == nil {
		return nil
	}
	runtimes, err := h.nodeRuntimeRepo.FindByTaskID(ctx, taskID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, runtime := range runtimes {
		if runtime == nil || !isCancelableNodeState(runtime.State) {
			continue
		}
		runtime.State = domain.NodeCanceled
		runtime.Error = reason
		runtime.FinishedAt = &now
		runtime.LastHeartbeat = nil
		runtime.Progress = 0
		if err := h.nodeRuntimeRepo.Update(ctx, runtime); err != nil {
			return err
		}
	}
	return nil
}

func isCancelableNodeState(state domain.NodeState) bool {
	switch state {
	case domain.NodePending,
		domain.NodeReady,
		domain.NodeRunning,
		domain.NodeAwaiting,
		domain.NodeRetrying,
		domain.NodeSuccessPendingEdges,
		domain.NodeFailedPendingEdges:
		return true
	default:
		return false
	}
}

func (h *WorkflowHandler) isAnyNodeAlive(ctx context.Context, taskID int64) (bool, error) {
	nodes, err := h.nodeRuntimeRepo.FindByTaskID(ctx, taskID)
	if err != nil {
		return false, err
	}
	now := time.Now()
	for _, n := range nodes {
		if n == nil || n.LastHeartbeat == nil {
			continue
		}
		if now.Sub(*n.LastHeartbeat) < 30*time.Second {
			return true, nil
		}
	}
	return false, nil
}

// RunWorkflow 运行工作流
func (h *WorkflowHandler) RunWorkflow(c *gin.Context) {

	idStr := c.Param("id")

	workflowID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid workflow id")
		return
	}

	var req dto.RunWorkflowReq

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx := c.Request.Context()

	// 1 获取 workflow
	wf, err := h.workflowRepo.GetByID(ctx, workflowID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "workflow not found")
		return
	}

	// 2 获取最新版本
	version, err := h.workflowVersionRepo.GetLatestByWorkflowID(ctx, wf.ID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "workflow has no published version")
		return
	}

	taskID := h.iSrv.GenSnowID()
	if req.Input == nil {
		req.Input = make(map[string]any)
	}
	req.Input["callback_token"] = strconv.FormatInt(taskID, 10)
	// 3 序列化 input
	inputJSON, _ := json.Marshal(req.Input)

	// 4 创建 Task
	task := domain.Task{
		ID:                   taskID,
		RootID:               taskID,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: workflowID,
		Status:               domain.TaskPending,
		InputJSON:            inputJSON,
	}

	err = h.taskRepo.Create(ctx, &task)
	if err != nil {
		response.Error(c, consts.Unknown, err.Error())
		return
	}
	// 注意：只有添加到 TaskQueue中 任务才会被执行
	err = h.taskRepo.Enqueue(ctx, task.ID)
	if err != nil {
		response.Error(c, consts.Unknown, err.Error())
		return
	}
	log.Println(taskID)
	// 5 返回 taskID
	response.Success(c, gin.H{
		"task_id": task.ID,
		"state":   task.Status,
	})
}

func (h *WorkflowHandler) ForkTask(c *gin.Context) {
	idStr := c.Param("id")
	parentTaskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}

	var req dto.ForkTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx := c.Request.Context()

	parentTask, err := h.taskRepo.GetByID(ctx, parentTaskID)
	if err != nil || parentTask == nil {
		response.Error(c, http.StatusBadRequest, "parent task not found")
		return
	}

	userID := c.GetInt64(consts.UserID)
	if userID != 0 && parentTask.UserID != userID {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	var resumeSpec *domain.ResumeSpec
	if req.ResumeSpec != nil {
		resumeSpec = &domain.ResumeSpec{
			ResumeFrom: req.ResumeSpec.ResumeFrom,
			Patches:    h.toDomainRuntimePatches(req.ResumeSpec.Patches),
		}
	}

	editLabel := req.EditLabel
	if editLabel == "" && req.EditAction != "" {
		editLabel = req.EditAction
	}

	newTask, err := h.taskForkService.RedoRun(
		ctx,
		parentTaskID,
		resumeSpec,
		req.OverrideInput,
		req.EditAction,
		editLabel,
		"",
	)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	response.Success(c, gin.H{
		"task_id":     newTask.ID,
		"forked_from": parentTask.ID,
		"status":      newTask.Status,
	})
}

// PatchPreviewTask 预览 fork/redo 的执行计划。
// POST /user/works/:id/patch-preview
//
// 客户端在正式 fork 之前先调用此接口，查看哪些节点会被复用、重跑、patch，
// 帮助用户理解此次 fork 的代价（哪些节点会重新执行、产生扣费）。
func (h *WorkflowHandler) PatchPreviewTask(c *gin.Context) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}

	var req dto.ForkTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx := c.Request.Context()

	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}

	userID := c.GetInt64(consts.UserID)
	if userID != 0 && task.UserID != userID {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	if task.Status != domain.TaskSuccess &&
		task.Status != domain.TaskFailed &&
		task.Status != domain.TaskSuspended &&
		task.Status != domain.TaskCanceled {
		response.Error(c, http.StatusBadRequest, "only terminal or suspended tasks can be forked")
		return
	}

	var resumeSpec *domain.ResumeSpec
	if req.ResumeSpec != nil {
		resumeSpec = &domain.ResumeSpec{
			ResumeFrom: req.ResumeSpec.ResumeFrom,
			Patches:    h.toDomainRuntimePatches(req.ResumeSpec.Patches),
		}
	}

	if h.eng == nil {
		response.Error(c, http.StatusInternalServerError, "engine not available")
		return
	}

	plan, _, err := h.eng.PreviewRunPlan(ctx, task, resumeSpec, req.OverrideInput)
	if err != nil {
		response.Success(c, gin.H{
			"valid":   false,
			"message": err.Error(),
		})
		return
	}

	nodes := make([]gin.H, 0, len(plan.TopoOrder))
	executeCount, reuseCount, patchCount := 0, 0, 0

	hasFailedChildren := h.collectHasFailedChildren(ctx, task.ID, plan)

	for _, nodeName := range plan.TopoOrder {
		np := plan.Nodes[nodeName]
		if np == nil {
			continue
		}
		action := string(np.Action)
		switch action {
		case "execute":
			executeCount++
		case "reuse":
			reuseCount++
		case "patch":
			patchCount++
		}

		nodes = append(nodes, gin.H{
			"name":                np.Name,
			"label":               np.Label,
			"type":                string(np.NodeType),
			"action":              action,
			"reason":              string(np.Reason),
			"reuse_kind":          string(np.ReuseKind),
			"is_patched":          np.Action == engine2.PlanActionPatch,
			"is_resume_boundary":  plan.ResumeFrom != "" && np.Name == plan.ResumeFrom,
			"has_failed_children": hasFailedChildren[np.Name],
		})
	}

	mode := "fork"
	if task.ForkedFrom != nil {
		mode = "re_fork"
	}

	response.Success(c, gin.H{
		"valid": true,
		"plan": gin.H{
			"mode":        mode,
			"resume_from": plan.ResumeFrom,
			"summary": gin.H{
				"execute_count": executeCount,
				"reuse_count":   reuseCount,
				"patch_count":   patchCount,
			},
			"nodes": nodes,
		},
	})
}

// collectHasFailedChildren 查看 parentTaskID 下，fan-out 节点 (map/loop/subworkflow) 是否有 failed/canceled 子任务。
func (h *WorkflowHandler) collectHasFailedChildren(ctx context.Context, parentTaskID int64, plan *engine2.RunPlan) map[string]bool {
	result := map[string]bool{}
	if parentTaskID == 0 {
		return result
	}

	for _, nodeName := range plan.TopoOrder {
		np := plan.Nodes[nodeName]
		if np == nil {
			continue
		}
		switch np.NodeType {
		case definition.NodeMap, definition.NodeLoop, definition.NodeSubWorkflow:
			children, err := h.taskRepo.ListByParentNode(ctx, parentTaskID, nodeName)
			if err != nil || len(children) == 0 {
				continue
			}
			for _, child := range children {
				if child != nil && (child.Status == domain.TaskFailed || child.Status == domain.TaskCanceled) {
					result[nodeName] = true
					break
				}
			}
		}
	}

	return result
}

// applyOverrideInput 将 override_input（key 为点分路径如 "spec.model"）深层合并到子任务的 input_json 上。
func (h *WorkflowHandler) applyOverrideInput(ctx context.Context, taskID int64, override map[string]any) error {
	if len(override) == 0 {
		return nil
	}

	child, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || child == nil {
		return fmt.Errorf("child task %d not found", taskID)
	}

	var input map[string]any
	if len(child.InputJSON) > 0 {
		if err := json.Unmarshal(child.InputJSON, &input); err != nil {
			return fmt.Errorf("unmarshal input_json: %w", err)
		}
	}
	if input == nil {
		input = make(map[string]any)
	}

	for path, val := range override {
		if err := engine2.SetByPath(input, path, val); err != nil {
			return fmt.Errorf("path %s: %w", path, err)
		}
	}

	updated, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal updated input: %w", err)
	}
	child.InputJSON = updated
	return h.taskRepo.Update(ctx, child)
}

// GetUserNodeDefinitionData 获取用户侧节点可编辑数据（轻量版）。
// GET /user/works/:id/nodes/:node
//
// 只返回 output + input_mapping + 基本元信息，不做 resolved_input 求值，
// 不给 checkpoint / hash / heartbeat 等引擎内部数据。
func (h *WorkflowHandler) GetUserNodeDefinitionData(c *gin.Context) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}

	nodeName := strings.TrimSpace(c.Param("node"))
	if nodeName == "" {
		response.Error(c, http.StatusBadRequest, "node is required")
		return
	}

	ctx := c.Request.Context()

	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}

	userID := c.GetInt64(consts.UserID)
	if userID != 0 && task.UserID != userID {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	rt, err := h.nodeRuntimeRepo.FindByTaskIDAndNode(ctx, taskID, nodeName)
	if err != nil || rt == nil {
		response.Error(c, http.StatusBadRequest, "node runtime not found")
		return
	}

	var (
		label string

		nodeType     string
		inputMapping map[string]string
	)

	dbVersion, err := h.workflowVersionRepo.Get(ctx, task.WorkflowVersionID)
	if err == nil && dbVersion != nil {
		var wfDef definition.WorkflowDefinition
		if json.Unmarshal(dbVersion.DefinitionJSON, &wfDef) == nil {
			wfRuntime, buildErr := h.builder.Build(&wfDef)
			if buildErr == nil {
				if defNode, ok := wfRuntime.Nodes()[nodeName]; ok {
					nodeType = string(defNode.Type)
					label = defNode.Label
					inputMapping = defNode.InputMapping
				}
			}
		}
	}

	output := rt.Output
	if h.assetSigner != nil {
		out := h.assetSigner.SignURLsInValue(ctx, task.UserID, output)
		out = h.assetSigner.HydrateAssetRefs(ctx, task.UserID, out)
		if signed, ok := out.(map[string]any); ok {
			output = signed
		}
	}
	response.Success(c, dto.UserNodeDataDTO{
		Name:  rt.Name,
		Label: label,

		Type:         nodeType,
		State:        string(rt.State),
		InputMapping: inputMapping,
		Output:       output,
	})
}

func (h *WorkflowHandler) GetUserNodeRuntimeData(c *gin.Context) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}
	nodeName := strings.TrimSpace(c.Param("node"))
	if nodeName == "" {
		response.Error(c, http.StatusBadRequest, "node is required")
		return
	}
	ctx := c.Request.Context()
	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}
	rt, err := h.nodeRuntimeRepo.FindByTaskIDAndNode(ctx, taskID, nodeName)
	if err != nil || rt == nil {
		response.Error(c, http.StatusBadRequest, "node runtime not found")
		return
	}
	userID := c.GetInt64(consts.UserID)
	if userID != 0 && task.UserID != userID {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}
	output := rt.Output
	if h.assetSigner != nil {
		out := h.assetSigner.SignURLsInValue(ctx, task.UserID, output)
		out = h.assetSigner.HydrateAssetRefs(ctx, task.UserID, out)
		if signed, ok := out.(map[string]any); ok {
			output = signed
		}
	}

	response.Success(c, gin.H{
		"name":           rt.Name,
		"task_id":        taskID,
		"id":             rt.ID,
		"output":         output,
		"error":          rt.Error,
		"state":          string(rt.State),
		"last_heartbeat": rt.LastHeartbeat.Unix(),
		"started_at":     rt.StartedAt.Unix(),
		"finished_at":    rt.FinishedAt.Unix(),
	})
}

func (h *WorkflowHandler) toDomainRuntimePatches(in []dto.RuntimePatchDTO) []domain.RuntimePatch {
	if len(in) == 0 {
		return nil
	}

	out := make([]domain.RuntimePatch, 0, len(in))
	for _, p := range in {
		out = append(out, domain.RuntimePatch{
			Target: domain.PatchTarget(p.Target),
			Node:   p.Node,
			Path:   p.Path,
			Op:     domain.PatchOp(p.Op),
			Value:  p.Value,
			Label:  p.Label,
		})
	}
	return out
}

// GetEvents 支持增量查询events
func (h *WorkflowHandler) GetEvents(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusBadRequest, "task not found")
		return
	}

	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	// after_sequence 用于客户端增量恢复（仅返回 sequence > X 的 Persistent 事件）
	afterSequence := int64(0)
	if as := strings.TrimSpace(c.Query("after_sequence")); as != "" {
		if v, parseErr := strconv.ParseInt(as, 10, 64); parseErr == nil {
			afterSequence = v
		}
	}

	// grade 过滤：persistent（默认）/ all / transient / audit
	gradeParam := strings.TrimSpace(c.Query("grade"))
	if gradeParam == "" && afterSequence > 0 {
		gradeParam = "persistent" // 增量恢复默认只取 Persistent
	}

	typeParam := strings.TrimSpace(c.Query("type"))
	var events []domain.TaskEvent
	switch {
	case afterSequence > 0:
		events, err = h.eventRepo.FindPersistentByTaskID(ctx, taskID, afterSequence, 0, true)
	case typeParam != "":
		prefixes := make([]string, 0)
		for _, p := range strings.Split(typeParam, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				prefixes = append(prefixes, trimmed)
			}
		}
		events, err = h.eventRepo.FindByTaskIDAndTypePrefixes(ctx, taskID, prefixes, true)
	default:
		if gradeParam == "all" {
			events, err = h.eventRepo.FindByTaskID(ctx, taskID, true)
		} else {
			events, err = h.eventRepo.FindPersistentByTaskID(ctx, taskID, 0, 0, true)
		}
	}
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, events)
}

func (h *WorkflowHandler) parseTaskID(c *gin.Context) (int64, bool) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return 0, false
	}
	return taskID, true
}

// ReplayTaskStream 触发 WS 事件流回放。
// 立即返回 replay_channel，客户端订阅该 channel 接收事件；后台 goroutine 推送回放事件。
func (h *WorkflowHandler) ReplayTaskStream(c *gin.Context) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}

	speedMs := 200
	if v := c.Query("speed_ms"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			speedMs = n
		}
	}

	ctx := c.Request.Context()

	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusNotFound, "task not found")
		return
	}
	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	// 后台推送，不阻塞 HTTP 响应
	go func() {
		_ = h.eng.ReplayToWS(context.Background(), taskID, speedMs)
	}()

	response.Success(c, gin.H{
		"task_id":  taskID,
		"speed_ms": speedMs,
	})
}

// ReplayTask 对已完成任务做纯读逻辑回放，返回结构化 trace。
// 不重新调用任何工具，不写入任何状态。
func (h *WorkflowHandler) ReplayTask(c *gin.Context) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return
	}

	ctx := c.Request.Context()

	// 权限校验：先加载任务确认归属
	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		response.Error(c, http.StatusNotFound, "task not found")
		return
	}
	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	trace, err := h.eng.Replay(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	response.Success(c, trace)
}

func (h *WorkflowHandler) canAccessTask(c *gin.Context, task *domain.Task) bool {
	if task == nil {
		return false
	}
	userID := c.GetInt64(consts.UserID)
	if userID == 0 {
		return true
	}
	return task.UserID == userID
}
