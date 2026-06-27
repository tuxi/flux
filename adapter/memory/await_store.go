package memory

import (
	"context"
	"sync"

	"flux/store"
)

// AwaitStore 是 AwaitStore 的纯内存实现。用于测试。
type AwaitStore struct {
	mu       sync.Mutex
	bindings map[string]*store.AwaitBinding            // key: bindingID
	byProv   map[string]string                         // key: providerTaskID → bindingID
	byTask   map[string]map[string]*store.AwaitBinding // key: taskID → bindingID → binding
}

var _ store.AwaitStore = (*AwaitStore)(nil)

func NewAwaitStore() *AwaitStore {
	return &AwaitStore{
		bindings: make(map[string]*store.AwaitBinding),
		byProv:   make(map[string]string),
		byTask:   make(map[string]map[string]*store.AwaitBinding),
	}
}

func (s *AwaitStore) CreateBinding(_ context.Context, binding store.AwaitBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := binding // copy
	s.bindings[b.BindingID] = &b

	if b.ProviderTaskID != "" {
		s.byProv[b.ProviderTaskID] = b.BindingID
	}
	if s.byTask[b.TaskID] == nil {
		s.byTask[b.TaskID] = make(map[string]*store.AwaitBinding)
	}
	s.byTask[b.TaskID][b.BindingID] = &b

	return nil
}

func (s *AwaitStore) ResolveBinding(_ context.Context, bindingID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.bindings[bindingID]
	if !ok {
		return false, nil
	}
	if b.Status != store.AwaitStatusAwaiting {
		return false, nil // 已被其他线程 resolve
	}
	b.Status = store.AwaitStatusCompleted
	return true, nil
}

func (s *AwaitStore) FindByProviderTaskID(_ context.Context, providerTaskID string) (*store.AwaitBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bindingID, ok := s.byProv[providerTaskID]
	if !ok {
		return nil, nil
	}
	b, ok := s.bindings[bindingID]
	if !ok {
		return nil, nil
	}
	return b, nil
}

func (s *AwaitStore) ListPending(_ context.Context) ([]store.AwaitBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []store.AwaitBinding
	for _, b := range s.bindings {
		if b.Status == store.AwaitStatusAwaiting {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (s *AwaitStore) ListByTask(_ context.Context, taskID string) ([]store.AwaitBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []store.AwaitBinding
	if m, ok := s.byTask[taskID]; ok {
		for _, b := range m {
			out = append(out, *b)
		}
	}
	return out, nil
}
