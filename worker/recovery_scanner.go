package worker

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"flux/domain"
	"flux/eventbus"
	"flux/repository"
	"flux/service"
	"gorm.io/gorm"
)

// RecoveryScanner 数据库级恢复扫描器
// Recovery Scanner（数据库级恢复扫描器）
//
// 负责：
//  1. 扫描 DB 中 status = running 的任务
//  2. 找出其中 node_state = running 的节点
//  3. 把它们改为 retrying
//  4. 把任务重新 push 进队列
//  5. 保证不会重复恢复
type RecoveryScanner struct {
	taskRepo     repository.TaskRepository
	nodeRepo     repository.NodeRuntimeRepository
	retryService service.TaskRetryService
	eventBus     *eventbus.EventBus
	interval     time.Duration /// 每 30 秒扫一次
	timeout      time.Duration // 心跳超过 多少 分钟没更新才算 crash
}

func NewRecoveryScanner(
	taskRepo repository.TaskRepository,
	nodeRepo repository.NodeRuntimeRepository,
	retryService service.TaskRetryService,
	eventBus *eventbus.EventBus,
	interval time.Duration,
	timeout time.Duration,
) *RecoveryScanner {
	return &RecoveryScanner{
		taskRepo:     taskRepo,
		nodeRepo:     nodeRepo,
		retryService: retryService,
		eventBus:     eventBus,
		interval:     interval,
		timeout:      timeout, // 超过2分钟没更新就算 crash
	}
}

func (r *RecoveryScanner) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				r.scan(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (r *RecoveryScanner) scan(ctx context.Context) {
	expireTime := time.Now().Add(-r.timeout)

	runningNodes, err := r.nodeRepo.FindExpiredRunningNodes(ctx, expireTime)
	if err != nil {
		log.Println("scan error:", err)
		return
	}

	seenTaskIDs := make(map[int64]struct{})

	for _, node := range runningNodes {
		if node == nil {
			continue
		}

		if _, ok := seenTaskIDs[node.TaskID]; ok {
			continue
		}
		seenTaskIDs[node.TaskID] = struct{}{}

		task, err := r.taskRepo.GetByID(ctx, node.TaskID)
		if err != nil || task == nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				r.cancelRunningNode(ctx, node, "当前任务不存在或者已经删除，正在取消运行中的节点")
			}
			continue
		}

		switch task.Status {
		case domain.TaskRunning, domain.TaskSuspended:
		case domain.TaskCanceled, "cancelled":
			r.cancelRunningNode(ctx, node, "当前任务已取消，正在取消运行中的节点")
		default:
			continue
		}

		switch node.State {
		case domain.NodeSuccessPendingEdges, domain.NodeFailedPendingEdges:
			if err := r.requeuePendingEdgesTask(ctx, task, node); err != nil {
				log.Printf("recovery requeue pending-edges task failed: task=%d node=%s err=%v\n", task.ID, node.Name, err)
			}
			continue
		}

		// 根因豁免：父任务正常运行/挂起、其“停滞”节点实际是一个仍在等待“在飞子任务”的
		// subworkflow / map / loop 父节点时，绝不能对父任务做 PrepareTaskRetry。
		// 这类父节点挂起后停留在 NodeRunning 且没有任何心跳来源（subworkflow 不走
		// runNodeWithHeartbeat，也没有 AsyncWorker 续心跳），超时后会被 FindExpiredRunningNodes
		// 误判为 crash。一旦重试父任务，cancelChildTasksForRetry 会取消还在跑的子任务，
		// 随后父任务重跑该节点又会用相同 sub_key 重新建子任务，撞 idx_tasks_sub_key_not_null。
		// 这种情况下父节点是“正常等子任务”，恢复动作应作用在子任务上，这里直接跳过父任务重试。
		if task.Status == domain.TaskRunning || task.Status == domain.TaskSuspended {
			if r.hasLiveChildren(ctx, task, node) {
				continue
			}
		}

		if err := r.retryService.PrepareTaskRetry(
			ctx,
			task.ID,
			service.RetryTriggerRecovery,
			"",
			nil,
		); err != nil {
			if strings.Contains(err.Error(), "task auto retry exhausted") {
				r.failRetryExhausted(ctx, task, node, err)
				continue
			}
			log.Printf("recovery prepare retry failed: task=%d err=%v\n", task.ID, err)
			continue
		}

		if err := r.taskRepo.Enqueue(ctx, task.ID); err != nil {
			log.Printf("recovery enqueue failed: task=%d err=%v\n", task.ID, err)
			continue
		}

		log.Printf("recovery scanner re-enqueued task=%d\n", task.ID)
	}
}

// hasLiveChildren 判断 node 是否是一个仍在等待“非终态子任务”的父节点（subworkflow / map / loop）。
//
// 这类节点 fan-out 出子任务后会挂起，自身停留在 NodeRunning。subworkflow 父节点没有任何心跳来源，
// 超时后会被 FindExpiredRunningNodes 当成 crash 节点。若其子任务仍处于 pending/running/suspended，
// 说明父任务是“正常等子任务完成后被唤醒”，不应被恢复重试（否则会误伤在飞子任务）。
//
// 返回 true 表示存在活着的子任务、调用方应跳过父任务重试。顺带把 pending 的子任务重新入队，
// 避免它游离在队列之外迟迟不被调度；running/suspended 的子任务交给它自己的节点级恢复负责。
func (r *RecoveryScanner) hasLiveChildren(ctx context.Context, task *domain.Task, node *domain.NodeRuntime) bool {
	if task == nil || node == nil {
		return false
	}

	children, err := r.taskRepo.ListByParentNode(ctx, task.ID, node.Name)
	if err != nil {
		log.Printf("recovery list children failed: task=%d node=%s err=%v\n", task.ID, node.Name, err)
		return false
	}

	live := false
	for _, child := range children {
		if child == nil {
			continue
		}
		switch child.Status {
		case domain.TaskPending:
			live = true
			if err := r.taskRepo.Enqueue(ctx, child.ID); err != nil {
				log.Printf("recovery re-enqueue pending child failed: parent=%d child=%d err=%v\n", task.ID, child.ID, err)
			}
		case domain.TaskRunning, domain.TaskSuspended:
			live = true
		}
	}

	if live {
		log.Printf("recovery skip parent retry (live child waiting): parent=%d node=%s state=%s\n", task.ID, node.Name, node.State)
	}
	return live
}

func (r *RecoveryScanner) cancelRunningNode(ctx context.Context, node *domain.NodeRuntime, message string) {

	if node == nil {
		return
	}

	now := time.Now()
	step := node.Name
	if node.State == domain.NodeSuccessPendingEdges {
		node.State = domain.NodeSuccess
	} else {
		node.State = domain.NodeCanceled
	}

	node.FinishedAt = &now
	node.LastHeartbeat = nil
	if err := r.nodeRepo.Update(ctx, node); err != nil {
		log.Printf("recovery mark cancell node failed failed: task=%d node=%s err=%v\n", node.TaskID, node.Name, err)
	}

	if r.eventBus != nil {
		r.eventBus.Publish(node.TaskID, &domain.TaskEvent{
			TaskID:    node.TaskID,
			Step:      step,
			Type:      domain.TaskEventFailed,
			Message:   message,
			CreatedAt: time.Now(),
		})
	}
}

func (r *RecoveryScanner) failRetryExhausted(ctx context.Context, task *domain.Task, node *domain.NodeRuntime, cause error) {
	if task == nil || cause == nil {
		return
	}

	now := time.Now()
	step := "task"
	if node != nil {
		step = node.Name
		node.State = domain.NodeFailed
		node.Error = cause.Error()
		node.FinishedAt = &now
		node.LastHeartbeat = nil
		if err := r.nodeRepo.Update(ctx, node); err != nil {
			log.Printf("recovery mark retry-exhausted node failed failed: task=%d node=%s err=%v\n", task.ID, node.Name, err)
		}
	}

	task.Status = domain.TaskFailed
	task.ErrorMessage = cause.Error()
	if err := r.taskRepo.Update(ctx, task); err != nil {
		log.Printf("recovery mark retry-exhausted task failed failed: task=%d err=%v\n", task.ID, err)
		return
	}

	if r.eventBus != nil {
		r.eventBus.Publish(task.RootID, &domain.TaskEvent{
			TaskID:     task.ID,
			RootTaskID: task.RootID,
			Step:       step,
			Type:       domain.TaskEventFailed,
			Message:    "任务自动恢复次数耗尽",
			Error:      cause.Error(),
			CreatedAt:  time.Now(),
		})
	}

	log.Printf("recovery scanner marked retry-exhausted task failed: task=%d retry_count=%d\n", task.ID, task.RetryCount)
}

func (r *RecoveryScanner) requeuePendingEdgesTask(ctx context.Context, task *domain.Task, node *domain.NodeRuntime) error {
	if task == nil || node == nil {
		return nil
	}

	if task.Status == domain.TaskSuspended {
		task.Status = domain.TaskPending
		task.ErrorMessage = ""
		task.WorkerID = ""
		task.StartedAt = time.Time{}
		if err := r.taskRepo.Update(ctx, task); err != nil {
			return err
		}
	}

	if err := r.taskRepo.Enqueue(ctx, task.ID); err != nil {
		return err
	}

	log.Printf("recovery scanner re-enqueued pending-edges task=%d node=%s state=%s\n", task.ID, node.Name, node.State)
	return nil
}
