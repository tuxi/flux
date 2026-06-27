package memory

import (
	"context"
	"sync"

	"flux/runtime"
	"flux/store"
)

// TraceStore 是 TraceStore 的纯内存实现。用于测试。
type TraceStore struct {
	mu     sync.RWMutex
	events map[string][]runtime.TraceEvent // key: taskID
}

var _ store.TraceStore = (*TraceStore)(nil)

func NewTraceStore() *TraceStore {
	return &TraceStore{
		events: make(map[string][]runtime.TraceEvent),
	}
}

func (s *TraceStore) AppendTrace(_ context.Context, taskID string, events []runtime.TraceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[taskID] = append(s.events[taskID], events...)
	return nil
}

func (s *TraceStore) ReplayTrace(_ context.Context, taskID string, sinceSeq int64) ([]runtime.TraceEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.events[taskID]
	var out []runtime.TraceEvent
	for _, ev := range all {
		if ev.Seq > sinceSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}
