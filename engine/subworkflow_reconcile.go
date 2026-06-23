package engine

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"flux/domain"
	"flux/utils"
)

// ReconcileSubWorkflowBinding 是 subworkflow await binding 的 poll 对账兜底（P2）。
//
// AwaitPollWorker 周期性对 due 的 subworkflow binding 调用本方法：直接查子任务的真实终态，
// 不依赖会丢的进程内事件，从而修复"子任务已完成但父任务没被唤醒"的静默挂起
// （P1 把父节点落到 NodeAwaiting 退出了 heartbeat scanner，这条 poll 兜底就是它的安全网）。
//
// 与事件快路径（event_listen.go）汇聚到同一个 CompleteAwaitNode，靠 ClaimCompleting 原子去重，
// 两路只生效一次。子任务仍在执行时只重排下次对账；子任务终态失败/取消时把 binding 完成为失败，
// 父任务重试时由 RunSubWorkflow 的 TaskFailed/TaskCanceled 分支（Fix 4）负责复活，沿用既有语义。
func (e *Engine) ReconcileSubWorkflowBinding(bindingID int64) RunResult {
	if e.awaitBindingRepo == nil || e.taskRepo == nil {
		return RunResult{Status: RunNoop}
	}
	ctx := context.Background()

	binding, err := e.loadAwaitBinding(bindingID)
	if err != nil || binding == nil {
		return RunResult{Status: RunNoop}
	}
	// 只处理仍在等待的 subworkflow binding；其它（已完成/被事件路径接管）直接放过。
	if binding.Status != domain.AwaitBindingWaiting || binding.AwaitType != domain.AwaitTypeSubWorkflow {
		return RunResult{Status: RunNoop}
	}

	childID := subWorkflowChildIDFromCorrelation(binding.Correlation)
	if childID == 0 {
		// binding 缺少 child 链接信息，无法对账：完成为失败，避免父任务永久挂起。
		return e.CompleteAwaitNode(binding.ID, nil, "subworkflow binding missing child_task_id", "poll:subworkflow")
	}

	child, err := e.taskRepo.GetByID(ctx, childID)
	if err != nil {
		// 读子任务失败（可能 DB 抖动）：不动 binding 终态，重排下次再对账。
		e.rescheduleSubWorkflowBinding(ctx, binding)
		return RunResult{Status: RunNoop}
	}
	if child == nil {
		return e.CompleteAwaitNode(binding.ID, nil, fmt.Sprintf("subworkflow child task %d not found", childID), "poll:subworkflow")
	}

	switch child.Status {
	case domain.TaskSuccess:
		final, perr := utils.ParseFinal(child.OutputJSON)
		if perr != nil {
			return e.CompleteAwaitNode(binding.ID, nil, fmt.Sprintf("parse subworkflow child %d output failed: %v", childID, perr), "poll:subworkflow")
		}
		return e.CompleteAwaitNode(binding.ID, final, "", "poll:subworkflow")

	case domain.TaskFailed, domain.TaskCanceled:
		msg := child.ErrorMessage
		if msg == "" {
			msg = fmt.Sprintf("subworkflow child task %d terminal status=%s", childID, child.Status)
		}
		return e.CompleteAwaitNode(binding.ID, nil, msg, "poll:subworkflow")

	case domain.TaskPending:
		// 子任务卡在 pending（可能游离出队列）：重新入队 + 重排对账。
		_ = e.taskRepo.Enqueue(ctx, child.ID)
		e.rescheduleSubWorkflowBinding(ctx, binding)
		return RunResult{Status: RunNoop}

	default: // TaskRunning / TaskSuspended：仍在执行，重排下次对账。
		e.rescheduleSubWorkflowBinding(ctx, binding)
		return RunResult{Status: RunNoop}
	}
}

func (e *Engine) rescheduleSubWorkflowBinding(ctx context.Context, binding *domain.AwaitBinding) {
	now := time.Now()
	next := now.Add(subWorkflowReconcileInterval)
	binding.LastPolledAt = &now
	binding.NextPollAt = &next
	binding.PollAttempts++
	if err := e.awaitBindingRepo.Update(ctx, binding); err != nil {
		fmt.Printf("subworkflow reconcile reschedule failed: binding=%d err=%v\n", binding.ID, err)
	}
}

// subWorkflowChildIDFromCorrelation 从 binding.Correlation 取 child_task_id。
// 写入时用字符串（雪花 int64 经 JSON float64 会丢精度），这里兼容 string / 数字两种历史形态。
func subWorkflowChildIDFromCorrelation(correlation map[string]any) int64 {
	if correlation == nil {
		return 0
	}
	switch v := correlation["child_task_id"].(type) {
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	default:
		return 0
	}
}
