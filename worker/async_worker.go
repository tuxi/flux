package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"time"

	"flux/domain"
	"flux/engine"
	"flux/eventbus"
	"flux/repository"
	"flux/tool"
	"github.com/redis/go-redis/v9"
)

// AsyncWorker 异步节点事件调度器
type AsyncWorker struct {
	queue           engine.AsyncJobQueue
	taskRepo        repository.TaskRepository
	nodeRuntimeRepo repository.NodeRuntimeRepository
	tools           *tool.Registry
	eventBus        *eventbus.EventBus
}

func NewAsyncWorker(
	queue engine.AsyncJobQueue,
	taskRepo repository.TaskRepository,
	nodeRuntimeRepo repository.NodeRuntimeRepository,
	tools *tool.Registry,
	eventBus *eventbus.EventBus,
) *AsyncWorker {
	return &AsyncWorker{
		queue:           queue,
		taskRepo:        taskRepo,
		nodeRuntimeRepo: nodeRuntimeRepo,
		tools:           tools,
		eventBus:        eventBus,
	}
}

func StartAsyncWorkers(
	ctx context.Context,
	worker *AsyncWorker,
	n int,
) {

	for i := 0; i < n; i++ {

		consumer := fmt.Sprintf("async-%d", i)

		go worker.Start(ctx, consumer)
	}
}

func (w *AsyncWorker) Start(ctx context.Context, consumer string) {

	for {

		job, msgID, err := w.queue.Consume(ctx, "workflow_group", consumer)
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				log.Printf("async worker consume failed: consumer=%s err=%v", consumer, err)
			}
			continue
		}

		err = w.handleJob(ctx, job)

		_ = w.queue.Ack(ctx, msgID)
	}
}

func (w *AsyncWorker) handleJob(ctx context.Context, job *engine.AsyncJob) (err error) {
	if job == nil {
		return fmt.Errorf("async job is nil")
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("async worker panic: %v", r)
			w.publishAsyncCompletion(job, map[string]any{
				"panic": string(debug.Stack()),
			}, err.Error())
		}
	}()

	w.publishJobEvent(context.Background(), job, "async_worker_received", "异步任务已被 worker 消费", nil, "")

	// 加载节点 runtime 并启动心跳，确保长时间执行的异步节点可被监控和恢复
	runtime, runtimeErr := w.nodeRuntimeRepo.FindByTaskIDAndNode(ctx, job.TaskID, job.Node)
	if runtimeErr != nil {
		err := fmt.Errorf("load async node runtime failed: task=%d node=%s: %w", job.TaskID, job.Node, runtimeErr)
		w.publishAsyncCompletion(job, nil, err.Error())
		return err
	}
	if runtime == nil {
		err := fmt.Errorf("async node runtime not found: task=%d node=%s", job.TaskID, job.Node)
		w.publishAsyncCompletion(job, nil, err.Error())
		return err
	}
	if runtime.State != domain.NodeRunning && runtime.State != domain.NodeRetrying {
		w.publishJobEvent(context.Background(), job, "async_job_stale", "异步任务已过期，跳过执行", map[string]any{
			"state": runtime.State,
		}, "")
		return nil
	}
	if job.Hash != "" && runtime.InputHash != "" && runtime.InputHash != job.Hash {
		w.publishJobEvent(context.Background(), job, "async_job_stale", "异步任务输入指纹已过期，跳过执行", map[string]any{
			"job_hash":     job.Hash,
			"runtime_hash": runtime.InputHash,
		}, "")
		return nil
	}

	if w.tools == nil {
		err := fmt.Errorf("tool registry is nil")
		w.publishAsyncCompletion(job, nil, err.Error())
		return err
	}
	toolImpl, ok := w.tools.Get(job.StepAdapter)
	if !ok {
		err := fmt.Errorf("tool not found: %s", job.StepAdapter)
		w.publishAsyncCompletion(job, nil, err.Error())
		return err
	}

	w.publishJobEvent(context.Background(), job, "async_tool_execute_start", "异步工具开始执行", map[string]any{
		"tool": job.StepAdapter,
	}, "")

	done := make(chan struct{})
	defer close(done)

	if runtime != nil {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					now := time.Now()
					runtime.LastHeartbeat = &now
					_ = w.nodeRuntimeRepo.Update(context.Background(), runtime)
				case <-done:
					return
				}
			}
		}()
	}

	emitter := &AsyncEmitter{
		eventBus: w.eventBus,
		taskID:   job.TaskID,
		nodeName: job.Node,
		taskRepo: w.taskRepo,
	}
	res, err := toolImpl.Execute(ctx, job.Input, emitter)

	if err != nil {
		w.publishAsyncCompletion(job, nil, err.Error())
		return err
	}
	if res == nil {
		err := fmt.Errorf("tool %s returned nil result", job.StepAdapter)
		w.publishAsyncCompletion(job, nil, err.Error())
		return err
	}

	meta := res.Data
	if aware, ok := toolImpl.(tool.UsageAware); ok && res != nil && res.Data != nil {
		if usageFacts, usageErr := aware.BuildUsageFacts(job.Input, res.Data); usageErr == nil && len(usageFacts) > 0 {
			meta = cloneAsyncMeta(res.Data)
			meta[engine.AwaitUsageFactsMetaKey] = usageFacts
		}
	}

	w.publishAsyncCompletion(job, meta, "")

	return nil
}

func (w *AsyncWorker) publishAsyncCompletion(job *engine.AsyncJob, meta map[string]any, errMsg string) {
	w.publishJobEvent(context.Background(), job, domain.TaskEventNodeCompleteAsync, "", meta, errMsg)
}

func (w *AsyncWorker) publishJobEvent(ctx context.Context, job *engine.AsyncJob, eventType string, message string, meta map[string]any, errMsg string) {
	if w == nil || w.eventBus == nil || job == nil {
		return
	}

	rootID := job.TaskID
	if w.taskRepo != nil {
		if task, err := w.taskRepo.GetByID(ctx, job.TaskID); err == nil && task != nil && task.RootID != 0 {
			rootID = task.RootID
		}
	}

	w.eventBus.Publish(rootID, &domain.TaskEvent{
		Step:       job.Node,
		TaskID:     job.TaskID,
		RootTaskID: rootID,
		Type:       eventType,
		Message:    message,
		Error:      errMsg,
		Meta:       meta,
		CreatedAt:  time.Now(),
	})
}

func cloneAsyncMeta(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
