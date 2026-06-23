package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"flux/definition"
	"flux/domain"
	engine2 "flux/engine"
	"flux/eventbus"
	"flux/pkg/lock"
	repository2 "flux/repository"
	"flux/repository/query"
	"flux/service"
	workflow2 "flux/workflow"
	"flux/workflow/nodes"
	"github.com/google/uuid"
)

/*
worker  Task Scheduler 负责任务调度

负责：
	•	从队列拿任务
	•	调用 Engine 执行
	•	控制并发

关系：
Worker 负责拿任务
      ↓
调用 Workflow Engine 执行
*/

type Worker struct {
	eng     *engine2.Engine
	runTask func(ctx context.Context, task *domain.Task, def *definition.WorkflowDefinition) engine2.RunResult

	taskRepo            repository2.TaskRepository
	nodeRunTimeRepo     repository2.NodeRuntimeRepository
	workflowVersionRepo repository2.WorkflowVersionRepository
	workflowRepo        repository2.WorkflowRepository
	// 任务队列
	queue repository2.TaskQueue
	// 注册节点系统
	nodeRegistry *nodes.NodeRegistry
	scanner      RecoveryScanner
	// 节点任务队列
	asyncJobQueue engine2.AsyncJobQueue
	eventBus      *eventbus.EventBus
	builder       *workflow2.Builder
	workerID      string

	taskRetryService service.TaskRetryService

	dLocker lock.DistributedLock // 分布式锁
}

const maxAutoRetryCount = 1

func NewWorker(
	eng *engine2.Engine,
	taskRepo repository2.TaskRepository,
	nodeRunTimeRepo repository2.NodeRuntimeRepository,
	workflowVersionRepo repository2.WorkflowVersionRepository,
	workflowRepo repository2.WorkflowRepository,
	queue repository2.TaskQueue,
	asyncJobQueue engine2.AsyncJobQueue,
	eventBus *eventbus.EventBus,
	nodeRegistry *nodes.NodeRegistry,
	dLocker lock.DistributedLock,
	builder *workflow2.Builder,
	taskRetryService service.TaskRetryService,
) *Worker {

	scanner := NewRecoveryScanner(taskRepo, nodeRunTimeRepo, taskRetryService, eventBus, 30*time.Second, 2*time.Minute)
	scanner.Start(context.Background())

	w := &Worker{
		eng:                 eng,
		runTask:             eng.RunWithResult,
		taskRepo:            taskRepo,
		nodeRunTimeRepo:     nodeRunTimeRepo,
		workflowVersionRepo: workflowVersionRepo,
		workflowRepo:        workflowRepo,
		queue:               queue,
		nodeRegistry:        nodeRegistry,
		asyncJobQueue:       asyncJobQueue,
		eventBus:            eventBus,
		workerID:            uuid.New().String(),
		dLocker:             dLocker,
		builder:             builder,
		taskRetryService:    taskRetryService,
	}

	return w
}

// TaskQueueRecovery Worker 崩溃或者进程重启后
func TaskQueueRecovery(queue *query.RedisQueue, ctx context.Context) {
	for {
		queue.Recover(ctx)
		time.Sleep(30 * time.Second) // 每 30 秒检查一次
	}
}

func (w *Worker) Loop(ctx context.Context) {
	for {
		// 从缓存队列中查找待执行的任务
		taskID, err := w.queue.PopAndReserve(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		// 抢占任务
		ok, err := w.taskRepo.TryClaimTask(ctx, taskID, w.workerID)
		if err != nil {
			log.Println("task already claimed:", taskID)
			continue
		}
		if !ok {
			// 说明任务已经被别的worker 抢走
			_ = w.queue.Ack(ctx, taskID)
			continue
		}

		task, err := w.taskRepo.GetByID(ctx, taskID)
		if err != nil || task == nil || task.Status == domain.TaskCanceled {
			log.Printf("worker load claimed task failed or task canceled: task=%d err=%v task_nil=%v", taskID, err, task == nil)
			// 直接 ACK，丢弃任务
			w.queue.Ack(ctx, taskID)
			continue
		}

		log.Println("worker start task:", taskID)

		w.handle(ctx, task)
	}
}

func (w *Worker) handle(parentCtx context.Context, task *domain.Task) {

	ctxTimeout, cancel := context.WithTimeout(parentCtx, 10*time.Minute)
	defer cancel()

	dbVersion, err := w.workflowVersionRepo.Get(ctxTimeout, task.WorkflowVersionID)
	if err != nil {
		log.Printf("worker load workflow version failed: task=%d workflow_version_id=%d err=%v", task.ID, task.WorkflowVersionID, err)
		w.failTask(parentCtx, task, err.Error(), "worker_prepare_failed", "worker.load_workflow_version")
		return
	}

	var def definition.WorkflowDefinition
	if err := json.Unmarshal(dbVersion.DefinitionJSON, &def); err != nil {
		log.Printf("worker unmarshal workflow definition failed: task=%d workflow_version_id=%d err=%v", task.ID, task.WorkflowVersionID, err)
		w.failTask(parentCtx, task, err.Error(), "worker_prepare_failed", "worker.unmarshal_workflow_definition")
		return
	}

	// engine 运行任务
	log.Printf("worker execute task: task=%d workflow=%s version=%d", task.ID, def.Name, dbVersion.ID)
	taskID := task.ID
	result := w.runTask(ctxTimeout, task, &def)
	switch result.Status {
	case engine2.RunSuccess:
		_ = w.queue.Ack(parentCtx, taskID)
		return
	case engine2.RunSuspended:
		_ = w.queue.Ack(parentCtx, taskID)
		return
	case engine2.RunNoop:
		_ = w.queue.Ack(parentCtx, taskID)
		return
	}

	log.Printf("worker execute task failed: task=%d workflow=%s retry_count=%d err=%v", taskID, def.Name, task.RetryCount, result.Err)

	// 失败重试
	task.RetryCount++
	if result.Err != nil {
		task.ErrorMessage = result.Err.Error()
	}
	if task.RetryCount >= maxAutoRetryCount {
		w.failTask(parentCtx, task, task.ErrorMessage, "retry_exhausted", "worker.retry_limit")
		return
	}

	if w.taskRetryService != nil {
		if retryErr := w.taskRetryService.PrepareTaskRetry(parentCtx, taskID, service.RetryTriggerRecovery, "", nil); retryErr != nil {
			log.Printf("worker prepare retry failed: task=%d retry_count=%d err=%v", taskID, task.RetryCount, retryErr)
			w.failTask(parentCtx, task, retryErr.Error(), "retry_prepare_failed", "worker.prepare_retry")
			return
		}
	}

	task.Status = domain.TaskPending
	_ = w.taskRepo.Update(parentCtx, task)
	_ = w.queue.Push(parentCtx, taskID)
	_ = w.queue.Ack(parentCtx, taskID)
}

func (w *Worker) failTask(ctx context.Context, task *domain.Task, reason string, finalReason string, source string) {
	if task == nil {
		return
	}

	task.Status = domain.TaskFailed
	task.ErrorMessage = reason
	if err := w.taskRepo.Update(ctx, task); err != nil {
		log.Printf("worker persist failed task status failed: task=%d err=%v", task.ID, err)
	}
	w.publishFinalFailed(task, reason, finalReason, source)

	_ = w.queue.MoveToDead(ctx, task.ID)
	_ = w.queue.Ack(ctx, task.ID)
}

func (w *Worker) publishFinalFailed(task *domain.Task, reason string, finalReason string, source string) {
	if w == nil || w.eventBus == nil || task == nil {
		return
	}

	w.eventBus.Publish(task.RootID, &domain.TaskEvent{
		TaskID:     task.ID,
		RootTaskID: task.RootID,
		Step:       "task",
		Type:       domain.TaskEventFinalFailed,
		Message:    "任务最终失败",
		Error:      reason,
		Meta: map[string]any{
			"final_reason":      finalReason,
			"error_message":     reason,
			"retry_count":       task.RetryCount,
			"retry_limit":       maxAutoRetryCount,
			"retry_exhausted":   task.RetryCount >= maxAutoRetryCount,
			"source":            source,
			"last_run_status":   string(domain.TaskFailed),
			"can_manual_resume": true,
			"billing_action":    "refund",
		},
		CreatedAt: time.Now(),
	})
}
