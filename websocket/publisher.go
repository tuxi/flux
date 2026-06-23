package websocket

import "fmt"

type Channel string

// EventPublisher 事件发布器接口
type EventPublisher interface {
	Publish(ch Channel, event any)
}

func TaskChannel(taskID int64) Channel {
	return Channel(fmt.Sprintf("task:%d", taskID))
}

// ConversationChannel is the room for an Agent conversation's live message feed.
func ConversationChannel(conversationID int64) Channel {
	return Channel(fmt.Sprintf("conversation:%d", conversationID))
}

func WorkflowChannel(id int64) Channel {
	return Channel(fmt.Sprintf("workflow:%d", id))
}

func WorkerChannel(id string) Channel {
	return Channel("worker:" + id)
}

func QueueChannel(name string) Channel {
	return Channel("queue:" + name)
}

func LogsChannel(taskID int64) Channel {
	return Channel(fmt.Sprintf("logs:%d", taskID))
}
