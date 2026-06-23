package engine

import (
	"context"
	"fmt"
	"strings"

	"time"

	"flux/domain"
	"flux/utils"
	"flux/workflow/nodes"
)

// startAsyncNodeEventListener 监听node_complete_async完成的通知，用来恢复节点
func (e *Engine) startAsyncNodeEventListener() {
	ch := e.eventBus.Subscribe(domain.TaskEventNodeCompleteAsync)
	go func() {
		for evt := range ch {

			taskID := evt.TaskID
			nodeName := evt.Step

			fmt.Println("receive node_complete_async event", taskID, nodeName)

			// 只有抢占成功的线程才能进入下一步
			ok, err := e.completeAsyncNode(evt.TaskID, evt.Step, evt.Meta, evt.Error)
			if err != nil || !ok {
				continue
			}

			// 恢复任务
			result := e.ResumeTask(taskID, nodeName, evt.Meta)
			switch result.Status {
			case RunFailed:
				fmt.Println("resume error:", result.Err)
			}
		}

	}()
}

func (e *Engine) startSubWorkflowSuccessListener() {
	ch := e.eventBus.Subscribe("task_succeeded")

	go func() {
		for evt := range ch {
			taskID := evt.TaskID

			evtCtx, err := e.loadChildEventContext(taskID)
			if err != nil {
				fmt.Printf("loadChildEventContext failed: child=%d err=%v\n", taskID, err)
				continue
			}
			if evtCtx == nil {
				continue
			}

			child := evtCtx.child
			parentID := evtCtx.parentID
			parentNode := evtCtx.parentNode
			parentRuntime := evtCtx.parentRuntime

			accept, err := e.canAcceptChildResult(parentID, parentNode, child)
			if err != nil {
				fmt.Printf("canAcceptChildResult failed: child=%d parent=%d node=%s err=%v\n", child.ID, parentID, parentNode, err)
				continue
			}
			if !accept {
				fmt.Printf("ignore stale child success event: child=%d parent=%d node=%s\n", child.ID, parentID, parentNode)
				continue
			}

			kind := e.classifyParentFanoutNode(parentRuntime)

			switch kind {
			case ParentFanoutNodeLoop, ParentFanoutNodeMap:
				// Fanout 节点一律只唤醒
				e.resumeParentTask(parentID, parentNode, nil)
				continue

			case ParentFanoutNodeNone:
				e.publishSubWorkflowFanoutProgress(child, parentID, parentNode, 1, 0, 0)
				final, err := e.parseChildFinal(child)
				if err != nil {
					fmt.Printf("parse child final failed: child=%d err=%v\n", child.ID, err)
					continue
				}

				// P1：存在 subworkflow binding 时，完成路径统一汇聚到 CompleteAwaitNode
				// （ClaimCompleting 去重，与后续 poll 兜底互斥）；否则回退旧路径。
				if e.tryCompleteSubWorkflowBinding(parentID, parentNode, final, "") {
					continue
				}

				e.completeAndResumeParent(
					parentID,
					parentNode,
					final,
					"",
					final,
				)
				continue

			default:
				fmt.Printf("unknown parent fanout kind: child=%d parent=%d node=%s kind=%s\n",
					child.ID, parentID, parentNode, kind)
				continue
			}
		}
	}()
}

func (e *Engine) startSubWorkflowFailedListener() {
	ch := e.eventBus.Subscribe("task_failed")

	go func() {
		for evt := range ch {
			taskID := evt.TaskID

			evtCtx, err := e.loadChildEventContext(taskID)
			if err != nil {
				fmt.Printf("loadChildEventContext failed: child=%d err=%v\n", taskID, err)
				continue
			}
			if evtCtx == nil {
				continue
			}

			child := evtCtx.child
			parentID := evtCtx.parentID
			parentNode := evtCtx.parentNode
			parentRuntime := evtCtx.parentRuntime

			accept, err := e.canAcceptChildResult(parentID, parentNode, child)
			if err != nil {
				fmt.Printf("canAcceptChildResult failed: child=%d parent=%d node=%s err=%v\n", child.ID, parentID, parentNode, err)
				continue
			}
			if !accept {
				fmt.Printf("ignore stale child failed event: child=%d parent=%d node=%s\n", child.ID, parentID, parentNode)
				continue
			}

			kind := e.classifyParentFanoutNode(parentRuntime)

			switch kind {
			case ParentFanoutNodeLoop, ParentFanoutNodeMap:
				// partial 模式：子任务失败不导致父任务永久失败，唤醒父任务由 Map 节点写 fallback
				if kind == ParentFanoutNodeMap && isPartialFailurePolicy(parentRuntime.Checkpoint) {
					e.resumeParentTask(parentID, parentNode, nil)
					continue
				}
				// 子任务重试耗尽 → 父任务不再唤醒，直接标记永久失败
				if child.RetryCount > domain.MaxAutoRetryCount {
					e.permanentFailParent(context.Background(), parentID, parentNode, child)
					continue
				}
				e.resumeParentTask(parentID, parentNode, nil)
				continue

			case ParentFanoutNodeNone:
				e.publishSubWorkflowFanoutProgress(child, parentID, parentNode, 0, 0, 1)
				if e.tryCompleteSubWorkflowBinding(parentID, parentNode, nil, evt.Message) {
					continue
				}
				e.completeAndResumeParent(
					parentID,
					parentNode,
					nil,
					evt.Message,
					evt.Meta,
				)
				continue

			default:
				fmt.Printf("unknown parent fanout kind on failed listener: child=%d parent=%d node=%s kind=%s\n",
					child.ID, parentID, parentNode, kind)
				continue
			}
		}
	}()
}

func (e *Engine) completeAsyncNode(
	taskID int64,
	nodeName string,
	output map[string]any,
	errMsg string,
) (bool, error) {
	publicOutput, _ := splitAwaitEventPayload(output)

	// 调用上面新增的抢占式更新方法
	ok, err := e.nodeRepo.AttemptCompletePendingEdges(context.Background(), taskID, nodeName, publicOutput, errMsg)
	if err != nil {
		return false, err
	}

	if !ok {
		// 抢占失败，说明别的线程已经处理过这个节点了
		fmt.Printf("Node %s for Task %d already completed by another thread, skipping resume\n", nodeName, taskID)
		return false, nil
	}

	return true, nil
}

func (e *Engine) getMapTaskIndex(task *domain.Task) (int, error) {
	if task == nil {
		return 0, fmt.Errorf("task is nil")
	}

	// 优先使用任务元数据
	if task.MapIndex != nil {
		return *task.MapIndex, nil
	}

	// fallback：从 InputJSON 恢复
	input := parseTaskInput(task.InputJSON)
	if input == nil {
		return 0, fmt.Errorf("map sub task missing input json")
	}

	if v, ok := input["index"]; ok {
		switch n := v.(type) {
		case int:
			return n, nil
		case int32:
			return int(n), nil
		case int64:
			return int(n), nil
		case float32:
			return int(n), nil
		case float64:
			return int(n), nil
		}
	}

	return 0, fmt.Errorf("map sub task missing index metadata")
}

func (e *Engine) canAcceptChildResult(
	parentID int64,
	parentNodeName string,
	child *domain.Task,
) (bool, error) {
	if child == nil {
		return false, nil
	}

	if child.Status == domain.TaskCanceled {
		return false, nil
	}

	parentTask, err := e.taskRepo.GetByID(context.Background(), parentID)
	if err != nil {
		return false, err
	}
	if parentTask == nil {
		return false, nil
	}

	switch parentTask.Status {
	case domain.TaskRunning, domain.TaskSuspended:
		// ok
	default:
		return false, nil
	}

	parentRuntime, err := e.nodeRepo.FindByTaskIDAndNode(context.Background(), parentID, parentNodeName)
	if err != nil {
		return false, err
	}
	if parentRuntime == nil {
		return false, nil
	}

	switch parentRuntime.State {
	case domain.NodePending,
		domain.NodeReady,
		domain.NodeRunning,
		domain.NodeRetrying,
		domain.NodeAwaiting, // P1：subworkflow 父节点挂起后落 NodeAwaiting，须可接收子任务完成事件
		domain.NodeSuccessPendingEdges,
		domain.NodeFailedPendingEdges:
		// ok
	default:
		return false, nil
	}

	kind := e.classifyParentFanoutNode(parentRuntime)

	// Loop 必须校验 running_sub_key binding，避免旧 child 事件污染当前 loop iteration
	if kind == ParentFanoutNodeLoop {
		ok, err := e.checkLoopChildBinding(parentTask, parentRuntime, child)
		if err != nil {
			return false, err
		}
		if !ok {
			fmt.Printf("reject loop child event by binding mismatch: child=%d parent=%d node=%s\n",
				child.ID, parentID, parentNodeName)
			return false, nil
		}
	}

	return true, nil
}

func (e *Engine) checkLoopChildBinding(
	parentTask *domain.Task,
	parentRuntime *domain.NodeRuntime,
	child *domain.Task,
) (bool, error) {
	if parentTask == nil || parentRuntime == nil || child == nil {
		return false, nil
	}

	cp := parentRuntime.Checkpoint
	if cp == nil {
		// 没 checkpoint，说明不是 loop / map 这类状态节点，放行
		return true, nil
	}

	runningSubKey := strings.TrimSpace(asString(cp["running_sub_key"]))
	runningIndex := asInt(cp["running_index"])

	// 没 running binding，说明不是 loop 正在等子任务，放行
	if runningSubKey == "" && runningIndex == -1 {
		return true, nil
	}

	// 说明这是 loop 节点，必须严格匹配
	if child.SubKey == nil || strings.TrimSpace(*child.SubKey) == "" {
		return false, nil
	}

	if runningSubKey != strings.TrimSpace(*child.SubKey) {
		return false, nil
	}

	return true, nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float32:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}

func (e *Engine) resumeParentTask(parentID int64, parentNode string, meta map[string]any) {
	result := e.ResumeTask(parentID, parentNode, meta)
	if result.Status == RunFailed {
		fmt.Printf("resume parent task failed: parentID=%d parentNode=%s err=%v\n", parentID, parentNode, result.Err)
	}
}

// tryCompleteSubWorkflowBinding 若 (父任务, 父节点) 存在 subworkflow await binding，
// 则把完成事件汇聚到 CompleteAwaitNode（内部 ClaimCompleting 与 P2 的 poll 兜底原子去重），
// 返回 true 表示已由 binding 路径处理；否则返回 false，调用方回退旧的 completeAndResumeParent。
//
// 路由依据是"binding 是否存在"而非 feature flag：flag 关闭时不会写入 binding，自然回退旧路径；
// flag 灰度切换期间，旧节点（无 binding，NodeRunning）走旧路、新节点（有 binding，NodeAwaiting）
// 走 binding，互不影响。
func (e *Engine) tryCompleteSubWorkflowBinding(parentID int64, parentNode string, output map[string]any, errMsg string) bool {
	if e.awaitBindingRepo == nil {
		return false
	}
	binding, err := e.awaitBindingRepo.GetByTaskAndNode(context.Background(), parentID, parentNode)
	if err != nil || binding == nil || binding.AwaitType != domain.AwaitTypeSubWorkflow {
		return false
	}
	e.CompleteAwaitNode(binding.ID, output, errMsg, "event:subworkflow")
	return true
}

func (e *Engine) publishSubWorkflowFanoutProgress(child *domain.Task, parentID int64, parentNode string, done, running, failed int) {
	if e == nil || e.eventBus == nil || child == nil || parentNode == "" {
		return
	}
	progress := 0.0
	if done >= 1 {
		progress = 1
	}
	status := "running"
	if failed > 0 {
		status = "failed"
	} else if done >= 1 {
		status = "completed"
	}
	e.eventBus.Publish(child.RootID, &domain.TaskEvent{
		TaskID:     parentID,
		RootTaskID: child.RootID,
		Step:       parentNode,
		Type:       nodes.TaskEventFanoutProgress,
		Message:    parentNode,
		Progress:   progress,
		Grade:      domain.GradePersistent,
		CreatedAt:  time.Now(),
		Meta: map[string]any{
			"event_type":    nodes.TaskEventFanoutProgress,
			"fanout_kind":   string(nodes.FanoutKindSubWorkflow),
			"parent_node":   parentNode,
			"parent_label":  parentNode,
			"total":         1,
			"done":          done,
			"running":       running,
			"failed":        failed,
			"reused":        0,
			"current_index": 1,
			"progress":      progress,
			"status":        status,
		},
	})
}

func (e *Engine) completeAndResumeParent(
	parentID int64,
	parentNode string,
	output map[string]any,
	errMsg string,
	resumeMeta map[string]any,
) {
	ok, err := e.completeAsyncNode(parentID, parentNode, output, errMsg)
	if err != nil {
		fmt.Printf("completeAsyncNode failed: parentID=%d parentNode=%s err=%v\n", parentID, parentNode, err)
		return
	}
	if !ok {
		return
	}

	e.resumeParentTask(parentID, parentNode, resumeMeta)
}

func (e *Engine) parseChildFinal(child *domain.Task) (map[string]any, error) {
	if child == nil {
		return nil, fmt.Errorf("child is nil")
	}
	return utils.ParseFinal(child.OutputJSON)
}

// permanentFailParent 当子任务重试耗尽时，永久标记父节点失败，防止无限唤醒循环。
func (e *Engine) permanentFailParent(ctx context.Context, parentID int64, parentNode string, child *domain.Task) {
	task, err := e.taskRepo.GetByID(ctx, parentID)
	if err != nil || task == nil {
		return
	}

	runtime, err := e.nodeRepo.FindByTaskIDAndNode(ctx, parentID, parentNode)
	if err != nil || runtime == nil {
		return
	}

	now := time.Now()
	runtime.State = domain.NodeFailed
	runtime.FinishedAt = &now
	runtime.Error = fmt.Sprintf("child task %d permanently failed after %d retries", child.ID, child.RetryCount)
	_ = e.nodeRepo.Update(ctx, runtime)

	task.Status = domain.TaskFailed
	task.ErrorMessage = runtime.Error
	_ = e.taskRepo.Update(ctx, task)

	e.eventBus.Publish(task.RootID, &domain.TaskEvent{
		TaskID:     task.ID,
		RootTaskID: task.RootID,
		Step:       parentNode,
		Type:       domain.TaskEventFinalFailed,
		Message:    "任务最终失败",
		Meta: map[string]any{
			"reason":        "child_retries_exhausted",
			"child_task_id": child.ID,
			"retry_count":   child.RetryCount,
		},
		CreatedAt: now,
	})
}

// isPartialFailurePolicy 检查 Map 节点 checkpoint 中的 failure_policy 是否为 "partial"。
// 旧任务没有该字段时默认返回 false（fail_fast）。
func isPartialFailurePolicy(cp map[string]any) bool {
	if cp == nil {
		return false
	}
	v, ok := cp["failure_policy"].(string)
	return ok && v == "partial"
}
