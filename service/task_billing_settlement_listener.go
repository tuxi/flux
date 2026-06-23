package service

import (
	"context"
	"log"
	"math"

	"flux/domain"
	"flux/eventbus"
	"flux/repository"
)

type TaskBillingSettlementListener struct {
	billingTaskSvc BillingTaskService
	taskRepo       repository.TaskRepository
	bus            *eventbus.EventBus
}

func NewTaskBillingSettlementListener(
	billingTaskSvc BillingTaskService,
	bus *eventbus.EventBus,
	taskRepo repository.TaskRepository,
) *TaskBillingSettlementListener {
	return &TaskBillingSettlementListener{
		billingTaskSvc: billingTaskSvc,
		taskRepo:       taskRepo,
		bus:            bus,
	}
}

func (l *TaskBillingSettlementListener) Start(ctx context.Context, bus *eventbus.EventBus) {
	if l == nil || l.billingTaskSvc == nil || bus == nil {
		return
	}

	l.startSubscriber(ctx, bus, domain.TaskEventSucceeded, l.handleSucceeded)
	l.startSubscriber(ctx, bus, domain.TaskEventFinalFailed, l.handleFinalFailed)
}

func (l *TaskBillingSettlementListener) startSubscriber(
	ctx context.Context,
	bus *eventbus.EventBus,
	eventType string,
	handler func(context.Context, *domain.TaskEvent),
) {
	ch := bus.Subscribe(eventType)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt := <-ch:
				if evt == nil {
					continue
				}
				handler(context.Background(), evt)
			}
		}
	}()
}

func (l *TaskBillingSettlementListener) handleSucceeded(ctx context.Context, evt *domain.TaskEvent) {
	if evt == nil || evt.TaskID <= 0 {
		return
	}

	actualDurationSec := l.resolveActualDurationSec(ctx, evt.TaskID)
	if err := l.billingTaskSvc.SettleTaskSuccessWithDuration(ctx, evt.TaskID, actualDurationSec); err != nil {
		log.Printf("task billing settle success failed: task=%d event=%s err=%v", evt.TaskID, evt.Type, err)
	}
}

// resolveActualDurationSec fetches the task output and returns the actual video duration in whole seconds.
// Returns 0 if the duration cannot be determined (caller will fall back to estimated points).
func (l *TaskBillingSettlementListener) resolveActualDurationSec(ctx context.Context, taskID int64) int {
	if l.taskRepo == nil {
		return 0
	}
	task, err := l.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil || len(task.OutputJSON) == 0 {
		return 0
	}
	output, err := domain.ParseFinal(task.OutputJSON)
	if err != nil || output == nil || output.Duration == nil || *output.Duration <= 0 {
		return 0
	}
	return int(math.Ceil(*output.Duration))
}

func (l *TaskBillingSettlementListener) handleFinalFailed(ctx context.Context, evt *domain.TaskEvent) {
	if evt == nil || evt.TaskID <= 0 {
		return
	}
	reason := "task final failed"
	if evt.Message != "" {
		reason = evt.Message
	}
	if err := l.billingTaskSvc.SettleTaskFailure(ctx, evt.TaskID, reason); err != nil {
		log.Printf("task billing settle failure failed: task=%d event=%s err=%v", evt.TaskID, evt.Type, err)
		l.publishBillingEvent(evt, domain.TaskEventPointsRefundFailed, err.Error())
		return
	}
	l.publishBillingEvent(evt, domain.TaskEventPointsRefunded, "points refunded")
}

func (l *TaskBillingSettlementListener) publishBillingEvent(evt *domain.TaskEvent, eventType, message string) {
	if l.bus == nil {
		return
	}
	l.bus.Publish(evt.RootTaskID, &domain.TaskEvent{
		TaskID:     evt.TaskID,
		RootTaskID: evt.RootTaskID,
		Type:       eventType,
		Message:    message,
	})
}
