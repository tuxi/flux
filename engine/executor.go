package engine

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"flux/cost"
	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
	"flux/runtimekeys"
	"flux/tool"
	"flux/workflow"
	"flux/workflow/nodes"
)

// 执行节点的统一入口
// executeNode 执行节点统一入口
func (e *Engine) executeNode(
	runCtx *nodes.Context,
	node nodes.Node,
	g *graph.Graph,
) error {
	e.mu.RLock()
	runtime := runCtx.Runtime[node.Name]
	e.mu.RUnlock()

	// 1. build input
	inputs, err := e.buildNodeInput(runCtx, node, g)
	if err != nil {
		return err
	}

	// 2. validate
	if err := e.validateNodeInput(node, inputs); err != nil {
		return err
	}

	// 3. snapshot
	runtime.ResolvedInput = deepCloneMap(inputs)

	// 4. hash
	hash := runCtx.CalculateInputHash(
		fmt.Sprintf("%d-%s", runCtx.Task.WorkflowVersionID, node.Name),
		inputs,
	)

	// 🔥 关键变化：基于 ExecutionReason 决定是否强制执行
	forceExecute := isExecutionRequired(runtime.ExecutionReason)

	// 5. 快速路径（只有 pure reuse 才允许跳过）
	if !forceExecute &&
		runtime.State == domain.NodeSuccess &&
		runtime.InputHash == hash {
		return nil
	}

	// 6. reset runtime
	runtime.InputHash = hash
	runtime.Output = nil
	runtime.OutputHash = ""
	runtime.ActivatedEdges = map[string]bool{}
	runtime.Error = ""

	// 重新执行前，把本节点 outgoing edge 从全局 ctx.ActivatedEdges 清掉，
	// 防止上一次执行残留的 true/false 标志在 finalize 之前被下游 depsMet 读到。
	e.clearOutgoingActivatedEdges(runCtx, node.Name)

	if err := e.nodeRepo.Update(runCtx.Ctx, runtime); err != nil {
		return err
	}

	execCtx := &nodes.NodeExecContext{
		TaskContext: runCtx,
		Input:       inputs,
		Output:      map[string]any{},
		NodeDef: &definition.NodeDefinition{
			Name:         node.Name,
			Label:        node.Label,
			Type:         node.Type,
			Version:      node.Version,
			InputMapping: node.InputMapping,
			Config:       node.Config,
		},
		Executor: e,
	}

	// ==== SubWorkflow ====
	if node.Type == definition.NodeSubWorkflow {
		e.mu.Lock()
		e.transitionLocked(runCtx, runtime, domain.NodeRunning, nil, nil)
		e.mu.Unlock()

		subWorkflowName := node.Config["workflow"].(string)
		result, err := e.RunSubWorkflow(
			execCtx,
			subWorkflowName,
			inputs,
		)
		if err != nil {
			var suspendErr *domain.WorkflowSuspendedError
			if e.enableSubWorkflowBinding && errors.As(err, &suspendErr) {
				// P1：把"父节点等子任务"建模成 await binding，父节点落 NodeAwaiting，
				// 退出 heartbeat scanner，完成路径统一走 CompleteAwaitNode（见 event_listen.go）。
				// 此分支只服务 plain subworkflow 节点（map/loop 在各自 Step 内部调用 RunSubWorkflow）。
				e.bindSubWorkflowAwaitOnSuspend(execCtx, subWorkflowName, inputs)
			}
			return err
		}

		if err := runCtx.SetNodeOutput(node.Name, result, node.Step.OutputSchema()); err != nil {
			return err
		}

		runtime.OutputHash = runCtx.CalculateOutputHash(result)
		if shouldPersistOutput(node) {
			runtime.Output = result
		}

		clearNodeReuseMetadata(runtime)
		return e.nodeRepo.Update(runCtx.Ctx, runtime)
	}

	// ==== running ====
	e.mu.Lock()
	e.transitionLocked(runCtx, runtime, domain.NodeRunning, nil, nil)
	e.mu.Unlock()

	// ==== async ====
	if node.Step.Mode() == tool.AsyncExecution {
		return e.scheduleAsyncActivity(runCtx, node, execCtx, hash)
	}

	// ==== await ====
	if node.Type == definition.NodeAwait {
		return e.executeAwaitNode(runCtx, runtime, node, execCtx)
	}

	// ==== sync ====
	if err := e.runNodeWithHeartbeat(execCtx, node); err != nil {
		return err
	}

	if err := runCtx.SetNodeOutput(node.Name, execCtx.Output, node.Step.OutputSchema()); err != nil {
		return err
	}

	runtime.OutputHash = runCtx.CalculateOutputHash(execCtx.Output)
	if shouldPersistOutput(node) {
		runtime.Output = execCtx.Output
	}

	clearNodeReuseMetadata(runtime)

	return e.nodeRepo.Update(runCtx.Ctx, runtime)
}

// subWorkflowReconcileInterval 是 subworkflow await binding 的对账周期（P2 的 poll 兜底使用）。
// P1 仅写入 NextPollAt，暂无 poller 消费它（processPollBinding 要求 fallback tool，processTimeoutBinding
// 要求 TimeoutAt，二者本 binding 都不设），因此对现有 AwaitPollWorker 完全无副作用。
const subWorkflowReconcileInterval = 45 * time.Second

// bindSubWorkflowAwaitOnSuspend 在 plain subworkflow 父节点挂起等待子任务时：
//  1. 按 sub_key 找到刚创建/复用的子任务；
//  2. upsert 一条 AwaitTypeSubWorkflow 的 binding（Correlation 记录 child_task_id/sub_key）；
//  3. 把父节点从 NodeRunning 迁到 NodeAwaiting —— 从而退出 FindExpiredRunningNodes（不再被
//     heartbeat scanner 误判为 crash），完成路径改由 CompleteAwaitNode 统一处理。
//
// 仅在 enableSubWorkflowBinding 开启时调用；任何异常都回退（保持 NodeRunning，由 scanner 兜底），
// 保证不因绑定失败而把任务卡死。
func (e *Engine) bindSubWorkflowAwaitOnSuspend(execCtx *nodes.NodeExecContext, workflowName string, input map[string]any) {
	runCtx := execCtx.TaskContext
	nodeName := execCtx.NodeDef.Name

	subKey := runtimekeys.BuildSubWorkflowKey(runCtx.Task.ID, nodeName, workflowName, input)
	child, err := e.taskRepo.FindBySubKey(runCtx.Ctx, subKey)
	if err != nil || child == nil {
		log.Printf("subworkflow bind await: child not found, fallback to NodeRunning: task=%d node=%s err=%v", runCtx.Task.ID, nodeName, err)
		return
	}

	if err := e.upsertSubWorkflowBinding(runCtx, nodeName, child.ID, subKey); err != nil {
		log.Printf("subworkflow bind await: upsert binding failed, fallback to NodeRunning: task=%d node=%s err=%v", runCtx.Task.ID, nodeName, err)
		return
	}

	e.mu.Lock()
	rt := runCtx.Runtime[nodeName]
	if rt != nil && rt.State == domain.NodeRunning {
		e.transitionLocked(runCtx, rt, domain.NodeAwaiting, nil, nil)
	}
	e.mu.Unlock()
}

// upsertSubWorkflowBinding 为 (父任务, 父节点) upsert 一条 subworkflow await binding。
// 一节点一 binding（GetByTaskAndNode 复用）。Correlation 记录子任务链接信息供 P2 poll 对账。
func (e *Engine) upsertSubWorkflowBinding(runCtx *nodes.Context, nodeName string, childID int64, subKey string) error {
	if e.awaitBindingRepo == nil {
		return fmt.Errorf("await binding repository is nil")
	}
	ctx := runCtx.Ctx
	correlation := map[string]any{
		// 用字符串存 child_task_id：Correlation 以 JSON 持久化，雪花 int64 经 float64 反序列化会丢精度。
		"child_task_id": strconv.FormatInt(childID, 10),
		"sub_key":       subKey,
	}

	existing, err := e.awaitBindingRepo.GetByTaskAndNode(ctx, runCtx.Task.ID, nodeName)
	if err == nil && existing != nil {
		next := time.Now().Add(subWorkflowReconcileInterval)
		existing.Correlation = correlation
		existing.NextPollAt = &next
		// 子任务被复活（Fix 4）等场景下 binding 可能已是终态，重新置为 waiting 等待新一轮完成。
		if isAwaitBindingTerminalStatus(existing.Status) {
			existing.Status = domain.AwaitBindingWaiting
			existing.ErrorMessage = ""
			existing.CompletedAt = nil
			existing.FailedAt = nil
			existing.CanceledAt = nil
		}
		return e.awaitBindingRepo.Update(ctx, existing)
	}

	now := time.Now()
	next := now.Add(subWorkflowReconcileInterval)
	binding := &domain.AwaitBinding{
		TaskID:            runCtx.Task.ID,
		RootTaskID:        runCtx.Task.RootID,
		NodeName:          nodeName,
		WorkflowVersionID: runCtx.Task.WorkflowVersionID,
		AwaitType:         domain.AwaitTypeSubWorkflow,
		Source:            domain.AwaitSourceSubWorkflow,
		Status:            domain.AwaitBindingPending,
		Correlation:       correlation,
		WaitingStartedAt:  &now,
		NextPollAt:        &next,
	}
	if err := e.awaitBindingRepo.Create(ctx, binding); err != nil {
		return err
	}
	moved, err := e.awaitBindingRepo.TransitionStatus(ctx, binding.ID, domain.AwaitBindingPending, domain.AwaitBindingWaiting)
	if err != nil {
		return err
	}
	if !moved {
		return fmt.Errorf("subworkflow binding transition pending->waiting failed: binding=%d", binding.ID)
	}
	return nil
}

func isAwaitBindingTerminalStatus(status domain.AwaitBindingStatus) bool {
	switch status {
	case domain.AwaitBindingCompleted,
		domain.AwaitBindingFailed,
		domain.AwaitBindingTimedOut,
		domain.AwaitBindingCanceled:
		return true
	default:
		return false
	}
}

func (e *Engine) executeAwaitNode(
	runCtx *nodes.Context,
	runtime *domain.NodeRuntime,
	node nodes.Node,
	execCtx *nodes.NodeExecContext,
) error {
	if e.awaitBindingRepo == nil {
		return fmt.Errorf("await binding repository is nil")
	}

	binding, err := e.buildAwaitBinding(runCtx, node, execCtx)
	if err != nil {
		return err
	}

	existing, err := e.awaitBindingRepo.GetByTaskAndNode(runCtx.Ctx, runCtx.Task.ID, node.Name)
	if err == nil && existing != nil {
		if existing.Status == domain.AwaitBindingWaiting ||
			existing.Status == domain.AwaitBindingPending ||
			existing.Status == domain.AwaitBindingCompleting {
			e.transitionLocked(runCtx, runtime, domain.NodeAwaiting, nil, nil)
			return &domain.WorkflowSuspendedError{Reason: domain.SuspendAsyncNode}
		}
	}

	now := time.Now()
	binding.WaitingStartedAt = &now
	if binding.FallbackPollEnabled {
		nextPollAt := now.Add(awaitFallbackStartAfter(node.Config["fallback_poll"]))
		binding.NextPollAt = &nextPollAt
	}
	binding.Status = domain.AwaitBindingPending

	if existing != nil {
		resetAwaitBindingForRetry(existing, binding)
		if err := e.awaitBindingRepo.Update(runCtx.Ctx, existing); err != nil {
			return err
		}
		binding = existing
	} else {
		if err := e.awaitBindingRepo.Create(runCtx.Ctx, binding); err != nil {
			return err
		}
	}
	moved, err := e.awaitBindingRepo.TransitionStatus(runCtx.Ctx, binding.ID, domain.AwaitBindingPending, domain.AwaitBindingWaiting)
	if err != nil {
		return err
	}
	if !moved {
		return fmt.Errorf("await binding transition failed: pending -> waiting, binding=%d", binding.ID)
	}
	binding.Status = domain.AwaitBindingWaiting

	e.transitionLocked(runCtx, runtime, domain.NodeAwaiting, nil, nil)

	return &domain.WorkflowSuspendedError{Reason: domain.SuspendAsyncNode}
}

func resetAwaitBindingForRetry(dst *domain.AwaitBinding, src *domain.AwaitBinding) {
	if dst == nil || src == nil {
		return
	}

	dst.TaskID = src.TaskID
	dst.RootTaskID = src.RootTaskID
	dst.NodeName = src.NodeName
	dst.WorkflowVersionID = src.WorkflowVersionID
	dst.AwaitType = src.AwaitType
	dst.Source = src.Source
	dst.Status = domain.AwaitBindingPending
	dst.Provider = src.Provider
	dst.ProviderTaskID = src.ProviderTaskID
	dst.APITaskID = src.APITaskID
	dst.ExternalTaskID = src.ExternalTaskID
	dst.SignalName = src.SignalName
	dst.MessageName = src.MessageName
	dst.CallbackToken = src.CallbackToken
	dst.Correlation = deepCloneMap(src.Correlation)
	dst.Config = deepCloneMap(src.Config)
	dst.LastEventID = nil
	dst.LastEventSource = nil
	dst.LastEventPayload = nil
	dst.ResultPayload = nil
	dst.ErrorMessage = ""
	dst.FallbackPollEnabled = src.FallbackPollEnabled
	dst.FallbackPollTool = src.FallbackPollTool
	dst.PollAttempts = 0
	dst.MaxPollAttempts = src.MaxPollAttempts
	dst.LastPolledAt = nil
	dst.NextPollAt = src.NextPollAt
	dst.WaitingStartedAt = src.WaitingStartedAt
	dst.TimeoutAt = src.TimeoutAt
	dst.CompletedAt = nil
	dst.FailedAt = nil
	dst.CanceledAt = nil
}

func (e *Engine) buildAwaitBinding(
	runCtx *nodes.Context,
	node nodes.Node,
	execCtx *nodes.NodeExecContext,
) (*domain.AwaitBinding, error) {
	awaitType := stringValue(node.Config["await_type"])
	source := stringValue(node.Config["source"])
	if awaitType == "" {
		return nil, fmt.Errorf("await node missing await_type")
	}
	if source == "" {
		return nil, fmt.Errorf("await node missing source")
	}

	configSnapshot := deepCloneMap(node.Config)
	correlation := resolveAwaitCorrelation(execCtx.Input, node.Config["correlation"])

	binding := &domain.AwaitBinding{
		TaskID:              runCtx.Task.ID,
		RootTaskID:          runCtx.Task.RootID,
		NodeName:            node.Name,
		WorkflowVersionID:   runCtx.Task.WorkflowVersionID,
		AwaitType:           domain.AwaitType(awaitType),
		Source:              domain.AwaitSource(source),
		Status:              domain.AwaitBindingPending,
		Correlation:         correlation,
		Config:              configSnapshot,
		FallbackPollEnabled: awaitFallbackEnabled(node.Config["fallback_poll"]),
		FallbackPollTool:    awaitFallbackTool(node.Config["fallback_poll"]),
		MaxPollAttempts:     awaitFallbackMaxAttempts(node.Config["fallback_poll"]),
		Provider:            optionalStringPtr(resolveAwaitValue(execCtx.Input, node.Config["provider"])),
		SignalName:          optionalStringPtr(node.Config["signal_name"]),
		CallbackToken:       optionalStringPtr(resolveAwaitCallbackToken(execCtx.Input, node.Config["callback_token_expr"])),
	}
	if timeoutSeconds := intValue(node.Config["timeout_seconds"]); timeoutSeconds > 0 {
		timeoutAt := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
		binding.TimeoutAt = &timeoutAt
	}
	if v := firstNonEmptyString(correlation["provider_task_id"]); v != "" {
		binding.ProviderTaskID = optionalStringPtr(v)
	}
	if v := firstNonEmptyString(correlation["api_task_id"]); v != "" {
		binding.APITaskID = optionalStringPtr(v)
	}
	if v := firstNonEmptyString(correlation["external_task_id"]); v != "" {
		binding.ExternalTaskID = optionalStringPtr(v)
	}
	return binding, nil
}

func resolveAwaitCorrelation(input map[string]any, raw any) map[string]any {
	out := map[string]any{}
	cfg, ok := raw.(map[string]any)
	if !ok || cfg == nil {
		return out
	}
	for key, value := range cfg {
		switch v := value.(type) {
		case string:
			if resolved, ok := input[v]; ok {
				out[key] = resolved
			} else {
				out[key] = v
			}
		default:
			out[key] = v
		}
	}
	return out
}

func resolveAwaitCallbackToken(input map[string]any, raw any) string {
	tokenExpr := stringValue(raw)
	if tokenExpr == "" {
		return ""
	}
	if v, ok := input[tokenExpr]; ok {
		return stringValue(v)
	}
	return tokenExpr
}

func resolveAwaitValue(input map[string]any, raw any) string {
	valueExpr := stringValue(raw)
	if valueExpr == "" {
		return ""
	}
	if v, ok := input[valueExpr]; ok {
		return stringValue(v)
	}
	return valueExpr
}

func awaitFallbackEnabled(raw any) bool {
	cfg, ok := raw.(map[string]any)
	if !ok || cfg == nil {
		return false
	}
	if enabled, ok := cfg["enabled"].(bool); ok {
		return enabled
	}
	return false
}

func awaitFallbackTool(raw any) *string {
	cfg, ok := raw.(map[string]any)
	if !ok || cfg == nil {
		return nil
	}
	return optionalStringPtr(cfg["tool"])
}

func awaitFallbackMaxAttempts(raw any) int {
	cfg, ok := raw.(map[string]any)
	if !ok || cfg == nil {
		return 0
	}
	return intValue(cfg["max_attempts"])
}

func awaitFallbackStartAfter(raw any) time.Duration {
	cfg, ok := raw.(map[string]any)
	if !ok || cfg == nil {
		return time.Minute
	}
	if duration, ok := parseDurationValue(cfg["start_after"]); ok && duration >= 0 {
		return duration
	}
	if duration, ok := parseDurationValue(cfg["interval"]); ok && duration > 0 {
		return duration
	}
	return time.Minute
}

func parseDurationValue(raw any) (time.Duration, bool) {
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
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func optionalStringPtr(raw any) *string {
	value := stringValue(raw)
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(raw any) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
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
	default:
		return 0
	}
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		s := stringValue(value)
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func isExecutionRequired(reason string) bool {
	switch ExecutionReason(reason) {
	case ExecutionReasonResumeBoundary,
		ExecutionReasonUpstreamDirty,
		ExecutionReasonInputChanged,
		ExecutionReasonMissingParent,
		ExecutionReasonParentNotReady,
		ExecutionReasonInputResolveFail:
		return true
	default:
		return false
	}
}

// 调度异步节点
func (e *Engine) scheduleAsyncActivity(
	ctx *nodes.Context,
	node nodes.Node,
	execCtx *nodes.NodeExecContext,
	hash string,
) error {

	job := AsyncJob{
		TaskID: ctx.Task.ID,
		Node:   node.Name,
		// 注意Step Name和Node name 不是一个东西，nodename是dag 结构中的一个节点名称，
		// 而 StepAdapter 是具体执行这一步骤的实现名称，如果这个节点是一个工具，那么它映射到具体要执行哪个工具
		StepAdapter: node.Step.Name(),
		Input:       execCtx.Input,
		Hash:        hash,
	}

	// 异步节点，写入redis stream，等待被 AsyncWorker 调度
	err := e.jobQueue.Publish(ctx.Ctx, job)

	if err != nil {
		return err
	}

	ctx.EmitNodeEvent(node.Name, nodes.NodeEvent{
		Type:    "async_scheduled",
		Message: "开始异步调度...",
	})

	return &domain.WorkflowSuspendedError{
		Reason: domain.SuspendAsyncNode,
	}
}

/*
runDAG 是整个 Workflow Engine 的核心调度器。

核心职责：
1. 解析 DAG 依赖关系
2. 调度可执行节点
3. 执行同步节点
4. 调度异步节点
5. 挂起 / 恢复 Workflow
6. 检测 DAG 死锁
7. 在所有节点完成后结束 Task
*/
func (e *Engine) runDAG(
	runCtx *nodes.Context,
	wf workflow.Workflow,
) RunResult {

	dag := wf.Graph()
	order := wf.Order()
	nodeDefs := wf.Nodes()

	//if runCtx.Task.Status == domain.TaskRunning {
	//	return RunResult{
	//		Status: RunNoop,
	//	}
	//}
	if err := e.transitionTaskStatus(runCtx, domain.TaskRunning); err != nil {
		return RunResult{
			Status: RunFailed,
			Err:    err,
		}
	}

	for {
		progress := false
		for _, name := range order {

			node := nodeDefs[name]
			runtime := runCtx.Runtime[node.Name]

			if runtime.State == domain.NodeSuccessPendingEdges {
				if err := e.finalizeNode(runCtx, node.Name, runtime.Output, nil, dag); err != nil {
					return RunResult{
						Status: RunFailed,
						Err:    err,
					}
				}
				e.tryRecordNodeCost(runCtx, node, runtime)
				progress = true
				continue
			}
			if runtime.State == domain.NodeFailedPendingEdges {
				var err error
				if runtime.Error != "" {
					err = errors.New(runtime.Error)
				}

				if err := e.finalizeNode(runCtx, node.Name, nil, err, dag); err != nil {
					return RunResult{
						Status: RunFailed,
						Err:    err,
					}
				}
				progress = true
				continue
			}

			if runtime.State != domain.NodePending {
				continue
			}
			// 不可达节点处理
			if e.shouldSkipNode(runCtx, node.Name, dag) {

				if err := e.skipNodeWithCorrectKind(runCtx, node.Name, dag); err != nil {
					return RunResult{
						Status: RunFailed,
						Err:    err,
					}
				}

				progress = true
				continue
			}

			// 依赖未满足
			if !e.depsMet(runCtx, node.Name, dag) {
				continue
			}

			// 节点准备
			e.transitionLocked(
				runCtx,
				runtime,
				domain.NodeReady,
				nil,
				nil,
			)

			progress = true

			// 执行节点
			err := e.executeNode(runCtx, node, dag)

			fmt.Printf("DEBUG Run executeNode set ReuseKind=%s\n", runtime.ReuseKind)

			if err != nil {
				var suspendErr *domain.WorkflowSuspendedError
				if errors.As(err, &suspendErr) {
					// 挂起，而不是失败
					_ = e.transitionTaskStatus(runCtx, domain.TaskSuspended)
					// ##### 这里一步
					runCtx.EventBus.Publish(runCtx.Task.RootID, &domain.TaskEvent{
						Step:       name,
						TaskID:     runCtx.Task.ID,
						RootTaskID: runCtx.Task.RootID,
						Type:       domain.TaskEventSuspended,
						Message:    "任务已就绪",
						CreatedAt:  time.Now(),
					})

					return RunResult{
						Status:        RunSuspended,
						SuspendReason: suspendErr.Error(),
						SuspendNode:   node.Name,
					}
				}

				// 可选节点失败：降级为 skip，不让整个任务失败（封面/字幕等非关键步骤）。
				if isOptionalNode(node) {
					fmt.Printf("⚠️ optional node %s failed, skip and continue task: %v\n", name, err)
					if ferr := e.finalizeOptionalFailedNode(runCtx, name, err, dag); ferr != nil {
						return RunResult{
							Status: RunFailed,
							Err:    ferr,
						}
					}
					continue
				}

				if err := e.finalizeNode(runCtx, name, nil, err, dag); err != nil {
					return RunResult{
						Status: RunFailed,
						Err:    err,
					}
				}
				continue
			}

			if err := e.finalizeNode(runCtx, name, nil, nil, dag); err != nil {
				return RunResult{
					Status: RunFailed,
					Err:    err,
				}
			}
			e.tryRecordNodeCost(runCtx, node, runtime)
		}

		// 全部完成
		if e.allTerminal(runCtx) {
			err := e.finishTask(runCtx)
			if err != nil {
				return RunResult{
					Status: RunFailed,
					Err:    err,
				}
			}
			return RunResult{
				Status: RunSuccess,
			}
		}

		// 检查到DAG 死锁
		if !progress {
			// ❗尝试全局 closure（兜底）
			e.globalClosure(runCtx, dag)

			if e.allTerminal(runCtx) {
				err := e.finishTask(runCtx)
				if err != nil {
					return RunResult{
						Status: RunFailed,
						Err:    err,
					}
				}
				return RunResult{
					Status: RunSuccess,
				}
			}

			return RunResult{
				Status: RunFailed,
				Err:    fmt.Errorf("dag deadlock detected （DAG 死锁）"),
			}
		}
	}
}

func (e *Engine) tryRecordNodeCost(
	runCtx *nodes.Context,
	node nodes.Node,
	runtime *domain.NodeRuntime,
) {
	if e == nil || e.costRecorder == nil || runCtx == nil || runtime == nil {
		return
	}
	if len(runtime.Output) == 0 {
		return
	}

	if runtime.Checkpoint != nil {
		if recordedHash := stringValue(runtime.Checkpoint["usage_recorded_output_hash"]); recordedHash != "" &&
			recordedHash == runtime.OutputHash {
			return
		}
		if recordedHash := stringValue(runtime.Checkpoint["cost_recorded_output_hash"]); recordedHash != "" &&
			recordedHash == runtime.OutputHash {
			return
		}
	}

	workflowName := ""
	if runCtx.Workflow != nil {
		workflowName = runCtx.Workflow.Name
	}

	usageFacts, err := e.buildNodeUsageFacts(node, runtime.ResolvedInput, runtime.Output)
	if err != nil {
		log.Printf("build node usage facts failed node=%s step=%s err=%v", node.Name, node.Step.Name(), err)
		return
	}
	if len(usageFacts) == 0 && runtime.Checkpoint != nil {
		usageFacts = toUsageFacts(runtime.Checkpoint["usage_facts"])
	}

	facts, err := e.costRecorder.RecordNodeSuccess(runCtx.Ctx, cost.RecordContext{
		NodeRuntimeID: runtime.ID,
		TaskID:        runCtx.Task.ID,
		RootTaskID:    runCtx.Task.RootID,
		WorkflowName:  workflowName,
		NodeName:      node.Name,
		NodeType:      string(node.Type),
		StepName:      node.Step.Name(),
		Input:         runtime.ResolvedInput,
		OutputHash:    runtime.OutputHash,
		Output:        runtime.Output,
		UsageFacts:    usageFacts,
	})
	if err != nil {
		log.Printf("cost record node success failed node=%s step=%s err=%v", node.Name, node.Step.Name(), err)
		return
	}
	if len(facts) == 0 {
		return
	}

	if runtime.Checkpoint == nil {
		runtime.Checkpoint = map[string]any{}
	}
	runtime.Checkpoint["usage_facts"] = usageFacts
	runtime.Checkpoint["usage_recorded_output_hash"] = runtime.OutputHash
	runtime.Checkpoint["cost_facts"] = facts
	runtime.Checkpoint["cost_recorded_output_hash"] = runtime.OutputHash
	if updateErr := e.nodeRepo.Update(runCtx.Ctx, runtime); updateErr != nil {
		log.Printf("cost checkpoint update failed node=%s err=%v", node.Name, updateErr)
	}

	runCtx.EventBus.Publish(runCtx.Task.RootID, &domain.TaskEvent{
		TaskID:     runCtx.Task.ID,
		RootTaskID: runCtx.Task.RootID,
		Step:       node.Name,
		Type:       "node_cost_identified",
		Message:    "节点成本事实已识别",
		Meta: map[string]any{
			"workflow_name": workflowName,
			"step_name":     node.Step.Name(),
			"facts":         facts,
			"count":         len(facts),
		},
		CreatedAt: time.Now(),
	})
}

func (e *Engine) buildNodeUsageFacts(
	node nodes.Node,
	input map[string]any,
	output map[string]any,
) ([]map[string]any, error) {
	aware, ok := node.Step.(nodes.UsageAwareStep)
	if !ok {
		return nil, nil
	}

	usageFacts, err := aware.BuildUsageFacts(input, output)
	if err != nil || len(usageFacts) == 0 {
		return usageFacts, err
	}

	if err := cost.ValidateUsageFacts(usageFacts, aware.UsageSchema()); err != nil {
		return nil, err
	}

	return usageFacts, nil
}

// computeEdges 计算边
func (e *Engine) computeEdges(
	runCtx *nodes.Context,
	runtime *domain.NodeRuntime,
	dag *graph.Graph) error {
	// DAG 执行时动态激活边，标记 ActivatedEdges
	// 成功后 → 才计算 edge
	// DAG 变成 显式执行路径（Execution Path）
	nodeName := runtime.Name
	edges := dag.Edges[nodeName]
	edgeMap := make(map[string]bool)

	for _, edge := range edges {

		key := nodeName + "->" + edge.To

		activated := false
		// 1️⃣ 无条件边（默认流转）
		if edge.Condition == "" && edge.CaseKey == "" {
			activated = true
		}

		// 2️⃣ condition 表达式
		if edge.Condition != "" {
			ok, err := runCtx.EvalBool(edge.Condition)
			if err != nil {
				return err
			}
			activated = ok
		}

		// 3️⃣ switch case
		if edge.CaseKey != "" {
			val := runCtx.GetNodeOutput(nodeName)["case"]
			activated = (val == edge.CaseKey)
		}

		edgeMap[key] = activated
		runCtx.ActivatedEdges[key] = activated
		// 处理“未激活边 → 子树跳过”
		if !activated {
			e.skipSubtree(runCtx, edge.To, dag)
		}
	}
	// 持久化runtime
	runtime.ActivatedEdges = edgeMap
	return nil
}

// finalizeNode 节点完成 + 边计算
func (e *Engine) finalizeNode(
	runCtx *nodes.Context,
	nodeName string,
	output map[string]any,
	err error,
	dag *graph.Graph,
) error {

	e.mu.RLock()
	runtime := runCtx.Runtime[nodeName]
	e.mu.RUnlock()

	if err == nil {
		// 成功激活边
		// DAG 执行时动态激活边，标记 ActivatedEdges
		if err := e.computeEdges(runCtx, runtime, dag); err != nil {
			return err
		}
		e.transitionLocked(
			runCtx,
			runtime,
			domain.NodeSuccess,
			nil,
			output,
		)
		e.ensureEdgeClosure(nodeName, dag, runCtx)
		return nil
	}

	// ❗失败关闭所有出边。EdgeNormal 写 true（必选路径，父死 → resolveEdgeState
	// 在 parent.State=Failed 时优先返回 blocked，true 仅保持语义一致）。
	// EdgeCondition 仍写 false 保持兼容；resolveEdgeState 对 parent=Failed 无条件
	// 返回 blocked，因此 false 不会被误解为 inactive。
	edges := dag.Edges[nodeName]
	edgeMap := make(map[string]bool)

	for _, edge := range edges {
		key := nodeName + "->" + edge.To
		if edge.Type == definition.EdgeNormal {
			edgeMap[key] = true
			runCtx.ActivatedEdges[key] = true
		} else {
			edgeMap[key] = false
			runCtx.ActivatedEdges[key] = false
		}
	}
	runtime.ActivatedEdges = edgeMap
	e.transitionLocked(
		runCtx,
		runtime,
		domain.NodeFailed,
		err,
		nil,
	)
	// ❗触发 Path Closure（递归关闭整个子图）
	e.failClosure(runCtx, nodeName, dag)
	return nil
}

// isOptionalNode 判断节点是否为「可选节点」（config.optional == true）。
// 可选节点执行失败时不拖垮整个任务，而是被降级为 skip。
// 适用于封面提取、字幕烧录等非关键、失败不应中断主交付物的步骤。
func isOptionalNode(node nodes.Node) bool {
	if node.Config == nil {
		return false
	}
	if v, ok := node.Config["optional"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// finalizeOptionalFailedNode 处理「可选节点」执行失败：
// 把该节点标记为 Skipped（finishTask 只在出现 NodeFailed 时才让任务失败，
// Skipped 不会），关闭其出边并向下游级联 skip（仅级联那些所有入边都失效的下游，
// 例如封面上传会随封面提取一起被 skip）。失败原因记录在 runtime.Error 中便于排查。
func (e *Engine) finalizeOptionalFailedNode(
	runCtx *nodes.Context,
	nodeName string,
	cause error,
	dag *graph.Graph,
) error {
	e.mu.RLock()
	runtime := runCtx.Runtime[nodeName]
	e.mu.RUnlock()
	if runtime == nil {
		return fmt.Errorf("runtime not found: %s", nodeName)
	}

	edgeMap := make(map[string]bool)
	for _, edge := range dag.Edges[nodeName] {
		key := nodeName + "->" + edge.To
		if edge.Type == definition.EdgeNormal {
			edgeMap[key] = true
			runCtx.ActivatedEdges[key] = true
		} else {
			edgeMap[key] = false
			runCtx.ActivatedEdges[key] = false
		}
	}
	runtime.ActivatedEdges = edgeMap

	reason := "optional node failed (skipped)"
	if cause != nil {
		reason = "optional node failed (skipped): " + cause.Error()
	}
	e.transitionLocked(
		runCtx,
		runtime,
		domain.NodeSkipped,
		fmt.Errorf("%s", reason),
		nil,
	)
	// 向下游级联 skip（与失败一致的子图关闭，但本节点是 Skipped 而非 Failed）。
	e.failClosure(runCtx, nodeName, dag)
	return nil
}

func (e *Engine) globalClosure(ctx *nodes.Context, dag *graph.Graph) {

	for name, runtime := range ctx.Runtime {

		if runtime.State != domain.NodePending {
			continue
		}

		if e.shouldSkipNode(ctx, name, dag) {

			e.mu.Lock()
		_:
			e.skipNodeWithCorrectKind(ctx, name, dag)
			e.mu.Unlock()
		}
	}
}

// failClosure 失败路径闭包
func (e *Engine) failClosure(ctx *nodes.Context, start string, dag *graph.Graph) {

	queue := []string{start}

	for len(queue) > 0 {

		cur := queue[0]
		queue = queue[1:]

		for _, child := range dag.Children[cur] {

			key := cur + "->" + child

			// 关闭出边：EdgeNormal 写 true（必选路径但父死），resolveEdgeState
			// 根据父非 Success 推导 blocked；EdgeCondition 写 false，同样由
			// resolveEdgeState 中 parent.State != Success → blocked 覆盖。
			// 不再无脑写 false，避免把 blocked 编码成 inactive。
			edge := findEdge(dag, cur, child)
			if edge != nil && edge.Type == definition.EdgeNormal {
				ctx.ActivatedEdges[key] = true
			} else {
				ctx.ActivatedEdges[key] = false
			}

			e.mu.Lock()
			runtime := ctx.Runtime[child]

			// 只处理 Pending 节点
			if runtime.State == domain.NodePending {

				// ❗判断是否可以 skip（所有路径都死）
				if e.shouldSkipNode(ctx, child, dag) {
					_ = e.finalizeBlockedNode(ctx, child, dag)
					//e.transitionLocked(
					//	ctx,
					//	runtime,
					//	domain.NodeSkipped,
					//	nil,
					//	nil,
					//)

					// 继续向下传播
					queue = append(queue, child)
				}
			}
			e.mu.Unlock()
		}
	}
}

// finalizeSkippedNode 标记节点为 Skipped（死分支：条件未命中导致）。
// 所有出边写 false（inactive），表示此子树从未进入执行路径。
// 仅 skipSubtree / globalClosure 调用。
func (e *Engine) finalizeSkippedNode(
	ctx *nodes.Context,
	nodeName string,
	dag *graph.Graph,
) error {
	runtime := ctx.Runtime[nodeName]
	if runtime == nil {
		return fmt.Errorf("runtime not found: %s", nodeName)
	}

	edgeMap := make(map[string]bool)
	for _, edge := range dag.Edges[nodeName] {
		key := nodeName + "->" + edge.To
		// 死分支：所有出边 inactive（false），下游 resolveEdgeState 会判定为
		// parent=Skipped+edge=false → inactive（继续传播死分支），而非 blocked。
		edgeMap[key] = false
		ctx.ActivatedEdges[key] = false
	}

	runtime.ActivatedEdges = edgeMap

	e.transitionLocked(
		ctx,
		runtime,
		domain.NodeSkipped,
		nil,
		nil,
	)
	return nil
}

// finalizeBlockedNode 标记节点为 Skipped（阻塞路径：上游失败/跳过导致）。
// EdgeNormal 出边写 true，resolveEdgeState 根据 parent=Skipped+edge=true 推导 blocked。
// 仅 failClosure / finalizeOptionalFailedNode 调用。
func (e *Engine) finalizeBlockedNode(
	ctx *nodes.Context,
	nodeName string,
	dag *graph.Graph,
) error {
	runtime := ctx.Runtime[nodeName]
	if runtime == nil {
		return fmt.Errorf("runtime not found: %s", nodeName)
	}

	edgeMap := make(map[string]bool)
	for _, edge := range dag.Edges[nodeName] {
		key := nodeName + "->" + edge.To
		if edge.Type == definition.EdgeNormal {
			edgeMap[key] = true
			ctx.ActivatedEdges[key] = true
		} else {
			edgeMap[key] = false
			ctx.ActivatedEdges[key] = false
		}
	}

	runtime.ActivatedEdges = edgeMap

	e.transitionLocked(
		ctx,
		runtime,
		domain.NodeSkipped,
		nil,
		nil,
	)
	return nil
}

// skipSubtree 处理子数跳过
func (e *Engine) skipSubtree(
	ctx *nodes.Context,
	start string,
	dag *graph.Graph,
) {

	stack := []string{start}

	for len(stack) > 0 {

		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		e.mu.Lock()
		runtime := ctx.Runtime[node]

		if runtime.State != domain.NodePending {
			e.mu.Unlock()
			continue
		}

		// 判断是否还有活跃父节点
		if !e.canSkipNode(ctx, node, dag) {
			e.mu.Unlock()
			continue
		}

		if err := e.skipNodeWithCorrectKind(ctx, node, dag); err != nil {
			e.mu.Unlock()
			return
		}
		//e.transitionLocked(
		//	ctx,
		//	runtime,
		//	domain.NodeSkipped,
		//	nil,
		//	nil,
		//)
		e.mu.Unlock()

		for _, c := range dag.Children[node] {
			stack = append(stack, c)
		}
	}
}

// shouldSkipNode 调度前判断节点是否已不可执行（P0 修复：三态语义 + AND-join）。
//
// 新语义（通过 resolveEdgeState 统一推导）：
//   - 任一父 blocked → true（必选路径阻塞，节点应 skip）
//   - 有父 unknown → false（继续等待）
//   - 所有父 terminal，但没有任何 active 父（全 inactive 条件边）→ true
//   - 有 active 父 → false（可以继续尝试执行，depsMet 会做最终判断）
func (e *Engine) shouldSkipNode(ctx *nodes.Context, node string, dag *graph.Graph) bool {
	parents := dag.Parents[node]
	if len(parents) == 0 {
		return false
	}

	hasActiveParent := false
	hasUnknownParent := false

	for _, p := range parents {
		es := e.resolveEdgeState(ctx, p, node, dag)
		switch es {
		case EdgeStateUnknown:
			hasUnknownParent = true
		case EdgeStateBlocked:
			// 必选父或条件父死 → 此节点已不可执行。
			return true
		case EdgeStateInactive:
			continue
		case EdgeStateActive:
			hasActiveParent = true
		}
	}

	if hasUnknownParent {
		return false
	}
	if !hasActiveParent {
		return true
	}
	return false
}

// canSkipNode 判断节点的所有激活父是否均已 terminal 且非 Success（P0 修复：三态语义）。
// 用于 skipSubtree 等传播路径。
func (e *Engine) canSkipNode(
	ctx *nodes.Context,
	node string,
	dag *graph.Graph,
) bool {
	for _, parent := range dag.Parents[node] {
		es := e.resolveEdgeState(ctx, parent, node, dag)
		switch es {
		case EdgeStateUnknown:
			return false
		case EdgeStateBlocked:
			continue // 此父已死，检查下一个
		case EdgeStateInactive:
			continue // 条件边未走
		case EdgeStateActive:
			// 有 active 父 → 不能 skip（还在 etc 或应正常执行）。
			return false
		}
	}
	return true
}

// shouldPersistOutput 根据节点配置决定是否持久化 output 到 task_nodes。
// 终端聚合节点（如 build_creative_detail）可设 persist_output: false 跳过落盘，
// output 仅在内存中流转给 GetFinal() 使用。
func shouldPersistOutput(node nodes.Node) bool {
	if node.Config == nil {
		return true
	}
	if v, ok := node.Config["persist_output"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	// 默认持久化，保持向后兼容
	return true
}
