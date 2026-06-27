package flux

import (
	"context"

	"flux/runtime"
	"flux/store"
)

// storeTraceSink 将 runtime.TraceSink 的事件实时桥接到 store.TraceStore。
// 每个 TraceEvent 接收时立即写入，保证事件顺序和持久化。
type storeTraceSink struct {
	store  store.TraceStore
	taskID string
}

func (s *storeTraceSink) EmitExecution(e runtime.TraceEvent) {
	// 每次写入单条 event。调用方（Scheduler）保证顺序。
	_ = s.store.AppendTrace(context.Background(), s.taskID, []runtime.TraceEvent{e})
}

func (s *storeTraceSink) EmitControl(e runtime.TraceEvent) {
	_ = s.store.AppendTrace(context.Background(), s.taskID, []runtime.TraceEvent{e})
}

var _ runtime.TraceSink = (*storeTraceSink)(nil)
