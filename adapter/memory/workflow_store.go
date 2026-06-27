package memory

import (
	"context"
	"fmt"
	"sync"

	"flux/runtime"
	"flux/store"
)

// WorkflowStore 是 WorkflowStore 的纯内存实现。用于测试。
type WorkflowStore struct {
	mu    sync.RWMutex
	runs  map[string]*store.WorkflowRun
	tasks map[string]*store.Task
	nodes map[string][]store.NodeRecord // key: taskID
	plans map[string]*runtime.Plan      // key: taskID
}

var _ store.WorkflowStore = (*WorkflowStore)(nil)

func NewWorkflowStore() *WorkflowStore {
	return &WorkflowStore{
		runs:  make(map[string]*store.WorkflowRun),
		tasks: make(map[string]*store.Task),
		nodes: make(map[string][]store.NodeRecord),
		plans: make(map[string]*runtime.Plan),
	}
}

func (s *WorkflowStore) CreateRun(_ context.Context, meta store.RunMeta) (*store.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run := &store.WorkflowRun{
		ID:             meta.ID,
		ConversationID: meta.ConversationID,
		Goal:           meta.Goal,
		Status:         "running",
	}
	s.runs[run.ID] = run
	return run, nil
}

func (s *WorkflowStore) LoadRun(_ context.Context, runID string) (*store.WorkflowRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	return run, nil
}

func (s *WorkflowStore) UpdateRunStatus(_ context.Context, runID string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run %q not found", runID)
	}
	run.Status = status
	return nil
}

func (s *WorkflowStore) CreateTask(_ context.Context, runID string, meta store.TaskMeta) (*store.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := &store.Task{
		ID:       meta.ID,
		RunID:    runID,
		ParentID: meta.ParentID,
		RootID:   meta.RootID,
		Status:   "running",
	}
	s.tasks[task.ID] = task
	return task, nil
}

func (s *WorkflowStore) LoadTask(_ context.Context, taskID string) (*store.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %q not found", taskID)
	}
	return task, nil
}

func (s *WorkflowStore) ListTasks(_ context.Context, runID string) ([]store.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []store.Task
	for _, t := range s.tasks {
		if t.RunID == runID {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (s *WorkflowStore) PersistNode(_ context.Context, taskID string, nodeName string, state runtime.NodeState, output map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	recs := s.nodes[taskID]
	// Update existing or append.
	for i, r := range recs {
		if r.NodeName == nodeName {
			recs[i] = store.NodeRecord{
				NodeName: nodeName,
				State:    state,
				Output:   output,
			}
			s.nodes[taskID] = recs
			return nil
		}
	}
	s.nodes[taskID] = append(recs, store.NodeRecord{
		NodeName: nodeName,
		State:    state,
		Output:   output,
	})
	return nil
}

func (s *WorkflowStore) LoadNodeStates(_ context.Context, taskID string) ([]store.NodeRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.nodes[taskID], nil
}

func (s *WorkflowStore) SavePlan(_ context.Context, taskID string, plan *runtime.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.plans[taskID] = plan
	return nil
}

func (s *WorkflowStore) LoadPlan(_ context.Context, taskID string) (*runtime.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.plans[taskID], nil
}
