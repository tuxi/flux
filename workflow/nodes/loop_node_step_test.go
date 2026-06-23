package nodes

import (
	"context"
	"encoding/json"
	"flux/definition"
	"flux/domain"
	"flux/dto"
	"flux/eventbus"
	repository2 "flux/repository"
	"flux/runtimekeys"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type loopFakeTaskRepo struct {
	tasks map[string]*domain.Task
}

func newLoopFakeTaskRepo(tasks ...*domain.Task) *loopFakeTaskRepo {
	repo := &loopFakeTaskRepo{tasks: map[string]*domain.Task{}}
	for _, task := range tasks {
		if task == nil || task.SubKey == nil {
			continue
		}
		cp := *task
		repo.tasks[*task.SubKey] = &cp
	}
	return repo
}

func (r *loopFakeTaskRepo) Create(ctx context.Context, task *domain.Task) error { return nil }
func (r *loopFakeTaskRepo) GetByID(ctx context.Context, id int64) (*domain.Task, error) {
	return nil, nil
}
func (r *loopFakeTaskRepo) Update(ctx context.Context, task *domain.Task) error { return nil }
func (r *loopFakeTaskRepo) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (r *loopFakeTaskRepo) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	return nil, nil
}
func (r *loopFakeTaskRepo) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (r *loopFakeTaskRepo) ListByUser(ctx context.Context, userID int64, params dto.PageRequest) ([]*domain.Task, int64, error) {
	return nil, 0, nil
}
func (r *loopFakeTaskRepo) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (r *loopFakeTaskRepo) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	return nil
}
func (r *loopFakeTaskRepo) Enqueue(ctx context.Context, taskID int64) error { return nil }
func (r *loopFakeTaskRepo) TryClaimTask(ctx context.Context, taskID int64, workerID string) (bool, error) {
	return true, nil
}
func (r *loopFakeTaskRepo) FindBySubKey(ctx context.Context, subKey string) (*domain.Task, error) {
	task := r.tasks[subKey]
	if task == nil {
		return nil, nil
	}
	cp := *task
	return &cp, nil
}
func (r *loopFakeTaskRepo) ListByParentNode(ctx context.Context, parentID int64, nodeName string) ([]*domain.Task, error) {
	var result []*domain.Task
	for _, task := range r.tasks {
		if task.ParentID == nil || *task.ParentID != parentID {
			continue
		}
		if task.ParentNode == nil || *task.ParentNode != nodeName {
			continue
		}
		cp := *task
		result = append(result, &cp)
	}
	return result, nil
}
func (r *loopFakeTaskRepo) CreateFork(ctx context.Context, source *domain.Task, newTaskID int64, newInput []byte, editAction, editLabel string) (*domain.Task, error) {
	return nil, fmt.Errorf("not implemented")
}
func (r *loopFakeTaskRepo) ListByUserV2(ctx context.Context, userID int64, req dto.TaskListReq) ([]*dto.Task, int64, error) {
	return nil, 0, nil
}
func (r *loopFakeTaskRepo) GetRootTaskByIDAndUser(ctx context.Context, taskID int64, userID int64) (*domain.Task, error) {
	return nil, nil
}
func (r *loopFakeTaskRepo) GetTaskDetail(ctx context.Context, taskID int64) (*dto.TaskDetail, error) {
	return nil, nil
}

type loopFakeNodeRepo struct {
	nodes   map[string]*domain.NodeRuntime
	updates int
}

func newLoopFakeNodeRepo(nodes ...*domain.NodeRuntime) *loopFakeNodeRepo {
	repo := &loopFakeNodeRepo{nodes: map[string]*domain.NodeRuntime{}}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		cp := *node
		cp.Output = cloneMapAny(node.Output)
		cp.Checkpoint = cloneMapAny(node.Checkpoint)
		cp.ResolvedInput = cloneMapAny(node.ResolvedInput)
		repo.nodes[node.Name] = &cp
	}
	return repo
}

func (r *loopFakeNodeRepo) Create(ctx context.Context, n *domain.NodeRuntime) error { return nil }
func (r *loopFakeNodeRepo) Update(ctx context.Context, n *domain.NodeRuntime) error {
	cp := *n
	cp.Output = cloneMapAny(n.Output)
	cp.Checkpoint = cloneMapAny(n.Checkpoint)
	cp.ResolvedInput = cloneMapAny(n.ResolvedInput)
	r.nodes[n.Name] = &cp
	r.updates++
	return nil
}
func (r *loopFakeNodeRepo) FindByTaskID(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error) {
	return nil, nil
}
func (r *loopFakeNodeRepo) FindByTaskIDAndNode(ctx context.Context, taskID int64, node string) (*domain.NodeRuntime, error) {
	if rt, ok := r.nodes[node]; ok {
		cp := *rt
		cp.Output = cloneMapAny(rt.Output)
		cp.Checkpoint = cloneMapAny(rt.Checkpoint)
		cp.ResolvedInput = cloneMapAny(rt.ResolvedInput)
		return &cp, nil
	}
	return nil, nil
}
func (r *loopFakeNodeRepo) MarkRunningAsRetrying(ctx context.Context, taskID int64) error { return nil }
func (r *loopFakeNodeRepo) MarkAsRetrying(ctx context.Context, taskID int64, name string) error {
	return nil
}
func (r *loopFakeNodeRepo) MarkFailed(ctx context.Context, taskID int64, name string, errMessage string) error {
	return nil
}
func (r *loopFakeNodeRepo) FindExpiredRunningNodes(ctx context.Context, expire time.Time) ([]*domain.NodeRuntime, error) {
	return nil, nil
}
func (r *loopFakeNodeRepo) AttemptCompletePendingEdges(ctx context.Context, taskID int64, nodeName string, output map[string]any, errMsg string) (bool, error) {
	return false, nil
}
func (r *loopFakeNodeRepo) CloneCheckpoint(ctx context.Context, fromTaskID, toTaskID int64) error {
	return nil
}

type loopFakeExecutor struct {
	taskRepo       repository2.TaskRepository
	nodeRepo       repository2.NodeRuntimeRepository
	runInputs      []map[string]any
	runWorkflowIDs []string
}

func (e *loopFakeExecutor) RunSubWorkflow(execCtx *NodeExecContext, workflowName string, input map[string]any) (map[string]any, error) {
	e.runWorkflowIDs = append(e.runWorkflowIDs, workflowName)
	e.runInputs = append(e.runInputs, cloneMapAny(input))
	return nil, &domain.WorkflowSuspendedError{Reason: domain.SuspendSubWorkflow}
}

func (e *loopFakeExecutor) TaskRepo() repository2.TaskRepository { return e.taskRepo }
func (e *loopFakeExecutor) NodeRepo() repository2.NodeRuntimeRepository {
	return e.nodeRepo
}

func newLoopExecContext(
	t *testing.T,
	taskRepo repository2.TaskRepository,
	nodeRepo repository2.NodeRuntimeRepository,
	runtime *domain.NodeRuntime,
	input map[string]any,
) *NodeExecContext {
	t.Helper()

	def := &definition.WorkflowDefinition{
		Name: "loop_test_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.loop_render.output.results[0].primary_file_url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name: "loop_render",
				Type: definition.NodeLoop,
				Config: map[string]any{
					"items":    "input.items",
					"iterator": "shot",
					"workflow": "loop_child",
				},
			},
			{Name: "end", Type: definition.NodeEnd},
		},
	}

	ctx := &Context{
		Ctx:      context.Background(),
		Task:     &domain.Task{ID: 1, RootID: 1},
		Workflow: def,
		Input:    cloneMapAny(input),
		Output: map[string]any{
			"input": cloneMapAny(input),
			"nodes": map[string]any{},
		},
		Runtime: map[string]*domain.NodeRuntime{
			"loop_render": runtime,
		},
		EventBus: eventbus.NewEventBus(nil, nil),
	}
	ctx.EnsureOutputInitialized()

	return &NodeExecContext{
		TaskContext: ctx,
		Input:       cloneMapAny(input),
		Output:      map[string]any{},
		NodeDef: &definition.NodeDefinition{
			Name:   "loop_render",
			Type:   definition.NodeLoop,
			Config: map[string]any{"workflow": "loop_child"},
		},
		Executor: &loopFakeExecutor{
			taskRepo: taskRepo,
			nodeRepo: nodeRepo,
		},
	}
}

func TestLoopRun_InitialDispatchSetsRunningBindingAndCarryState(t *testing.T) {
	step := NewLoopStep(
		"input.items",
		"shot",
		"loop_child",
		map[string]string{"seed_image": "primary_file_url"},
		map[string]any{"seed_image": "input.seed_image"},
	)
	runtime := &domain.NodeRuntime{Name: "loop_render", State: domain.NodePending}
	nodeRepo := newLoopFakeNodeRepo(runtime)
	execCtx := newLoopExecContext(t, newLoopFakeTaskRepo(), nodeRepo, runtime, map[string]any{
		"items":      []any{"shot-a", "shot-b"},
		"seed_image": "frame-0.png",
		"workflow":   "should_not_flow_to_child",
	})

	err := step.Run(execCtx)
	var suspendErr *domain.WorkflowSuspendedError
	require.ErrorAs(t, err, &suspendErr)

	stored := nodeRepo.nodes["loop_render"]
	require.NotNil(t, stored.Checkpoint)
	require.Equal(t, 0, stored.Checkpoint[loopCPCurrentIndex])
	require.Equal(t, 0, stored.Checkpoint[loopCPRunningIndex])
	require.NotEmpty(t, stored.Checkpoint[loopCPRunningSubKey])

	carryState, ok := stored.Checkpoint[loopCPCarryState].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "frame-0.png", carryState["seed_image"])

	fakeExec := execCtx.Executor.(*loopFakeExecutor)
	require.Len(t, fakeExec.runInputs, 1)
	subInput := fakeExec.runInputs[0]
	require.Equal(t, "shot-a", subInput["shot"])
	require.Equal(t, 0, subInput["index"])
	require.Equal(t, "frame-0.png", subInput["seed_image"])
	_, hasWorkflowKey := subInput["workflow"]
	require.False(t, hasWorkflowKey)
	require.Equal(t, stored.Checkpoint[loopCPRunningSubKey], buildExpectedLoopSubKey(t, execCtx, step.workflow, subInput))
}

func TestLoopEmitFanoutProgressSummaryOnly(t *testing.T) {
	step := NewLoopStep("input.items", "shot", "loop_child", nil, nil)
	runtime := &domain.NodeRuntime{
		Name:  "loop_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			loopCPTotal:        8,
			loopCPDone:         3,
			loopCPCurrentIndex: 4,
			loopCPRunningIndex: 4,
			loopCPResults:      []any{map[string]any{"large": "result"}},
			loopCPCarryState:   map[string]any{"secret": "state"},
		},
	}
	execCtx := newLoopExecContext(t, newLoopFakeTaskRepo(), newLoopFakeNodeRepo(runtime), runtime, map[string]any{
		"items": []any{"a", "b"},
	})
	ch := execCtx.TaskContext.EventBus.Subscribe(TaskEventFanoutProgress)

	step.emitFanoutProgress(execCtx, runtime)

	evt := <-ch
	require.Equal(t, TaskEventFanoutProgress, evt.Type)
	require.Equal(t, "loop", evt.Meta["fanout_kind"])
	require.Equal(t, 8, evt.Meta["total"])
	require.Equal(t, 3, evt.Meta["done"])
	require.Equal(t, 1, evt.Meta["running"])
	require.Equal(t, 5, evt.Meta["current_index"])
	require.NotContains(t, evt.Meta, loopCPResults)
	require.NotContains(t, evt.Meta, loopCPCarryState)
}

func TestLoopProcessRunningIteration_SuccessAdvancesStateAndCarry(t *testing.T) {
	step := NewLoopStep(
		"input.items",
		"shot",
		"loop_child",
		map[string]string{"seed_image": "primary_file_url"},
		nil,
	)

	subKey := "loop-sub-key"
	runtime := &domain.NodeRuntime{
		Name:  "loop_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			loopCPTotal:             2,
			loopCPCurrentIndex:      0,
			loopCPDone:              0,
			loopCPResults:           []any{},
			loopCPCarryState:        map[string]any{},
			loopCPRunningIndex:      0,
			loopCPRunningSubKey:     subKey,
			loopCPRunningAttempt:    "attempt-1",
			loopCPAttemptSeqByIndex: map[string]any{"0": 1},
		},
	}
	child := &domain.Task{
		ID:     100,
		Status: domain.TaskSuccess,
		SubKey: &subKey,
		OutputJSON: mustJSON(t, map[string]any{
			"final": map[string]any{
				"result_type":      "video",
				"primary_file_url": "https://example.com/shot-0.mp4",
			},
		}),
	}
	taskRepo := newLoopFakeTaskRepo(child)
	nodeRepo := newLoopFakeNodeRepo(runtime)
	execCtx := newLoopExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": []any{"shot-a", "shot-b"},
	})

	decision, err := step.processRunningIteration(execCtx, runtime)
	require.NoError(t, err)
	require.Equal(t, loopDecisionContinue, decision)

	stored := nodeRepo.nodes["loop_render"]
	require.Equal(t, 1, stored.Checkpoint[loopCPDone])
	require.Equal(t, 1, stored.Checkpoint[loopCPCurrentIndex])
	require.Equal(t, -1, stored.Checkpoint[loopCPRunningIndex])
	require.Equal(t, "", stored.Checkpoint[loopCPRunningSubKey])

	carryState, ok := stored.Checkpoint[loopCPCarryState].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/shot-0.mp4", carryState["seed_image"])

	results, ok := stored.Checkpoint[loopCPResults].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	result0, ok := results[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/shot-0.mp4", result0["primary_file_url"])
}

func TestLoopProcessRunningIteration_MissingChildKeepsSuspended(t *testing.T) {
	step := NewLoopStep("input.items", "shot", "loop_child", nil, nil)
	runtime := &domain.NodeRuntime{
		Name:  "loop_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			loopCPTotal:         1,
			loopCPCurrentIndex:  0,
			loopCPDone:          0,
			loopCPResults:       []any{},
			loopCPCarryState:    map[string]any{},
			loopCPRunningIndex:  0,
			loopCPRunningSubKey: "missing-child",
		},
	}
	nodeRepo := newLoopFakeNodeRepo(runtime)
	execCtx := newLoopExecContext(t, newLoopFakeTaskRepo(), nodeRepo, runtime, map[string]any{
		"items": []any{"shot-a"},
	})

	decision, err := step.processRunningIteration(execCtx, runtime)
	require.NoError(t, err)
	require.Equal(t, loopDecisionSuspend, decision)

	stored := nodeRepo.nodes["loop_render"]
	require.Equal(t, 0, stored.Checkpoint[loopCPRunningIndex])
	require.Equal(t, "missing-child", stored.Checkpoint[loopCPRunningSubKey])
	require.Equal(t, 0, nodeRepo.updates)
}

func TestLoopResolveAttemptToken_ReusesOrRecreatesByExecutionReason(t *testing.T) {
	step := NewLoopStep("input.items", "shot", "loop_child", nil, nil)

	cp := map[string]any{
		loopCPRunningAttempt:    "existing-attempt",
		loopCPAttemptSeqByIndex: map[string]any{"0": 1},
	}
	runtime := &domain.NodeRuntime{Name: "loop_render", ExecutionReason: ""}
	token := step.resolveAttemptToken(runtime, cp, 0)
	require.Equal(t, "existing-attempt", token)

	runtime.ExecutionReason = "resume_boundary"
	newToken := step.resolveAttemptToken(runtime, cp, 0)
	require.NotEmpty(t, newToken)
	require.NotEqual(t, "existing-attempt", newToken)
	require.Equal(t, 1, step.getAttemptSeq(cp, 0))
}

func buildExpectedLoopSubKey(t *testing.T, execCtx *NodeExecContext, workflowName string, input map[string]any) string {
	t.Helper()
	return runtimekeys.BuildSubWorkflowKey(execCtx.TaskContext.Task.ID, execCtx.NodeDef.Name, workflowName, input)
}

func cloneMapAny(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		switch x := v.(type) {
		case map[string]any:
			dst[k] = cloneMapAny(x)
		case []any:
			out := make([]any, len(x))
			for i := range x {
				if xm, ok := x[i].(map[string]any); ok {
					out[i] = cloneMapAny(xm)
				} else {
					out[i] = x[i]
				}
			}
			dst[k] = out
		default:
			dst[k] = v
		}
	}
	return dst
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
