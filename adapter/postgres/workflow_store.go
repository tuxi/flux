package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"flux/domain"
	"flux/repository"
	"flux/runtime"
	"flux/store"
)

// WorkflowStore 实现 store.WorkflowStore，内部委托给现有的 GORM repository 层。
// 这是 DreamAI PostgreSQL → flux v3 Store 接口的适配器。
type WorkflowStore struct {
	nodeRepo repository.NodeRuntimeRepository
	taskRepo repository.TaskRepository

	// 运行时 Plan 缓存（GORM 层不存 runtime.Plan，只存 domain 类型）
	mu    sync.RWMutex
	plans map[string]*runtime.Plan // taskID → plan
}

var _ store.WorkflowStore = (*WorkflowStore)(nil)

// NewWorkflowStore 创建一个 PostgreSQL-backed WorkflowStore。
func NewWorkflowStore(nodeRepo repository.NodeRuntimeRepository, taskRepo repository.TaskRepository) *WorkflowStore {
	return &WorkflowStore{
		nodeRepo: nodeRepo,
		taskRepo: taskRepo,
		plans:    make(map[string]*runtime.Plan),
	}
}

func (s *WorkflowStore) CreateRun(ctx context.Context, meta store.RunMeta) (*store.WorkflowRun, error) {
	task := &domain.Task{
		WorkflowVersionID: 0, // v3: 不绑定 workflow version
		Status:            domain.TaskRunning,
	}
	if err := s.taskRepo.Create(ctx, task); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	return &store.WorkflowRun{
		ID:             strconv.FormatInt(task.ID, 10),
		ConversationID: meta.ConversationID,
		Goal:           meta.Goal,
		Status:         "running",
	}, nil
}

func (s *WorkflowStore) LoadRun(ctx context.Context, runID string) (*store.WorkflowRun, error) {
	id, err := strconv.ParseInt(runID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid run id: %w", err)
	}
	task, err := s.taskRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load run: %w", err)
	}
	return &store.WorkflowRun{
		ID:     strconv.FormatInt(task.ID, 10),
		Status: string(task.Status),
	}, nil
}

func (s *WorkflowStore) UpdateRunStatus(ctx context.Context, runID string, status string) error {
	id, err := strconv.ParseInt(runID, 10, 64)
	if err != nil {
		return err
	}
	task, err := s.taskRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	task.Status = domain.TaskStatus(status)
	return s.taskRepo.Update(ctx, task)
}

func (s *WorkflowStore) CreateTask(ctx context.Context, runID string, meta store.TaskMeta) (*store.Task, error) {
	parentID, _ := strconv.ParseInt(meta.ParentID, 10, 64)
	rootID, _ := strconv.ParseInt(meta.RootID, 10, 64)
	task := &domain.Task{
		ParentID: &parentID,
		RootID:   rootID,
		Status:       domain.TaskRunning,
	}
	if err := s.taskRepo.Create(ctx, task); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	return &store.Task{
		ID:       strconv.FormatInt(task.ID, 10),
		RunID:    runID,
		ParentID: meta.ParentID,
		RootID:   meta.RootID,
		Status:   "running",
	}, nil
}

func (s *WorkflowStore) LoadTask(ctx context.Context, taskID string) (*store.Task, error) {
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		return nil, err
	}
	task, err := s.taskRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return domainToStoreTask(task), nil
}

func (s *WorkflowStore) ListTasks(ctx context.Context, runID string) ([]store.Task, error) {
	id, _ := strconv.ParseInt(runID, 10, 64)
	tasks, err := s.taskRepo.ListByParent(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]store.Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, *domainToStoreTask(t))
	}
	return out, nil
}

func (s *WorkflowStore) PersistNode(ctx context.Context, taskID string, nodeName string, state runtime.NodeState, output map[string]any) error {
	id, _ := strconv.ParseInt(taskID, 10, 64)

	// 先查是否已存在该 node
	existing, _ := s.nodeRepo.FindByTaskIDAndNode(ctx, id, nodeName)
	if existing != nil {
		// Update
		existing.State = runtimeToDomainState(state)
		existing.Output = output
		return s.nodeRepo.Update(ctx, existing)
	}

	// Create
	n := &domain.NodeRuntime{
		TaskID:  id,
		Name:    nodeName,
		State:   runtimeToDomainState(state),
		Output:  output,
	}
	return s.nodeRepo.Create(ctx, n)
}

func (s *WorkflowStore) LoadNodeStates(ctx context.Context, taskID string) ([]store.NodeRecord, error) {
	id, _ := strconv.ParseInt(taskID, 10, 64)
	nodes, err := s.nodeRepo.FindByTaskID(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]store.NodeRecord, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, store.NodeRecord{
			NodeName: n.Name,
			State:    domainToRuntimeState(n.State),
			Output:   n.Output,
			Error:    n.Error,
		})
	}
	return out, nil
}

func (s *WorkflowStore) SavePlan(ctx context.Context, taskID string, plan *runtime.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[taskID] = plan
	// Plan 序列化为 JSON 存入 checkpoint？
	// 当前最小实现：内存缓存 + 后续可落 NodeRuntime.Checkpoint
	return nil
}

func (s *WorkflowStore) LoadPlan(ctx context.Context, taskID string) (*runtime.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.plans[taskID], nil
}

// ── 类型转换 ──

func runtimeToDomainState(s runtime.NodeState) domain.NodeState {
	switch s {
	case runtime.NodePending:
		return domain.NodePending
	case runtime.NodeRunning:
		return domain.NodeRunning
	case runtime.NodeAwaiting:
		return domain.NodeAwaiting
	case runtime.NodeSuccess:
		return domain.NodeSuccess
	case runtime.NodeFailed:
		return domain.NodeFailed
	case runtime.NodeSkipped:
		return domain.NodeSkipped
	default:
		return domain.NodePending
	}
}

func domainToRuntimeState(s domain.NodeState) runtime.NodeState {
	switch s {
	case domain.NodePending:
		return runtime.NodePending
	case domain.NodeRunning, domain.NodeReady, domain.NodeRetrying:
		return runtime.NodeRunning
	case domain.NodeAwaiting:
		return runtime.NodeAwaiting
	case domain.NodeSuccess, domain.NodeSuccessPendingEdges:
		return runtime.NodeSuccess
	case domain.NodeFailed, domain.NodeFailedPendingEdges:
		return runtime.NodeFailed
	case domain.NodeSkipped, domain.NodeCanceled:
		return runtime.NodeSkipped
	default:
		return runtime.NodePending
	}
}

func domainToStoreTask(t *domain.Task) *store.Task {
	st := &store.Task{
		ID:     strconv.FormatInt(t.ID, 10),
		Status: string(t.Status),
	}
	if t.ParentID != nil {
		st.ParentID = strconv.FormatInt(*t.ParentID, 10)
	}
	if t.RootID > 0 {
		st.RootID = strconv.FormatInt(t.RootID, 10)
	}
	return st
}

// Ensure json import is used.
var _ = json.Marshal
