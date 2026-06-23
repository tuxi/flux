package handler

import (
	"context"
	"encoding/json"
	"flux/internal/consts"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/dto"
	engine2 "flux/engine"
	"flux/pkg/response"
	repository2 "flux/repository"
	"flux/service"
	"flux/workflow"
	"flux/workflow/nodes"

	"github.com/gin-gonic/gin"
)

type RunInspectorHandler struct {
	eng                 *engine2.Engine
	taskRepo            repository2.TaskRepository
	nodeRuntimeRepo     repository2.NodeRuntimeRepository
	eventRepo           repository2.EventRepository
	awaitBindingRepo    repository2.AwaitBindingRepository
	workflowRepo        repository2.WorkflowRepository
	workflowVersionRepo repository2.WorkflowVersionRepository
	builder             *workflow.Builder
	redoService         service.RunRedoService
}

func NewRunInspectorHandler(
	eng *engine2.Engine,
	taskRepo repository2.TaskRepository,
	nodeRuntimeRepo repository2.NodeRuntimeRepository,
	eventRepo repository2.EventRepository,
	awaitBindingRepo repository2.AwaitBindingRepository,
	workflowRepo repository2.WorkflowRepository,
	workflowVersionRepo repository2.WorkflowVersionRepository,
	builder *workflow.Builder,
	redoService service.RunRedoService,
) *RunInspectorHandler {
	return &RunInspectorHandler{
		eng:                 eng,
		taskRepo:            taskRepo,
		nodeRuntimeRepo:     nodeRuntimeRepo,
		eventRepo:           eventRepo,
		awaitBindingRepo:    awaitBindingRepo,
		workflowRepo:        workflowRepo,
		workflowVersionRepo: workflowVersionRepo,
		builder:             builder,
		redoService:         redoService,
	}
}

// ListRuns
//
// GET /runs?page=1&page_size=20
//
// 第一版直接复用 task 列表能力，返回轻量 runs 列表。
func (h *RunInspectorHandler) ListRuns(c *gin.Context) {
	var req dto.PageRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	userID := c.GetInt64(consts.UserID)

	tasks, total, err := h.taskRepo.ListByUser(c.Request.Context(), userID, req)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	runs := make([]dto.RunSummaryDTO, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		runs = append(runs, h.toRunSummaryDTO(task))
	}

	response.Success(c, gin.H{
		"runs":  runs,
		"total": total,
	})
}

// GetRunInspector
//
// GET /runs/:id/inspect
//
// 返回：
// 1. run summary
// 2. workflow summary
// 3. dag
// 4. snapshot
// 5. lineage(第一版先给基础字段)
// 6. patches / resume
func (h *RunInspectorHandler) GetRunInspector(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	task, wfDef, wfVersion, wfRuntime, runtimes, err := h.loadRunBundle(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	resp := dto.RunInspectorResp{
		Run:           h.toRunSummaryDTO(task),
		Workflow:      h.toWorkflowSummaryDTO(task, wfDef, wfVersion),
		DAG:           h.toRunDAGDTO(wfRuntime, runtimes, task),
		Snapshot:      h.toRunSnapshotDTO(task),
		Lineage:       h.buildRunLineageSummary(task),
		Patches:       h.toRuntimePatchDTOs(task.PatchJSON),
		Resume:        h.toResumeSpecSummaryDTO(task),
		AwaitBindings: h.listRunAwaitBindings(ctx, task.ID),
	}

	response.Success(c, resp)
}

// GetRunDAG
//
// GET /runs/:id/dag
func (h *RunInspectorHandler) GetRunDAG(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	task, _, _, wfRuntime, runtimes, err := h.loadRunBundle(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	response.Success(c, h.toRunDAGDTO(wfRuntime, runtimes, task))
}

// GetRunTimeline
//
// GET /runs/:id/timeline
//
// 第一版策略：
// - 直接从 eventRepo 读 task events
// - 做最小映射
// - phase 暂时通过 type 推导
func (h *RunInspectorHandler) GetRunTimeline(c *gin.Context) {
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

	typeParam := strings.TrimSpace(c.Query("type"))
	var events []domain.TaskEvent
	if typeParam != "" {
		prefixes := make([]string, 0)
		for _, p := range strings.Split(typeParam, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				prefixes = append(prefixes, trimmed)
			}
		}
		events, err = h.eventRepo.FindByTaskIDAndTypePrefixes(ctx, taskID, prefixes, false)
	} else {
		// Timeline 只展示 Persistent 事件，Transient 事件通过 WebSocket 实时消费
		events, err = h.eventRepo.FindPersistentByTaskID(ctx, taskID, 0, 0, false)
	}
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]dto.RunTimelineEventDTO, 0, len(events))
	for _, evt := range events {
		resp = append(resp, h.toRunTimelineEventDTO(evt))
	}

	response.Success(c, resp)
}

// GetRunNodeDetail
//
// GET /runs/:id/nodes/:node
func (h *RunInspectorHandler) GetRunNodeDetail(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	nodeName := strings.TrimSpace(c.Param("node"))
	if nodeName == "" {
		response.Error(c, http.StatusBadRequest, "node is required")
		return
	}

	task, wfDef, _, wfRuntime, runtimes, err := h.loadRunBundle(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	runtimeMap := make(map[string]*domain.NodeRuntime, len(runtimes))
	for _, rt := range runtimes {
		if rt == nil {
			continue
		}
		runtimeMap[rt.Name] = rt
	}

	rt := runtimeMap[nodeName]
	if rt == nil {
		response.Error(c, http.StatusBadRequest, "node runtime not found")
		return
	}

	parentDTO, _ := h.buildParentNodeDTO(ctx, task, nodeName)

	events, _ := h.eventRepo.FindPersistentByTaskID(ctx, taskID, 0, 0, false)
	nodeTimeline := h.filterNodeTimeline(events, nodeName)

	resp := dto.RunNodeDetailResp{
		Run:          h.toRunSummaryDTO(task),
		Node:         h.toRunNodeDetailDTO(wfRuntime, rt),
		Parent:       parentDTO,
		Patches:      h.filterNodePatches(task.PatchJSON, nodeName),
		Timeline:     nodeTimeline,
		AwaitBinding: h.getNodeAwaitBinding(ctx, task.ID, nodeName),
		Diff: h.buildRunNodeDiffDTO(
			ctx,
			task,
			rt,
			nodeName,
		),
	}

	_ = wfDef // 第一版先保留，后续可用于 schema / path 校验增强
	response.Success(c, resp)
}

func (h *RunInspectorHandler) listRunAwaitBindings(ctx context.Context, taskID int64) []dto.RunAwaitBindingDTO {
	if h.awaitBindingRepo == nil {
		return nil
	}
	bindings, err := h.awaitBindingRepo.ListByTaskID(ctx, taskID)
	if err != nil || len(bindings) == 0 {
		return nil
	}
	out := make([]dto.RunAwaitBindingDTO, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		out = append(out, h.toRunAwaitBindingDTO(binding))
	}
	return out
}

func (h *RunInspectorHandler) getNodeAwaitBinding(ctx context.Context, taskID int64, nodeName string) *dto.RunAwaitBindingDTO {
	if h.awaitBindingRepo == nil || nodeName == "" {
		return nil
	}
	binding, err := h.awaitBindingRepo.GetByTaskAndNode(ctx, taskID, nodeName)
	if err != nil || binding == nil {
		return nil
	}
	dtoValue := h.toRunAwaitBindingDTO(binding)
	return &dtoValue
}

// GetRunNodeDiff
//
// GET /runs/:id/nodes/:node/diff
//
// 第一版只做：
// - 和 fork parent 同名节点比较
// - input/output/checkpoint 三类 diff
func (h *RunInspectorHandler) GetRunNodeDiff(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	nodeName := strings.TrimSpace(c.Param("node"))
	if nodeName == "" {
		response.Error(c, http.StatusBadRequest, "node is required")
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

	rt, err := h.nodeRuntimeRepo.FindByTaskIDAndNode(ctx, taskID, nodeName)
	if err != nil || rt == nil {
		response.Error(c, http.StatusBadRequest, "node runtime not found")
		return
	}

	diff := h.buildRunNodeDiffDTO(ctx, task, rt, nodeName)
	if diff == nil {
		diff = &dto.RunNodeDiffDTO{}
	}

	response.Success(c, diff)
}

// PatchPreview
//
// POST /runs/:id/patch-preview
//
// 第一版策略：
// - 校验 task 存在
// - 加载 workflow definition
// - 做最小 patch / resume 校验
// - 先返回一个“预览骨架”
// 后续可以直接接 engine.BuildRunPlan 产出真实预览
func (h *RunInspectorHandler) PatchPreview(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	var req dto.PatchPreviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	task, _, _, _, _, err := h.loadRunBundle(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	resumeSpec := h.toDomainResumeSpec(req)

	plan, _, err := h.eng.PreviewRunPlan(
		ctx,
		task,
		resumeSpec,
		req.OverrideInput, // 当前 patch-preview 只预览 patch/resume；后续可扩展 override_input
	)
	if err != nil {
		response.Success(c, dto.PatchPreviewResp{
			Valid:   false,
			Message: err.Error(),
			RunPlan: dto.RunPlanPreviewDTO{
				Mode:         h.inferPreviewMode(task),
				ResumeFrom:   req.ResumeFrom,
				ParentTaskID: &task.ID,
				Nodes:        []dto.RunPlanNodePreviewDTO{},
			},
		})
		return
	}

	response.Success(c, dto.PatchPreviewResp{
		Valid:   true,
		Message: "ok",
		RunPlan: h.toRunPlanPreviewDTO(ctx, plan),
	})
}

// ------------------------- private helpers -------------------------

func (h *RunInspectorHandler) parseTaskID(c *gin.Context) (int64, bool) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid task id")
		return 0, false
	}
	return taskID, true
}

func (h *RunInspectorHandler) loadRunBundle(
	ctx context.Context,
	taskID int64,
) (
	*domain.Task,
	*definition.WorkflowDefinition,
	*domain.WorkflowVersion,
	workflow.Workflow,
	[]*domain.NodeRuntime,
	error,
) {
	task, err := h.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if task == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("task not found")
	}

	dbVersion, err := h.workflowVersionRepo.Get(ctx, task.WorkflowVersionID)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	var wfDef definition.WorkflowDefinition
	if err := json.Unmarshal(dbVersion.DefinitionJSON, &wfDef); err != nil {
		return nil, nil, nil, nil, nil, err
	}

	wfRuntime, err := h.builder.Build(&wfDef)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	runtimes, err := h.nodeRuntimeRepo.FindByTaskID(ctx, taskID)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	return task, &wfDef, dbVersion, wfRuntime, runtimes, nil
}

func (h *RunInspectorHandler) canAccessTask(c *gin.Context, task *domain.Task) bool {
	if task == nil {
		return false
	}
	userID := c.GetInt64(consts.UserID)
	if userID == 0 {
		return true
	}
	return task.UserID == userID
}

func (h *RunInspectorHandler) toRunSummaryDTO(task *domain.Task) dto.RunSummaryDTO {
	if task == nil {
		return dto.RunSummaryDTO{}
	}

	return dto.RunSummaryDTO{
		TaskID:        task.ID,
		RootID:        task.RootID,
		BaseRunID:     task.BaseRunID,
		ForkedFrom:    task.ForkedFrom,
		RunDepth:      task.RunDepth,
		Status:        string(task.Status),
		Progress:      task.Progress,
		Type:          task.Type,
		StartedAt:     &task.StartedAt,
		CreatedAt:     &task.CreatedAt,
		UpdatedAt:     &task.UpdatedAt,
		ErrorMessage:  task.ErrorMessage,
		EditAction:    task.EditAction,
		EditLabel:     task.EditLabel,
		ResumeFrom:    task.ResumeFrom,
		WorkflowID:    task.WorkflowDefinitionID,
		WorkflowVerID: task.WorkflowVersionID,
	}
}

func (h *RunInspectorHandler) toRunAwaitBindingDTO(binding *domain.AwaitBinding) dto.RunAwaitBindingDTO {
	if binding == nil {
		return dto.RunAwaitBindingDTO{}
	}
	now := time.Now()
	return dto.RunAwaitBindingDTO{
		ID:                  binding.ID,
		TaskID:              binding.TaskID,
		RootTaskID:          binding.RootTaskID,
		NodeName:            binding.NodeName,
		WorkflowVersionID:   binding.WorkflowVersionID,
		AwaitType:           string(binding.AwaitType),
		Source:              string(binding.Source),
		Status:              string(binding.Status),
		Provider:            binding.Provider,
		ProviderTaskID:      binding.ProviderTaskID,
		APITaskID:           binding.APITaskID,
		ExternalTaskID:      binding.ExternalTaskID,
		SignalName:          binding.SignalName,
		MessageName:         binding.MessageName,
		CallbackToken:       binding.CallbackToken,
		Correlation:         binding.Correlation,
		Config:              binding.Config,
		LastEventID:         binding.LastEventID,
		LastEventSource:     binding.LastEventSource,
		LastEventPayload:    binding.LastEventPayload,
		ResultPayload:       binding.ResultPayload,
		ErrorMessage:        binding.ErrorMessage,
		FallbackPollEnabled: binding.FallbackPollEnabled,
		FallbackPollTool:    binding.FallbackPollTool,
		PollAttempts:        binding.PollAttempts,
		MaxPollAttempts:     binding.MaxPollAttempts,
		LastPolledAt:        binding.LastPolledAt,
		NextPollAt:          binding.NextPollAt,
		WaitingStartedAt:    binding.WaitingStartedAt,
		TimeoutAt:           binding.TimeoutAt,
		CompletedAt:         binding.CompletedAt,
		FailedAt:            binding.FailedAt,
		CanceledAt:          binding.CanceledAt,
		CreatedAt:           binding.CreatedAt,
		UpdatedAt:           binding.UpdatedAt,
		StatusCategory:      inferAwaitBindingStatusCategory(binding),
		StatusLabel:         inferAwaitBindingStatusLabel(binding),
		WaitingFor:          inferAwaitBindingWaitingFor(binding),
		NextAction:          inferAwaitBindingNextAction(binding, now),
		IsTerminal:          isAwaitBindingTerminal(binding),
		CorrelationKeys:     sortedKeys(binding.Correlation),
		EventSummary: dto.RunAwaitEventSummaryDTO{
			LastSource:      binding.LastEventSource,
			HasLastPayload:  len(binding.LastEventPayload) > 0,
			LastPayloadKeys: sortedKeys(binding.LastEventPayload),
			HasResult:       len(binding.ResultPayload) > 0,
			ResultKeys:      sortedKeys(binding.ResultPayload),
		},
		PollSummary: dto.RunAwaitPollSummaryDTO{
			Enabled:      binding.FallbackPollEnabled,
			Tool:         binding.FallbackPollTool,
			Attempts:     binding.PollAttempts,
			MaxAttempts:  binding.MaxPollAttempts,
			LastPolledAt: binding.LastPolledAt,
			NextPollAt:   binding.NextPollAt,
			IsDue:        binding.NextPollAt != nil && !binding.NextPollAt.After(now) && binding.Status == domain.AwaitBindingWaiting,
			HasCapacity:  binding.MaxPollAttempts <= 0 || binding.PollAttempts < binding.MaxPollAttempts,
		},
	}
}

func inferAwaitBindingStatusCategory(binding *domain.AwaitBinding) string {
	if binding == nil {
		return "unknown"
	}
	switch binding.Status {
	case domain.AwaitBindingPending, domain.AwaitBindingWaiting, domain.AwaitBindingCompleting:
		return "active"
	case domain.AwaitBindingCompleted:
		return "success"
	case domain.AwaitBindingFailed, domain.AwaitBindingTimedOut:
		return "error"
	case domain.AwaitBindingCanceled:
		return "neutral"
	default:
		return "unknown"
	}
}

func inferAwaitBindingStatusLabel(binding *domain.AwaitBinding) string {
	if binding == nil {
		return "Unknown"
	}
	switch binding.Status {
	case domain.AwaitBindingPending:
		return "Pending"
	case domain.AwaitBindingWaiting:
		return "Waiting"
	case domain.AwaitBindingCompleting:
		return "Completing"
	case domain.AwaitBindingCompleted:
		return "Completed"
	case domain.AwaitBindingFailed:
		return "Failed"
	case domain.AwaitBindingTimedOut:
		return "Timed Out"
	case domain.AwaitBindingCanceled:
		return "Canceled"
	default:
		return strings.Title(string(binding.Status))
	}
}

func inferAwaitBindingWaitingFor(binding *domain.AwaitBinding) string {
	if binding == nil {
		return ""
	}
	switch binding.Source {
	case domain.AwaitSourceWebhook:
		if binding.Provider != nil {
			return "webhook:" + *binding.Provider
		}
		return "webhook"
	case domain.AwaitSourceWebhookOrPoll:
		if binding.Provider != nil {
			return "webhook_or_poll:" + *binding.Provider
		}
		return "webhook_or_poll"
	case domain.AwaitSourceSignal:
		if binding.SignalName != nil {
			return "signal:" + *binding.SignalName
		}
		return "signal"
	case domain.AwaitSourceMessage:
		if binding.MessageName != nil {
			return "message:" + *binding.MessageName
		}
		return "message"
	case domain.AwaitSourcePoll:
		if binding.FallbackPollTool != nil {
			return "poll:" + *binding.FallbackPollTool
		}
		return "poll"
	default:
		return string(binding.Source)
	}
}

func inferAwaitBindingNextAction(binding *domain.AwaitBinding, now time.Time) string {
	if binding == nil {
		return ""
	}
	switch binding.Status {
	case domain.AwaitBindingPending:
		return "activate_wait"
	case domain.AwaitBindingWaiting:
		if binding.TimeoutAt != nil && !binding.TimeoutAt.After(now) {
			return "timeout_due"
		}
		if binding.FallbackPollEnabled && binding.NextPollAt != nil && !binding.NextPollAt.After(now) {
			return "poll_due"
		}
		switch binding.Source {
		case domain.AwaitSourceSignal:
			return "wait_signal"
		case domain.AwaitSourceMessage:
			return "wait_message"
		case domain.AwaitSourceWebhook:
			return "wait_webhook"
		case domain.AwaitSourceWebhookOrPoll:
			return "wait_webhook_or_poll"
		case domain.AwaitSourcePoll:
			return "wait_poll"
		default:
			return "wait_external"
		}
	case domain.AwaitBindingCompleting:
		return "resume_task"
	case domain.AwaitBindingCompleted:
		return "done"
	case domain.AwaitBindingFailed:
		return "failed"
	case domain.AwaitBindingTimedOut:
		return "timed_out"
	case domain.AwaitBindingCanceled:
		return "canceled"
	default:
		return ""
	}
}

func isAwaitBindingTerminal(binding *domain.AwaitBinding) bool {
	if binding == nil {
		return false
	}
	switch binding.Status {
	case domain.AwaitBindingCompleted, domain.AwaitBindingFailed, domain.AwaitBindingTimedOut, domain.AwaitBindingCanceled:
		return true
	default:
		return false
	}
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

func (h *RunInspectorHandler) toWorkflowSummaryDTO(
	task *domain.Task,
	wfDef *definition.WorkflowDefinition,
	wfVersion *domain.WorkflowVersion,
) dto.WorkflowSummaryDTO {
	if task == nil {
		return dto.WorkflowSummaryDTO{}
	}

	name := ""
	desc := ""
	version := int64(0)

	if wfDef != nil {
		name = wfDef.Name
		desc = wfDef.Desc
		version = wfVersion.Version
	}

	return dto.WorkflowSummaryDTO{
		WorkflowID:  task.WorkflowDefinitionID,
		Name:        name,
		VersionID:   task.WorkflowVersionID,
		Version:     version,
		Description: desc,
	}
}

func (h *RunInspectorHandler) toRunSnapshotDTO(task *domain.Task) dto.RunSnapshotDTO {
	var input map[string]any
	var output map[string]any

	if len(task.InputJSON) > 0 {
		_ = json.Unmarshal(task.InputJSON, &input)
	}
	if len(task.OutputJSON) > 0 {
		_ = json.Unmarshal(task.OutputJSON, &output)
	}

	final, _ := output["final"].(map[string]any)

	return dto.RunSnapshotDTO{
		Input: input,
		Final: final,
	}
}

func (h *RunInspectorHandler) toRunDAGDTO(
	wfRuntime workflow.Workflow,
	runtimes []*domain.NodeRuntime,
	task *domain.Task,
) dto.RunDAGDTO {
	rtMap := make(map[string]*domain.NodeRuntime, len(runtimes))
	for _, rt := range runtimes {
		if rt == nil {
			continue
		}
		rtMap[rt.Name] = rt
	}

	nodesDTO := make([]dto.RunDAGNodeDTO, 0, len(wfRuntime.Order()))
	activatedEdges := map[string]bool{}

	stats := dto.RunDAGStatsDTO{}
	stats.TotalNodes = len(wfRuntime.Order())

	for _, nodeName := range wfRuntime.Order() {
		defNode, ok := wfRuntime.Nodes()[nodeName]
		if !ok {
			continue
		}

		rt := rtMap[nodeName]
		nodeDTO := h.toRunDAGNodeDTO(task, defNode, rt)

		nodesDTO = append(nodesDTO, nodeDTO)

		switch nodeDTO.State {
		case string(domain.NodeSuccess), string(domain.NodeSuccessPendingEdges):
			stats.SuccessNodes++
		case string(domain.NodeFailed), string(domain.NodeFailedPendingEdges):
			stats.FailedNodes++
		case string(domain.NodeRunning), string(domain.NodeReady), string(domain.NodeRetrying):
			stats.RunningNodes++
		case string(domain.NodeSkipped):
			stats.SkippedNodes++
		}

		if nodeDTO.IsPatched {
			stats.PatchedNodes++
		}
		if nodeDTO.ReuseKind == string(domain.ReuseNode) || nodeDTO.ReuseKind == string(domain.ReuseMapItems) {
			stats.ReusedNodes++
		}
		if nodeDTO.Action == "execute" {
			stats.ExecutedNodes++
		}

		if rt != nil && rt.ActivatedEdges != nil {
			for k, v := range rt.ActivatedEdges {
				activatedEdges[k] = v
			}
		}
	}

	edgesDTO := make([]dto.RunDAGEdgeDTO, 0)
	for _, from := range wfRuntime.Order() {
		for _, e := range wfRuntime.Graph().Edges[from] {
			key := from + "->" + e.To
			edgesDTO = append(edgesDTO, dto.RunDAGEdgeDTO{
				From:      from,
				To:        e.To,
				Activated: activatedEdges[key],
				Type:      string(e.Type),
				Condition: e.Condition,
				CaseKey:   e.CaseKey,
				Label:     e.Label,
				Priority:  e.Priority,
			})
		}
	}

	return dto.RunDAGDTO{
		Nodes:          nodesDTO,
		Edges:          edgesDTO,
		ActivatedEdges: activatedEdges,
		Stats:          stats,
	}
}

func (h *RunInspectorHandler) toRunDAGNodeDTO(
	task *domain.Task,
	defNode nodes.Node,
	rt *domain.NodeRuntime,
) dto.RunDAGNodeDTO {
	groupKind := ""
	groupID := ""
	parallelism := 0

	switch string(defNode.Type) {
	case "map":
		groupKind = "map"
		groupID = defNode.Name
		if raw, ok := defNode.Config["parallel"]; ok {
			switch v := raw.(type) {
			case int:
				parallelism = v
			case float64:
				parallelism = int(v)
			}
		}
	case "subworkflow":
		groupKind = "subworkflow"
		groupID = defNode.Name
	}

	if rt == nil {
		return dto.RunDAGNodeDTO{
			Name:  defNode.Name,
			Label: defNode.Label,

			Type:             string(defNode.Type),
			State:            string(domain.NodePending),
			Action:           "execute",
			IsPatched:        false,
			IsResumeBoundary: task != nil && task.ResumeFrom != nil && *task.ResumeFrom == defNode.Name,
		}
	}

	action := h.inferNodeAction(rt)
	mapReuse := h.extractMapItemReuseIndexes(rt)

	return dto.RunDAGNodeDTO{
		Name:  rt.Name,
		Label: defNode.Label,

		Type:              string(defNode.Type),
		Version:           defNode.Version,
		Config:            defNode.Config,
		InputMapping:      defNode.InputMapping,
		GroupKind:         groupKind,
		GroupID:           groupID,
		Parallelism:       parallelism,
		State:             string(rt.State),
		Action:            action,
		ExecutionReason:   rt.ExecutionReason,
		ReuseKind:         string(rt.ReuseKind),
		IsInjected:        rt.IsInjected,
		IsDirty:           rt.IsDirty,
		DirtyReason:       rt.DirtyReason,
		IsPatched:         rt.ExecutionReason == "patched_node" || rt.DirtyReason == "patched_state",
		IsResumeBoundary:  task != nil && task.ResumeFrom != nil && *task.ResumeFrom == rt.Name,
		HasCheckpoint:     len(rt.Checkpoint) > 0,
		HasOutput:         len(rt.Output) > 0,
		Progress:          rt.Progress,
		Index:             rt.Index,
		BizIndex:          rt.BizIndex,
		Weight:            rt.Weight,
		ParentTaskID:      nil,
		ReusedFromTaskID:  rt.ReusedFromTaskID,
		ReusedFromNode:    rt.ReusedFromNode,
		PatchCount:        0,
		MapItemReuse:      mapReuse,
		ResolvedInputHash: rt.InputHash,
		OutputHash:        rt.OutputHash,
		Meta: map[string]any{
			"node_type": string(defNode.Type),
		},
	}
}

func (h *RunInspectorHandler) inferNodeAction(rt *domain.NodeRuntime) string {
	if rt == nil {
		return "execute"
	}
	if rt.ExecutionReason == "patched_node" {
		return "patch"
	}
	if rt.ReuseKind == domain.ReuseNode || rt.IsInjected {
		return "reuse"
	}
	return "execute"
}

func (h *RunInspectorHandler) extractMapItemReuseIndexes(rt *domain.NodeRuntime) []int {
	if rt == nil || rt.Checkpoint == nil {
		return nil
	}

	raw, _ := rt.Checkpoint["reused_items"].(map[string]any)
	if raw == nil {
		return nil
	}

	out := make([]int, 0)
	for k, v := range raw {
		b, ok := v.(bool)
		if !ok || !b {
			continue
		}
		idx, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

func (h *RunInspectorHandler) toRunNodeDetailDTO(
	wfRuntime workflow.Workflow,
	rt *domain.NodeRuntime,
) dto.RunNodeDetailDTO {
	var (
		nodeType     string
		version      string
		config       map[string]any
		inputMapping map[string]string
		label        string
	)

	if wfRuntime != nil {
		if defNode, ok := wfRuntime.Nodes()[rt.Name]; ok {
			nodeType = string(defNode.Type)
			version = defNode.Version
			config = defNode.Config
			label = defNode.Label
			inputMapping = defNode.InputMapping
		}
	}

	return dto.RunNodeDetailDTO{
		Name:  rt.Name,
		Label: label,

		Type:            nodeType,
		Version:         version,
		Config:          config,
		InputMapping:    inputMapping,
		State:           string(rt.State),
		Action:          h.inferNodeAction(rt),
		ExecutionReason: rt.ExecutionReason,
		ReuseKind:       string(rt.ReuseKind),
		IsInjected:      rt.IsInjected,
		IsDirty:         rt.IsDirty,
		DirtyReason:     rt.DirtyReason,
		InputHash:       rt.InputHash,
		OutputHash:      rt.OutputHash,
		ResolvedInput:   rt.ResolvedInput,
		Output:          rt.Output,
		Checkpoint:      rt.Checkpoint,
		ActivatedEdges:  rt.ActivatedEdges,
		Error:           rt.Error,
		StartedAt:       rt.StartedAt,
		FinishedAt:      rt.FinishedAt,
		LastHeartbeat:   rt.LastHeartbeat,
		CheckpointedAt:  rt.CheckpointedAt,
		Progress:        rt.Progress,
		Meta:            map[string]any{},
	}
}

func (h *RunInspectorHandler) buildParentNodeDTO(
	ctx context.Context,
	task *domain.Task,
	nodeName string,
) (*dto.RunNodeParentDTO, error) {
	if task == nil || task.ForkedFrom == nil {
		return nil, nil
	}

	parentRT, err := h.nodeRuntimeRepo.FindByTaskIDAndNode(ctx, *task.ForkedFrom, nodeName)
	if err != nil || parentRT == nil {
		return nil, err
	}

	return &dto.RunNodeParentDTO{
		TaskID:        parentRT.TaskID,
		NodeName:      parentRT.Name,
		State:         string(parentRT.State),
		InputHash:     parentRT.InputHash,
		OutputHash:    parentRT.OutputHash,
		ResolvedInput: parentRT.ResolvedInput,
		Output:        parentRT.Output,
		Checkpoint:    parentRT.Checkpoint,
	}, nil
}

func (h *RunInspectorHandler) toRuntimePatchDTOs(data []byte) []dto.RuntimePatchDTO {
	patches := h.parseRuntimePatches(data)
	if len(patches) == 0 {
		return nil
	}

	out := make([]dto.RuntimePatchDTO, 0, len(patches))
	for _, p := range patches {
		out = append(out, dto.RuntimePatchDTO{
			Target: string(p.Target),
			Node:   p.Node,
			Path:   p.Path,
			Op:     string(p.Op),
			Value:  p.Value,
			Label:  p.Label,
		})
	}
	return out
}

func (h *RunInspectorHandler) parseRuntimePatches(data []byte) []domain.RuntimePatch {
	if len(data) == 0 {
		return nil
	}
	var patches []domain.RuntimePatch
	_ = json.Unmarshal(data, &patches)
	return patches
}

func (h *RunInspectorHandler) toResumeSpecSummaryDTO(task *domain.Task) *dto.ResumeSpecSummaryDTO {
	if task == nil || task.ResumeFrom == nil {
		return nil
	}

	patches := h.parseRuntimePatches(task.PatchJSON)
	return &dto.ResumeSpecSummaryDTO{
		ResumeFrom: *task.ResumeFrom,
		PatchCount: len(patches),
	}
}

func (h *RunInspectorHandler) buildRunLineageSummary(task *domain.Task) *dto.RunLineageSummaryDTO {
	if task == nil {
		return nil
	}

	return &dto.RunLineageSummaryDTO{
		BaseRunID:      task.BaseRunID,
		ForkedFrom:     task.ForkedFrom,
		AncestorRunIDs: nil, // 第一版留空，后续可递归查
		ChildRunIDs:    nil, // 第一版留空，后续可按 base/fork 查询
	}
}

func (h *RunInspectorHandler) toRunTimelineEventDTO(evt domain.TaskEvent) dto.RunTimelineEventDTO {
	return dto.RunTimelineEventDTO{
		ID:        evt.ID,
		TaskID:    evt.TaskID,
		NodeName:  evt.Step,
		Phase:     inferTimelinePhase(evt),
		Type:      evt.Type,
		Title:     buildTimelineTitle(evt),
		Message:   evt.Message,
		Level:     evt.Level,
		Progress:  evt.Progress,
		CreatedAt: evt.CreatedAt,
		Meta:      evt.Meta,
	}
}

func inferTimelinePhase(evt domain.TaskEvent) string {
	t := evt.Type
	switch {
	case strings.HasPrefix(t, "await_replay_"):
		return "await_replay"
	case strings.Contains(t, "planned"), strings.Contains(t, "patch"):
		return "planning"
	case strings.Contains(t, "materialize"), strings.Contains(t, "reuse"):
		return "materialization"
	default:
		return "execution"
	}
}

func buildTimelineTitle(evt domain.TaskEvent) string {
	if strings.HasPrefix(evt.Type, "await_replay_") {
		if evt.Step == "" || evt.Step == "task" {
			return fmt.Sprintf("replay · %s", evt.Type)
		}
		return fmt.Sprintf("%s · replay · %s", evt.Step, evt.Type)
	}
	if evt.Step == "" || evt.Step == "task" {
		return evt.Type
	}
	return fmt.Sprintf("%s · %s", evt.Step, evt.Type)
}

func (h *RunInspectorHandler) filterNodeTimeline(
	events []domain.TaskEvent,
	nodeName string,
) []dto.RunTimelineEventDTO {
	if len(events) == 0 {
		return nil
	}

	out := make([]dto.RunTimelineEventDTO, 0)
	for _, evt := range events {
		if evt.Step != nodeName {
			continue
		}
		out = append(out, h.toRunTimelineEventDTO(evt))
	}
	return out
}

func (h *RunInspectorHandler) filterNodePatches(
	data []byte,
	nodeName string,
) []dto.RuntimePatchDTO {
	all := h.toRuntimePatchDTOs(data)
	if len(all) == 0 {
		return nil
	}

	out := make([]dto.RuntimePatchDTO, 0)
	for _, p := range all {
		if p.Node == nodeName {
			out = append(out, p)
		}
	}
	return out
}

func (h *RunInspectorHandler) buildRunNodeDiffDTO(
	ctx context.Context,
	task *domain.Task,
	rt *domain.NodeRuntime,
	nodeName string,
) *dto.RunNodeDiffDTO {
	if task == nil || rt == nil || task.ForkedFrom == nil {
		return nil
	}

	parentRT, err := h.nodeRuntimeRepo.FindByTaskIDAndNode(ctx, *task.ForkedFrom, nodeName)
	if err != nil || parentRT == nil {
		return nil
	}

	return &dto.RunNodeDiffDTO{
		BaseTaskID:     task.ForkedFrom,
		BaseNodeName:   nodeName,
		InputDiff:      diffFlatMap(parentRT.ResolvedInput, rt.ResolvedInput),
		OutputDiff:     diffFlatMap(parentRT.Output, rt.Output),
		CheckpointDiff: diffFlatMap(parentRT.Checkpoint, rt.Checkpoint),
		PlanDiff:       nil,
	}
}

func diffFlatMap(oldMap, newMap map[string]any) []dto.FieldDiffDTO {
	oldFlat := flattenMap("", oldMap)
	newFlat := flattenMap("", newMap)

	pathsMap := make(map[string]struct{}, len(oldFlat)+len(newFlat))
	for k := range oldFlat {
		pathsMap[k] = struct{}{}
	}
	for k := range newFlat {
		pathsMap[k] = struct{}{}
	}

	paths := make([]string, 0, len(pathsMap))
	for k := range pathsMap {
		paths = append(paths, k)
	}
	sort.Strings(paths)

	out := make([]dto.FieldDiffDTO, 0)
	for _, p := range paths {
		oldVal, oldOK := oldFlat[p]
		newVal, newOK := newFlat[p]

		switch {
		case !oldOK && newOK:
			out = append(out, dto.FieldDiffDTO{
				Path:     p,
				Change:   "added",
				NewValue: newVal,
			})
		case oldOK && !newOK:
			out = append(out, dto.FieldDiffDTO{
				Path:     p,
				Change:   "removed",
				OldValue: oldVal,
			})
		case !jsonValueEqual(oldVal, newVal):
			out = append(out, dto.FieldDiffDTO{
				Path:     p,
				Change:   "modified",
				OldValue: oldVal,
				NewValue: newVal,
			})
		}
	}

	return out
}

func flattenMap(prefix string, m map[string]any) map[string]any {
	out := map[string]any{}
	if m == nil {
		return out
	}

	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}

		child, ok := v.(map[string]any)
		if ok {
			for ck, cv := range flattenMap(path, child) {
				out[ck] = cv
			}
			continue
		}

		out[path] = v
	}

	return out
}

func jsonValueEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func (h *RunInspectorHandler) inferPreviewMode(task *domain.Task) string {
	if task != nil && task.ForkedFrom != nil {
		return "fork"
	}
	return "initial"
}

// 仅用于避免 toRunSummaryDTO 里空指针构造显得奇怪，后续 task 若补 CreatedAt/UpdatedAt 可删掉
type timeAlias struct{}

func (h *RunInspectorHandler) toDomainResumeSpec(req dto.PatchPreviewReq) *domain.ResumeSpec {
	patches := make([]domain.RuntimePatch, 0, len(req.Patches))
	for _, p := range req.Patches {
		patches = append(patches, domain.RuntimePatch{
			Target: domain.PatchTarget(p.Target),
			Node:   p.Node,
			Path:   p.Path,
			Op:     domain.PatchOp(p.Op),
			Value:  p.Value,
			Label:  p.Label,
		})
	}

	return &domain.ResumeSpec{
		ResumeFrom: req.ResumeFrom,
		Patches:    patches,
	}
}

func (h *RunInspectorHandler) toRunPlanPreviewDTO(ctx context.Context, plan *engine2.RunPlan) dto.RunPlanPreviewDTO {
	if plan == nil {
		return dto.RunPlanPreviewDTO{}
	}

	hasFailedChildren := h.collectFailedChildrenMap(ctx, plan)

	resp := dto.RunPlanPreviewDTO{
		Mode:         string(plan.Mode),
		ResumeFrom:   plan.ResumeFrom,
		ParentTaskID: plan.ParentTaskID,
		Summary:      h.toRunPlanPreviewSummaryDTO(plan),
		Nodes:        make([]dto.RunPlanNodePreviewDTO, 0, len(plan.TopoOrder)),
	}

	for _, nodeName := range plan.TopoOrder {
		np := plan.Nodes[nodeName]
		if np == nil {
			continue
		}

		item := dto.RunPlanNodePreviewDTO{
			Name:              np.Name,
			Label:             np.Label,
			Type:              string(np.NodeType),
			Action:            string(np.Action),
			Reason:            string(np.Reason),
			ReuseKind:         string(np.ReuseKind),
			IsPatched:         np.Action == engine2.PlanActionPatch,
			IsResumeBoundary:  plan.ResumeFrom != "" && np.Name == plan.ResumeFrom,
			HasFailedChildren: hasFailedChildren[np.Name],
			MapItemReuse:      h.mapItemReuseToSortedSlice(np.MapItemReuse),
		}

		resp.Nodes = append(resp.Nodes, item)
	}

	return resp
}

// collectFailedChildrenMap returns node names that have failed/canceled children.
// Only queries when plan has a parent task (fork/resume scenarios).
func (h *RunInspectorHandler) collectFailedChildrenMap(ctx context.Context, plan *engine2.RunPlan) map[string]bool {
	result := map[string]bool{}
	parentTaskID := plan.ParentTaskID
	if parentTaskID == nil || *parentTaskID == 0 {
		return result
	}

	for _, nodeName := range plan.TopoOrder {
		np := plan.Nodes[nodeName]
		if np == nil {
			continue
		}
		switch np.NodeType {
		case definition.NodeMap, definition.NodeLoop, definition.NodeSubWorkflow:
			children, err := h.taskRepo.ListByParentNode(ctx, *parentTaskID, nodeName)
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

func (h *RunInspectorHandler) mapItemReuseToSortedSlice(m map[int]bool) []int {
	if len(m) == 0 {
		return nil
	}

	out := make([]int, 0, len(m))
	for idx, ok := range m {
		if ok {
			out = append(out, idx)
		}
	}
	sort.Ints(out)
	return out
}

// RedoRun
//
// POST /runs/:id/redo
//
// 创建一个新的 fork run，并从 resume_from 开始局部重做。
func (h *RunInspectorHandler) RedoRun(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	var req dto.RunRedoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
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

	resumeSpec := &domain.ResumeSpec{
		ResumeFrom: req.ResumeFrom,
		Patches:    toDomainRuntimePatches(req.Patches),
	}

	editAction := req.EditAction
	if editAction == "" {
		editAction = "redo_partial"
	}

	newTask, err := h.redoService.RedoRun(
		ctx,
		taskID,
		resumeSpec,
		req.OverrideInput,
		req.EditAction, // "redo_partial"
		req.EditLabel,
		req.Note,
	)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	response.Success(c, dto.RunRedoResp{
		TaskID:       newTask.ID,
		Status:       string(newTask.Status),
		ParentTaskID: taskID,
		ResumeFrom:   req.ResumeFrom,
	})
}

func toDomainRuntimePatches(in []dto.RuntimePatchDTO) []domain.RuntimePatch {
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

func (h *RunInspectorHandler) toRunPlanPreviewSummaryDTO(plan *engine2.RunPlan) dto.RunPlanPreviewSummaryDTO {
	var summary dto.RunPlanPreviewSummaryDTO
	if plan == nil {
		return summary
	}

	for _, nodeName := range plan.TopoOrder {
		np := plan.Nodes[nodeName]
		if np == nil {
			continue
		}

		switch string(np.Action) {
		case "execute":
			summary.ExecuteCount++
		case "reuse":
			summary.ReuseCount++
		case "patch":
			summary.PatchCount++
		}

		if plan.ResumeFrom != "" && np.Name == plan.ResumeFrom {
			summary.ResumeBoundaryCount++
		}
	}

	return summary
}

// GetRunNodeExpansion
//
// GET /runs/:id/nodes/:node/expansion
//
// 第二版：
// - map: 返回 fanout/fanin 实例图
// - subworkflow: 返回子工作流定义图（后续再升级实例图）
func (h *RunInspectorHandler) GetRunNodeExpansion(c *gin.Context) {
	ctx := c.Request.Context()

	taskID, ok := h.parseTaskID(c)
	if !ok {
		return
	}

	nodeName := strings.TrimSpace(c.Param("node"))
	if nodeName == "" {
		response.Error(c, http.StatusBadRequest, "node is required")
		return
	}

	task, _, _, wfRuntime, runtimes, err := h.loadRunBundle(ctx, taskID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if !h.canAccessTask(c, task) {
		response.Error(c, http.StatusForbidden, "forbidden")
		return
	}

	defNode, ok := wfRuntime.Nodes()[nodeName]
	if !ok {
		response.Error(c, http.StatusBadRequest, "node definition not found")
		return
	}

	rtMap := make(map[string]*domain.NodeRuntime, len(runtimes))
	for _, rt := range runtimes {
		if rt != nil {
			rtMap[rt.Name] = rt
		}
	}
	parentRT := rtMap[nodeName]

	resp, err := h.buildRunNodeExpansionResp(ctx, task, defNode, parentRT)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	response.Success(c, resp)
}

func (h *RunInspectorHandler) buildRunNodeExpansionResp(
	ctx context.Context,
	task *domain.Task,
	parentNode nodes.Node,
	parentRT *domain.NodeRuntime,
) (*dto.RunNodeExpansionResp, error) {
	parentType := strings.ToLower(string(parentNode.Type))
	if parentType != "map" && parentType != "subworkflow" {
		return nil, fmt.Errorf("node %s is not expandable", parentNode.Name)
	}

	childWorkflowName := extractChildWorkflowName(parentNode)
	if childWorkflowName == "" {
		return nil, fmt.Errorf("node %s has no child workflow configured", parentNode.Name)
	}

	childDef, _, err := h.loadWorkflowDefinitionByName(ctx, childWorkflowName)
	if err != nil {
		return nil, err
	}

	childRuntime, err := h.builder.Build(childDef)
	if err != nil {
		return nil, err
	}

	switch parentType {
	case "map":
		return h.buildMapExpansionDTO(ctx, task, parentNode, parentRT, childWorkflowName, childRuntime)
	case "subworkflow":
		return h.buildSubworkflowExpansionDTO(ctx, task, parentNode, parentRT, childWorkflowName, childRuntime)
	default:
		return nil, fmt.Errorf("unsupported expansion type: %s", parentType)
	}
}

func (h *RunInspectorHandler) loadWorkflowDefinitionByName(
	ctx context.Context,
	workflowName string,
) (*definition.WorkflowDefinition, *domain.WorkflowVersion, error) {
	workflowName = strings.TrimSpace(workflowName)
	if workflowName == "" {
		return nil, nil, fmt.Errorf("workflow name is empty")
	}

	dbVersion, err := h.workflowVersionRepo.GetLatestByWorkflowName(ctx, workflowName)
	if err != nil {
		return nil, nil, fmt.Errorf("load workflow version by name %q failed: %w", workflowName, err)
	}
	if dbVersion == nil {
		return nil, nil, fmt.Errorf("workflow version not found for %q", workflowName)
	}

	var wfDef definition.WorkflowDefinition
	if err := json.Unmarshal(dbVersion.DefinitionJSON, &wfDef); err != nil {
		return nil, nil, fmt.Errorf("unmarshal workflow definition %q failed: %w", workflowName, err)
	}

	return &wfDef, dbVersion, nil
}

func (h *RunInspectorHandler) buildMapExpansionDTO(
	ctx context.Context,
	task *domain.Task,
	parentNode nodes.Node,
	parentRT *domain.NodeRuntime,
	childWorkflowName string,
	childRuntime workflow.Workflow,
) (*dto.RunNodeExpansionResp, error) {
	itemCount := h.inferMapItemCount(parentNode, parentRT)
	itemStates := h.inferMapItemStates(parentRT, itemCount)

	fanOutID := parentNode.Name + "::fanout"
	fanInID := parentNode.Name + "::fanin"

	builder := newExpansionGraphBuilder()
	groupsDTO := make([]dto.RunNodeExpansionGroupDTO, 0)
	itemsDTO := make([]dto.RunNodeExpansionItemDTO, 0, itemCount)

	// fan-out
	builder.addNode(dto.RunNodeExpansionNodeDTO{
		ID:              fanOutID,
		Name:            fanOutID,
		Title:           "Fan Out",
		Kind:            "virtual_fan_out",
		NodeType:        "virtual",
		State:           string(domain.NodeSuccess),
		Action:          "execute",
		Progress:        1,
		SourceNodeName:  "",
		ExecutionReason: "map fan-out",
		HasCheckpoint:   false,
		HasOutput:       false,
	})

	allNodeIDs := []string{fanOutID}

	for i := 0; i < itemCount; i++ {
		itemState := stateAt(itemStates, i)
		itemKey := strconv.Itoa(i)
		itemTitle := fmt.Sprintf("Item %d", i)

		itemLaneID := fmt.Sprintf("%s::item::%d", parentNode.Name, i)
		laneGroupID := fmt.Sprintf("group.%s.item.%d", parentNode.Name, i)

		itemsDTO = append(itemsDTO, dto.RunNodeExpansionItemDTO{
			ItemIndex:    i,
			ItemKey:      itemKey,
			DisplayTitle: itemTitle,
			State:        itemState,
			ChildRunID:   nil,
		})

		builder.addNode(dto.RunNodeExpansionNodeDTO{
			ID:              itemLaneID,
			Name:            itemLaneID,
			Title:           itemTitle,
			Kind:            "virtual_item",
			NodeType:        "virtual",
			State:           itemState,
			Action:          actionFromItemState(itemState),
			Progress:        progressFromState(itemState),
			SourceNodeName:  "",
			ExecutionReason: "map item lane",
			HasCheckpoint:   false,
			HasOutput:       false,
			ItemContext: &dto.RunNodeExpansionItemRefDTO{
				ItemIndex:    i,
				ItemKey:      itemKey,
				DisplayTitle: itemTitle,
			},
		})
		builder.appendGroupNode(laneGroupID, itemLaneID)
		allNodeIDs = append(allNodeIDs, itemLaneID)

		builder.addEdge(dto.RunNodeExpansionEdgeDTO{
			ID:          fmt.Sprintf("%s->%s", fanOutID, itemLaneID),
			FromNodeID:  fanOutID,
			ToNodeID:    itemLaneID,
			Kind:        "fan_out",
			IsActivated: true,
			Label:       itemTitle,
		})

		entryIDs, exitIDs, err := h.buildItemInstanceGraph(
			parentNode,
			i,
			itemState,
			itemKey,
			itemTitle,
			childRuntime,
			builder,
			laneGroupID,
		)
		if err != nil {
			return nil, err
		}

		for _, entryID := range entryIDs {
			builder.addEdge(dto.RunNodeExpansionEdgeDTO{
				ID:          fmt.Sprintf("%s->%s", itemLaneID, entryID),
				FromNodeID:  itemLaneID,
				ToNodeID:    entryID,
				Kind:        "item_flow",
				IsActivated: true,
			})
		}

		for _, exitID := range exitIDs {
			builder.addEdge(dto.RunNodeExpansionEdgeDTO{
				ID:          fmt.Sprintf("%s->%s", exitID, fanInID),
				FromNodeID:  exitID,
				ToNodeID:    fanInID,
				Kind:        "fan_in",
				IsActivated: true,
			})
		}

		groupsDTO = append(groupsDTO, dto.RunNodeExpansionGroupDTO{
			ID:      laneGroupID,
			Title:   itemTitle,
			Kind:    "item_lane",
			NodeIDs: uniqueStrings(builder.groupNodes[laneGroupID]),
		})
	}

	finalState := aggregateItemStates(itemStates)

	builder.addNode(dto.RunNodeExpansionNodeDTO{
		ID:              fanInID,
		Name:            fanInID,
		Title:           "Fan In",
		Kind:            "virtual_fan_in",
		NodeType:        "virtual",
		State:           finalState,
		Action:          "execute",
		Progress:        progressFromState(finalState),
		SourceNodeName:  "",
		ExecutionReason: "map fan-in",
		HasCheckpoint:   false,
		HasOutput:       false,
	})
	allNodeIDs = append(allNodeIDs, fanInID)

	nodesDTO := builder.nodeSlice()
	for _, n := range nodesDTO {
		if n.ID != fanOutID && n.ID != fanInID && !containsString(allNodeIDs, n.ID) {
			allNodeIDs = append(allNodeIDs, n.ID)
		}
	}

	groupsDTO = append([]dto.RunNodeExpansionGroupDTO{
		{
			ID:      "group." + parentNode.Name,
			Title:   parentNode.Name,
			Kind:    "map",
			NodeIDs: uniqueStrings(allNodeIDs),
		},
	}, groupsDTO...)

	_ = ctx
	_ = task

	return &dto.RunNodeExpansionResp{
		ParentNodeName:    parentNode.Name,
		Kind:              "map",
		ChildWorkflowName: childWorkflowName,
		ChildRunID:        nil,
		ItemCount:         intPtr(itemCount),
		Items:             itemsDTO,
		Nodes:             nodesDTO,
		Edges:             builder.edgeSlice(),
		Groups:            groupsDTO,
	}, nil
}

func (h *RunInspectorHandler) buildSubworkflowExpansionDTO(
	ctx context.Context,
	task *domain.Task,
	parentNode nodes.Node,
	parentRT *domain.NodeRuntime,
	childWorkflowName string,
	childRuntime workflow.Workflow,
) (*dto.RunNodeExpansionResp, error) {
	nodesDTO := make([]dto.RunNodeExpansionNodeDTO, 0, len(childRuntime.Order()))
	edgesDTO := make([]dto.RunNodeExpansionEdgeDTO, 0)
	allNodeIDs := make([]string, 0, len(childRuntime.Order()))

	state := string(domain.NodePending)
	if parentRT != nil {
		state = string(parentRT.State)
	}

	for _, nodeName := range childRuntime.Order() {
		defNode, ok := childRuntime.Nodes()[nodeName]
		if !ok {
			continue
		}

		nodesDTO = append(nodesDTO, dto.RunNodeExpansionNodeDTO{
			ID:               nodeName,
			Name:             nodeName,
			Title:            nodeName,
			Kind:             h.toExpansionNodeKind(defNode),
			NodeType:         string(defNode.Type),
			State:            state,
			Action:           "execute",
			Progress:         progressFromState(state),
			SourceNodeName:   nodeName,
			ExecutionReason:  "subworkflow definition expansion",
			ReuseKind:        "",
			IsInjected:       false,
			IsDirty:          false,
			IsPatched:        false,
			IsResumeBoundary: false,
			HasCheckpoint:    false,
			HasOutput:        false,
		})
		allNodeIDs = append(allNodeIDs, nodeName)
	}

	for _, from := range childRuntime.Order() {
		for _, e := range childRuntime.Graph().Edges[from] {
			kind := "normal"
			if e.Condition != "" || e.CaseKey != "" || e.Type == definition.EdgeCondition {
				kind = "condition"
			}

			edgesDTO = append(edgesDTO, dto.RunNodeExpansionEdgeDTO{
				ID:          fmt.Sprintf("%s->%s", from, e.To),
				FromNodeID:  from,
				ToNodeID:    e.To,
				Kind:        kind,
				IsActivated: true,
				Label:       edgeLabel(e),
				Condition:   e.Condition,
				CaseKey:     e.CaseKey,
				Priority:    e.Priority,
			})
		}
	}

	return &dto.RunNodeExpansionResp{
		ParentNodeName:    parentNode.Name,
		Kind:              "subworkflow",
		ChildWorkflowName: childWorkflowName,
		ChildRunID:        nil,
		ItemCount:         nil,
		Items:             nil,
		Nodes:             nodesDTO,
		Edges:             edgesDTO,
		Groups: []dto.RunNodeExpansionGroupDTO{
			{
				ID:      "group." + parentNode.Name,
				Title:   parentNode.Name,
				Kind:    "subworkflow",
				NodeIDs: allNodeIDs,
			},
		},
	}, nil
}

func extractChildWorkflowName(parentNode nodes.Node) string {
	raw, ok := parentNode.Config["workflow"]
	if !ok || raw == nil {
		return ""
	}

	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (h *RunInspectorHandler) inferMapItemCount(
	parentNode nodes.Node,
	rt *domain.NodeRuntime,
) int {
	if rt == nil {
		return 0
	}

	// 1. checkpoint.item_count
	if rt.Checkpoint != nil {
		if raw, ok := rt.Checkpoint["item_count"]; ok {
			if n := toInt(raw); n > 0 {
				return n
			}
		}
	}

	// 2. checkpoint.items
	if rt.Checkpoint != nil {
		if raw, ok := rt.Checkpoint["items"]; ok {
			if arr, ok := raw.([]any); ok {
				return len(arr)
			}
		}
	}

	// 3. output.results
	if rt.Output != nil {
		if raw, ok := rt.Output["results"]; ok {
			if arr, ok := raw.([]any); ok {
				return len(arr)
			}
		}
	}

	// 4. resolved_input 上按 map config.items 路径取值
	itemsPath := extractMapItemsPath(parentNode)
	if itemsPath != "" && rt.ResolvedInput != nil {
		if raw, ok := getValueByPath(rt.ResolvedInput, itemsPath); ok {
			if arr, ok := raw.([]any); ok {
				return len(arr)
			}
		}
	}

	// 5. reused_items 最大索引回推
	reused := h.extractMapItemReuseIndexes(rt)
	if len(reused) > 0 {
		return reused[len(reused)-1] + 1
	}

	return 0
}

func (h *RunInspectorHandler) inferMapItemStates(
	rt *domain.NodeRuntime,
	itemCount int,
) []string {
	if itemCount <= 0 {
		return nil
	}

	out := make([]string, itemCount)
	defaultState := string(domain.NodePending)
	if rt != nil && rt.State != "" {
		defaultState = string(rt.State)
	}

	for i := 0; i < itemCount; i++ {
		out[i] = defaultState
	}

	if rt == nil || rt.Checkpoint == nil {
		return out
	}

	// checkpoint.item_states
	if raw, ok := rt.Checkpoint["item_states"]; ok {
		if m, ok := raw.(map[string]any); ok {
			for k, v := range m {
				idx, err := strconv.Atoi(k)
				if err != nil || idx < 0 || idx >= itemCount {
					continue
				}
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					out[idx] = s
				}
			}
		}
	}

	// reused_items 覆盖成 reuse/success
	if raw, ok := rt.Checkpoint["reused_items"]; ok {
		if m, ok := raw.(map[string]any); ok {
			for k, v := range m {
				idx, err := strconv.Atoi(k)
				if err != nil || idx < 0 || idx >= itemCount {
					continue
				}
				b, ok := v.(bool)
				if ok && b {
					out[idx] = "reuse"
				}
			}
		}
	}

	return out
}

func (h *RunInspectorHandler) toExpansionNodesDTO(
	wfRuntime workflow.Workflow,
) []dto.RunNodeExpansionNodeDTO {
	out := make([]dto.RunNodeExpansionNodeDTO, 0, len(wfRuntime.Order()))

	for _, nodeName := range wfRuntime.Order() {
		defNode, ok := wfRuntime.Nodes()[nodeName]
		if !ok {
			continue
		}

		out = append(out, dto.RunNodeExpansionNodeDTO{
			ID:               nodeName,
			Name:             nodeName,
			Title:            nodeName,
			Kind:             h.toExpansionNodeKind(defNode),
			NodeType:         string(defNode.Type),
			State:            string(domain.NodePending),
			Action:           "execute",
			Progress:         0,
			SourceNodeName:   nodeName,
			ExecutionReason:  "",
			ReuseKind:        "",
			IsInjected:       false,
			IsDirty:          false,
			IsPatched:        false,
			IsResumeBoundary: false,
			HasCheckpoint:    false,
			HasOutput:        false,
			InputHash:        "",
			OutputHash:       "",
			ItemContext:      nil,
		})
	}

	return out
}

func (h *RunInspectorHandler) toExpansionEdgesDTO(
	wfRuntime workflow.Workflow,
) []dto.RunNodeExpansionEdgeDTO {
	out := make([]dto.RunNodeExpansionEdgeDTO, 0)

	for _, from := range wfRuntime.Order() {
		for _, e := range wfRuntime.Graph().Edges[from] {
			kind := "normal"
			if e.Condition != "" || e.CaseKey != "" || e.Type == definition.EdgeCondition {
				kind = "condition"
			}

			edgeID := from + "->" + e.To
			out = append(out, dto.RunNodeExpansionEdgeDTO{
				ID:          edgeID,
				FromNodeID:  from,
				ToNodeID:    e.To,
				Kind:        kind,
				IsActivated: false,
				Label:       edgeLabel(e),
				Condition:   e.Condition,
				CaseKey:     e.CaseKey,
				Priority:    e.Priority,
			})
		}
	}

	return out
}

func edgeLabel(e definition.EdgeDefinition) string {
	if e.Label != "" {
		return e.Label
	}
	if e.CaseKey != "" {
		return e.CaseKey
	}
	if e.Condition != "" {
		return e.Condition
	}
	return ""
}

func (h *RunInspectorHandler) toExpansionGroupsDTO(
	wfRuntime workflow.Workflow,
	parentNodeName string,
	parentType string,
) []dto.RunNodeExpansionGroupDTO {
	nodeIDs := make([]string, 0, len(wfRuntime.Order()))
	for _, nodeName := range wfRuntime.Order() {
		nodeIDs = append(nodeIDs, nodeName)
	}

	groupKind := "workflow"
	if parentType == "map" {
		groupKind = "map"
	} else if parentType == "subworkflow" {
		groupKind = "subworkflow"
	}

	return []dto.RunNodeExpansionGroupDTO{
		{
			ID:      "group." + parentNodeName,
			Title:   parentNodeName,
			Kind:    groupKind,
			NodeIDs: nodeIDs,
		},
	}
}

func (h *RunInspectorHandler) toExpansionNodeKind(defNode nodes.Node) string {
	switch strings.ToLower(string(defNode.Type)) {
	case "start":
		return "start"
	case "end":
		return "end"
	case "tool":
		return "tool"
	case "map":
		return "map"
	case "subworkflow":
		return "subworkflow"
	default:
		return "tool"
	}
}

// 构建单个 item lane 的 branch-aware 图
func (h *RunInspectorHandler) buildItemInstanceGraph(
	parentNode nodes.Node,
	itemIndex int,
	itemState string,
	itemKey string,
	itemTitle string,
	childRuntime workflow.Workflow,
	builder *expansionGraphBuilder,
	laneGroupID string,
) (entryIDs []string, exitIDs []string, err error) {
	adj := buildWorkflowAdjacency(childRuntime)
	entryNodes := findWorkflowEntryNodes(childRuntime, adj)
	if len(entryNodes) == 0 {
		return nil, nil, nil
	}

	type visitState struct {
		sourceNode  string
		branchScope string
	}

	visited := make(map[visitState]bool)
	var exits []string

	var walk func(nodeName string, branchScope string, stopAt string) (string, []string, error)
	walk = func(nodeName string, branchScope string, stopAt string) (string, []string, error) {
		stateKey := visitState{sourceNode: nodeName, branchScope: branchScope}
		if visited[stateKey] {
			clonedID := buildExpansionCloneID(parentNode.Name, itemIndex, branchScope, nodeName)
			return clonedID, []string{clonedID}, nil
		}
		visited[stateKey] = true

		defNode, ok := childRuntime.Nodes()[nodeName]
		if !ok {
			return "", nil, fmt.Errorf("child node %s not found", nodeName)
		}

		clonedID := buildExpansionCloneID(parentNode.Name, itemIndex, branchScope, nodeName)

		builder.addNode(dto.RunNodeExpansionNodeDTO{
			ID:               clonedID,
			Name:             clonedID,
			Title:            nodeName,
			Kind:             h.toExpansionNodeKind(defNode),
			NodeType:         string(defNode.Type),
			State:            itemState,
			Action:           actionFromItemState(itemState),
			Progress:         progressFromState(itemState),
			SourceNodeName:   nodeName,
			ExecutionReason:  "map item instance",
			ReuseKind:        "",
			IsInjected:       false,
			IsDirty:          false,
			IsPatched:        false,
			IsResumeBoundary: false,
			HasCheckpoint:    false,
			HasOutput:        false,
			ItemContext: &dto.RunNodeExpansionItemRefDTO{
				ItemIndex:    itemIndex,
				ItemKey:      itemKey,
				DisplayTitle: itemTitle,
			},
		})
		builder.appendGroupNode(laneGroupID, clonedID)

		// 到达 branch stop boundary，不再向后展开
		if stopAt != "" && nodeName == stopAt {
			return clonedID, []string{clonedID}, nil
		}

		outgoing := adj.outgoing[nodeName]
		if len(outgoing) == 0 {
			return clonedID, []string{clonedID}, nil
		}

		// branch fork：为每个 branch 单独复制到 merge boundary
		if isBranchFork(adj, nodeName) {
			mergeNode := findMergeBoundary(childRuntime, adj, nodeName)

			branchExits := make([]string, 0)
			for idx, e := range outgoing {
				branchLabel := e.CaseKey
				if branchLabel == "" {
					branchLabel = e.Label
				}
				if branchLabel == "" {
					branchLabel = fmt.Sprintf("branch%d", idx)
				}
				branchLabel = sanitizeBranchScope(branchLabel)

				childBranchScope := branchLabel
				if branchScope != "" {
					childBranchScope = branchScope + "." + branchLabel
				}

				childID, childExits, err := walk(e.To, childBranchScope, mergeNode)
				if err != nil {
					return "", nil, err
				}

				builder.addEdge(dto.RunNodeExpansionEdgeDTO{
					ID:          fmt.Sprintf("%s->%s", clonedID, childID),
					FromNodeID:  clonedID,
					ToNodeID:    childID,
					Kind:        edgeKindFromDef(e),
					IsActivated: true,
					Label:       edgeLabel(e),
					Condition:   e.Condition,
					CaseKey:     e.CaseKey,
					Priority:    e.Priority,
				})

				branchExits = append(branchExits, childExits...)
			}

			// merge boundary 之后继续接共享主干
			if mergeNode != "" {
				mergeCloneID := buildExpansionCloneID(parentNode.Name, itemIndex, "", mergeNode)

				// 为每个 branch shadow merge -> shared merge
				for _, branchExitID := range branchExits {
					builder.addEdge(dto.RunNodeExpansionEdgeDTO{
						ID:          fmt.Sprintf("%s->%s", branchExitID, mergeCloneID),
						FromNodeID:  branchExitID,
						ToNodeID:    mergeCloneID,
						Kind:        "virtual",
						IsActivated: true,
					})
				}

				// 如果 shared merge 还没展开，则继续展开共享主干
				if !visited[visitState{sourceNode: mergeNode, branchScope: ""}] {
					_, mergeExits, err := walk(mergeNode, "", "")
					if err != nil {
						return "", nil, err
					}
					return clonedID, mergeExits, nil
				}

				return clonedID, []string{mergeCloneID}, nil
			}

			return clonedID, branchExits, nil
		}

		// 普通线性/单出边
		if len(outgoing) == 1 {
			e := outgoing[0]
			childID, childExits, err := walk(e.To, branchScope, stopAt)
			if err != nil {
				return "", nil, err
			}

			builder.addEdge(dto.RunNodeExpansionEdgeDTO{
				ID:          fmt.Sprintf("%s->%s", clonedID, childID),
				FromNodeID:  clonedID,
				ToNodeID:    childID,
				Kind:        edgeKindFromDef(e),
				IsActivated: true,
				Label:       edgeLabel(e),
				Condition:   e.Condition,
				CaseKey:     e.CaseKey,
				Priority:    e.Priority,
			})

			return clonedID, childExits, nil
		}

		// 多出边但不是条件分支，按普通多出边处理
		var multiExits []string
		for _, e := range outgoing {
			childID, childExits, err := walk(e.To, branchScope, stopAt)
			if err != nil {
				return "", nil, err
			}

			builder.addEdge(dto.RunNodeExpansionEdgeDTO{
				ID:          fmt.Sprintf("%s->%s", clonedID, childID),
				FromNodeID:  clonedID,
				ToNodeID:    childID,
				Kind:        edgeKindFromDef(e),
				IsActivated: true,
				Label:       edgeLabel(e),
				Condition:   e.Condition,
				CaseKey:     e.CaseKey,
				Priority:    e.Priority,
			})
			multiExits = append(multiExits, childExits...)
		}

		return clonedID, multiExits, nil
	}

	var allEntryIDs []string
	for _, entry := range entryNodes {
		entryID, entryExits, err := walk(entry, "", "")
		if err != nil {
			return nil, nil, err
		}
		allEntryIDs = append(allEntryIDs, entryID)
		exits = append(exits, entryExits...)
	}

	return allEntryIDs, uniqueStrings(exits), nil
}
