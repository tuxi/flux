package worker

import (
	"context"
	"flux/domain"
	"flux/eventbus"
	"flux/repository"
	"flux/tool"
	"time"
)

type AsyncEmitter struct {
	eventBus *eventbus.EventBus
	taskID   int64
	nodeName string
	taskRepo repository.TaskRepository
}

func (e *AsyncEmitter) EmitToolEvent(event tool.ToolEvent) {

	message := event.Message

	errMsg := ""
	if v, ok := event.Data["error"].(string); ok {
		errMsg = v
	}

	task, err := e.taskRepo.GetByID(context.Background(), e.taskID)
	if err != nil {
		return
	}

	eventType := event.CustomType
	eventGrade := domain.GradePersistent
	if eventType == "" {
		eventType = "tool_" + event.Type
		switch event.Type {
		case "stream", "stream_end", "progress", "log":
			eventGrade = domain.GradeTransient
		default:
			eventGrade = domain.GradePersistent
		}
	}

	e.eventBus.Publish(
		e.taskID,
		&domain.TaskEvent{
			TaskID:     e.taskID,
			RootTaskID: task.RootID,
			Step:       e.nodeName,
			Type:       eventType,
			Grade:      eventGrade,
			Message:    message,
			Error:      errMsg,
			Progress:   event.Progress,
			Meta:       event.Data,
			CreatedAt:  time.Now(),
			Level:      event.LogLevel,
		})

}
