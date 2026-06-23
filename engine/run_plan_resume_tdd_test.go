package engine

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/dto"
	"flux/eventbus"
	"flux/pkg/uuid"
	repository2 "flux/repository"
	"flux/runtimekeys"
	"flux/tool"
	"flux/utils"
	"flux/workflow"
	"flux/workflow/nodes"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

type legacyMapItem struct {
	Name string         `json:"name"`
	Meta legacyItemMeta `json:"meta"`
}

type legacyItemMeta struct {
	Score int `json:"score"`
}

type fakeTaskRepo struct {
	mu        sync.Mutex
	tasks     map[int64]*domain.Task
	updates   map[int64]int
	enqueues  []int64
	createErr error
}

func newFakeTaskRepo(tasks ...*domain.Task) *fakeTaskRepo {
	repo := &fakeTaskRepo{
		tasks:   map[int64]*domain.Task{},
		updates: map[int64]int{},
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		repo.tasks[task.ID] = cloneTask(task)
	}
	return repo
}

func (r *fakeTaskRepo) Create(ctx context.Context, task *domain.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	r.tasks[task.ID] = cloneTask(task)
	return nil
}

func (r *fakeTaskRepo) GetByID(ctx context.Context, id int64) (*domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[id]
	if task == nil {
		return nil, nil
	}
	return cloneTask(task), nil
}

func (r *fakeTaskRepo) Update(ctx context.Context, task *domain.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[task.ID] = cloneTask(task)
	r.updates[task.ID]++
	return nil
}

func (r *fakeTaskRepo) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Task
	for _, task := range r.tasks {
		if task.ParentID != nil && *task.ParentID == parentID {
			result = append(result, cloneTask(task))
		}
	}
	return result, nil
}

func (r *fakeTaskRepo) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	return nil, nil
}

func (r *fakeTaskRepo) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	return nil, nil
}

func (r *fakeTaskRepo) ListByUser(ctx context.Context, userID int64, params dto.PageRequest) ([]*domain.Task, int64, error) {
	return nil, 0, nil
}

func (r *fakeTaskRepo) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return r.ListByParent(ctx, parentID)
}

func (r *fakeTaskRepo) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	return nil
}

func (r *fakeTaskRepo) Enqueue(ctx context.Context, taskID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enqueues = append(r.enqueues, taskID)
	return nil
}

func (r *fakeTaskRepo) TryClaimTask(ctx context.Context, taskID int64, workerID string) (bool, error) {
	return true, nil
}

func (r *fakeTaskRepo) FindBySubKey(ctx context.Context, subKey string) (*domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, task := range r.tasks {
		if task.SubKey != nil && *task.SubKey == subKey {
			return cloneTask(task), nil
		}
	}
	return nil, nil
}

func (r *fakeTaskRepo) ListByParentNode(ctx context.Context, parentID int64, nodeName string) ([]*domain.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Task
	for _, task := range r.tasks {
		if task.ParentID == nil || *task.ParentID != parentID {
			continue
		}
		if task.ParentNode == nil || *task.ParentNode != nodeName {
			continue
		}
		result = append(result, cloneTask(task))
	}
	return result, nil
}

func (r *fakeTaskRepo) CreateFork(ctx context.Context, source *domain.Task, newTaskID int64, newInput []byte, editAction, editLabel string) (*domain.Task, error) {
	return nil, fmt.Errorf("not implemented")
}

func (r *fakeTaskRepo) ListByUserV2(ctx context.Context, userID int64, req dto.TaskListReq) ([]*dto.Task, int64, error) {
	return nil, 0, nil
}

func (r *fakeTaskRepo) GetRootTaskByIDAndUser(ctx context.Context, taskID int64, userID int64) (*domain.Task, error) {
	return r.GetByID(ctx, taskID)
}

func (r *fakeTaskRepo) GetTaskDetail(ctx context.Context, taskID int64) (*dto.TaskDetail, error) {
	return nil, nil
}

type fakeNodeRepo struct {
	mu                    sync.Mutex
	nodes                 map[int64]map[string]*domain.NodeRuntime
	attemptCalls          map[string]int
	successfulCompletions map[string]int
}

func newFakeNodeRepo() *fakeNodeRepo {
	return &fakeNodeRepo{
		nodes:                 map[int64]map[string]*domain.NodeRuntime{},
		attemptCalls:          map[string]int{},
		successfulCompletions: map[string]int{},
	}
}

func (r *fakeNodeRepo) Create(ctx context.Context, n *domain.NodeRuntime) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nodes[n.TaskID] == nil {
		r.nodes[n.TaskID] = map[string]*domain.NodeRuntime{}
	}
	r.nodes[n.TaskID][n.Name] = cloneNodeRuntime(n)
	return nil
}

func (r *fakeNodeRepo) Update(ctx context.Context, n *domain.NodeRuntime) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nodes[n.TaskID] == nil {
		r.nodes[n.TaskID] = map[string]*domain.NodeRuntime{}
	}
	r.nodes[n.TaskID][n.Name] = cloneNodeRuntime(n)
	return nil
}

func (r *fakeNodeRepo) FindByTaskID(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.NodeRuntime
	for _, node := range r.nodes[taskID] {
		result = append(result, cloneNodeRuntime(node))
	}
	return result, nil
}

func (r *fakeNodeRepo) FindByTaskIDAndNode(ctx context.Context, taskID int64, node string) (*domain.NodeRuntime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	runtime := r.nodes[taskID][node]
	if runtime == nil {
		return nil, nil
	}
	return cloneNodeRuntime(runtime), nil
}

func (r *fakeNodeRepo) MarkRunningAsRetrying(ctx context.Context, taskID int64) error {
	return nil
}

func (r *fakeNodeRepo) MarkAsRetrying(ctx context.Context, taskID int64, name string) error {
	return nil
}

func (r *fakeNodeRepo) MarkFailed(ctx context.Context, taskID int64, name string, errMessage string) error {
	return nil
}

func (r *fakeNodeRepo) FindExpiredRunningNodes(ctx context.Context, expire time.Time) ([]*domain.NodeRuntime, error) {
	return nil, nil
}

func (r *fakeNodeRepo) AttemptCompletePendingEdges(ctx context.Context, taskID int64, nodeName string, output map[string]any, errMsg string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%d:%s", taskID, nodeName)
	r.attemptCalls[key]++

	node := r.nodes[taskID][nodeName]
	if node == nil {
		return false, fmt.Errorf("runtime not found: %s", key)
	}

	switch node.State {
	case domain.NodeSuccessPendingEdges, domain.NodeFailedPendingEdges, domain.NodeSuccess, domain.NodeFailed:
		return false, nil
	}

	if errMsg != "" {
		node.State = domain.NodeFailedPendingEdges
		node.Error = errMsg
	} else {
		node.State = domain.NodeSuccessPendingEdges
		node.Output = deepCloneMap(output)
		node.OutputHash = calculateOutputHash(output)
	}
	r.successfulCompletions[key]++
	r.nodes[taskID][nodeName] = cloneNodeRuntime(node)
	return true, nil
}

func (r *fakeNodeRepo) CloneCheckpoint(ctx context.Context, fromTaskID, toTaskID int64) error {
	return nil
}

type fakeWorkflowRepo struct {
	byID   map[int64]*domain.Workflow
	byName map[string]*domain.Workflow
}

func newFakeWorkflowRepo(workflows ...*domain.Workflow) *fakeWorkflowRepo {
	repo := &fakeWorkflowRepo{
		byID:   map[int64]*domain.Workflow{},
		byName: map[string]*domain.Workflow{},
	}
	for _, wf := range workflows {
		if wf == nil {
			continue
		}
		cp := *wf
		repo.byID[wf.ID] = &cp
		repo.byName[wf.Name] = &cp
	}
	return repo
}

func (r *fakeWorkflowRepo) Create(ctx context.Context, wf *domain.Workflow) error { return nil }
func (r *fakeWorkflowRepo) Update(ctx context.Context, wf *domain.Workflow) error { return nil }
func (r *fakeWorkflowRepo) GetByID(ctx context.Context, id int64) (*domain.Workflow, error) {
	if wf, ok := r.byID[id]; ok {
		cp := *wf
		return &cp, nil
	}
	return nil, nil
}
func (r *fakeWorkflowRepo) GetByName(ctx context.Context, name string) (*domain.Workflow, error) {
	if wf, ok := r.byName[name]; ok {
		cp := *wf
		return &cp, nil
	}
	return nil, nil
}
func (r *fakeWorkflowRepo) List(ctx context.Context) ([]*domain.Workflow, error) { return nil, nil }

type fakeWorkflowVersionRepo struct {
	byID         map[int64]*domain.WorkflowVersion
	byWorkflowID map[int64]*domain.WorkflowVersion
}

func newFakeWorkflowVersionRepo(versions ...*domain.WorkflowVersion) *fakeWorkflowVersionRepo {
	repo := &fakeWorkflowVersionRepo{
		byID:         map[int64]*domain.WorkflowVersion{},
		byWorkflowID: map[int64]*domain.WorkflowVersion{},
	}
	for _, version := range versions {
		if version == nil {
			continue
		}
		cp := *version
		repo.byID[version.ID] = &cp
		repo.byWorkflowID[version.WorkflowID] = &cp
	}
	return repo
}

func (r *fakeWorkflowVersionRepo) Create(ctx context.Context, version *domain.WorkflowVersion) error {
	return nil
}

func (r *fakeWorkflowVersionRepo) Get(ctx context.Context, id int64) (*domain.WorkflowVersion, error) {
	if version, ok := r.byID[id]; ok {
		cp := *version
		return &cp, nil
	}
	return nil, nil
}

func (r *fakeWorkflowVersionRepo) GetLatestByWorkflowID(ctx context.Context, id int64) (*domain.WorkflowVersion, error) {
	if version, ok := r.byWorkflowID[id]; ok {
		cp := *version
		return &cp, nil
	}
	return nil, nil
}

func (r *fakeWorkflowVersionRepo) GetLatestByWorkflowName(ctx context.Context, name string) (*domain.WorkflowVersion, error) {
	return nil, nil
}

func (r *fakeWorkflowVersionRepo) UpdateDefinitionJSON(ctx context.Context, versionID int64, json []byte) error {
	return nil
}

type fakeLock struct{}

func (f *fakeLock) Lock(ctx context.Context, key string, timeout time.Duration) (bool, func(), error) {
	return true, func() {}, nil
}

type fakeAsyncJobQueue struct{}

func (q *fakeAsyncJobQueue) Publish(ctx context.Context, job AsyncJob) error { return nil }
func (q *fakeAsyncJobQueue) Consume(ctx context.Context, group string, consumer string) (*AsyncJob, string, error) {
	return nil, "", nil
}
func (q *fakeAsyncJobQueue) Ack(ctx context.Context, id string) error { return nil }

type asyncTestTool struct {
	name string
}

func (t *asyncTestTool) Name() string                 { return t.name }
func (t *asyncTestTool) Description() string          { return "async test tool" }
func (t *asyncTestTool) InputSchema() tool.DataSchema { return tool.DataSchema{} }
func (t *asyncTestTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"url": {Type: "string", Required: true},
		},
	}
}
func (t *asyncTestTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(map[string]any{"url": "https://example.com/generated.mp4"}), nil
}
func (t *asyncTestTool) Mode() tool.ExecutionMode { return tool.AsyncExecution }

type syncResultTool struct {
	name   string
	output map[string]any
	schema tool.DataSchema
	err    error
}

func (t *syncResultTool) Name() string                  { return t.name }
func (t *syncResultTool) Description() string           { return "sync result tool" }
func (t *syncResultTool) InputSchema() tool.DataSchema  { return tool.DataSchema{} }
func (t *syncResultTool) OutputSchema() tool.DataSchema { return t.schema }
func (t *syncResultTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	if t.err != nil {
		return nil, t.err
	}
	return tool.Success(deepCloneMap(t.output)), nil
}
func (t *syncResultTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func newPlanTestBuilder(t *testing.T) (*workflow.Builder, workflow.Workflow) {
	t.Helper()

	toolReg := tool.NewRegistry()
	toolReg.Register(&patchTestTool{
		name: "merge_video",
		output: tool.DataSchema{
			Fields: map[string]tool.FieldSchema{
				"file": {Type: "string"},
			},
		},
	})
	toolReg.Register(&patchTestTool{
		name: "single_upload_storage",
		output: tool.DataSchema{
			Fields: map[string]tool.FieldSchema{
				"url": {Type: "string"},
			},
		},
	})

	builder := workflow.NewBuilder(nodes.InitNodeRegistry(toolReg))
	def := &definition.WorkflowDefinition{
		Name: "run_plan_test_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.upload_storage.output.url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name: "map_images",
				Type: definition.NodeMap,
				Config: map[string]any{
					"items":    "input.image_urls",
					"iterator": "image",
					"workflow": "dummy_sub",
					"parallel": 2,
				},
			},
			{
				Name:   "merge_video",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "merge_video"},
				InputMapping: map[string]string{
					"videos": "map_images.results",
				},
			},
			{
				Name:   "upload_storage",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "single_upload_storage"},
				InputMapping: map[string]string{
					"file_path": "merge_video.file",
				},
			},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "map_images", Type: definition.EdgeNormal},
			{From: "map_images", To: "merge_video", Type: definition.EdgeNormal},
			{From: "merge_video", To: "upload_storage", Type: definition.EdgeNormal},
			{From: "upload_storage", To: "end", Type: definition.EdgeNormal},
		},
	}

	wf, err := builder.Build(def)
	require.NoError(t, err)
	return builder, wf
}

func newConditionalJoinPlanTestBuilder(t *testing.T) (*workflow.Builder, workflow.Workflow) {
	t.Helper()

	toolReg := tool.NewRegistry()
	valueSchema := tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"value": {Type: "string"},
		},
	}
	toolReg.Register(&patchTestTool{name: "router", output: valueSchema})
	toolReg.Register(&patchTestTool{name: "branch_tool", output: valueSchema})
	toolReg.Register(&patchTestTool{name: "join_tool", output: valueSchema})

	builder := workflow.NewBuilder(nodes.InitNodeRegistry(toolReg))
	def := &definition.WorkflowDefinition{
		Name: "conditional_join_plan_test_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "text",
			PrimaryFileUrl: "nodes.join.output.value",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name:   "router",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "router"},
			},
			{
				Name:   "inactive_branch",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "branch_tool"},
			},
			{
				Name:   "active_branch",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "branch_tool"},
			},
			{
				Name:   "join",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "join_tool"},
				InputMapping: map[string]string{
					"value": "active_branch.value ?? inactive_branch.value",
				},
			},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "router", Type: definition.EdgeNormal},
			{From: "router", To: "inactive_branch", Condition: "router.value == 'inactive'", Type: definition.EdgeCondition},
			{From: "router", To: "active_branch", Condition: "router.value == 'active'", Type: definition.EdgeCondition},
			{From: "inactive_branch", To: "join", Type: definition.EdgeNormal},
			{From: "active_branch", To: "join", Type: definition.EdgeNormal},
			{From: "join", To: "end", Type: definition.EdgeNormal},
		},
	}

	wf, err := builder.Build(def)
	require.NoError(t, err)
	return builder, wf
}

func newConditionalJoinParentSnapshot(taskID int64) *nodes.ReuseSnapshot {
	return &nodes.ReuseSnapshot{
		TaskID: taskID,
		Nodes: map[string]*domain.NodeRuntime{
			"start": {
				TaskID:         taskID,
				Name:           "start",
				State:          domain.NodeSuccess,
				ActivatedEdges: map[string]bool{"start->router": true},
			},
			"router": {
				TaskID: taskID,
				Name:   "router",
				State:  domain.NodeSuccess,
				Output: map[string]any{"value": "active"},
				ActivatedEdges: map[string]bool{
					"router->inactive_branch": false,
					"router->active_branch":   true,
				},
			},
			"inactive_branch": {
				TaskID:         taskID,
				Name:           "inactive_branch",
				State:          domain.NodeSkipped,
				Output:         map[string]any{},
				ActivatedEdges: map[string]bool{"inactive_branch->join": false},
			},
			"active_branch": {
				TaskID:         taskID,
				Name:           "active_branch",
				State:          domain.NodeSuccess,
				Output:         map[string]any{"value": "active-result"},
				ActivatedEdges: map[string]bool{"active_branch->join": true},
			},
			"join": {
				TaskID:         taskID,
				Name:           "join",
				State:          domain.NodeSuccess,
				Output:         map[string]any{"value": "joined"},
				ActivatedEdges: map[string]bool{"join->end": true},
			},
			"end": {
				TaskID: taskID,
				Name:   "end",
				State:  domain.NodeSuccess,
			},
		},
	}
}

func newAsyncResumeWorkflow(t *testing.T) (*workflow.Builder, workflow.Workflow, *domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()

	toolReg := tool.NewRegistry()
	toolReg.Register(&asyncTestTool{name: "fake_async"})
	builder := workflow.NewBuilder(nodes.InitNodeRegistry(toolReg))

	def := &definition.WorkflowDefinition{
		Name: "async_resume_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.async_generate.output.url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name:   "async_generate",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "fake_async"},
			},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "async_generate", Type: definition.EdgeNormal},
			{From: "async_generate", To: "end", Type: definition.EdgeNormal},
		},
	}

	wf, err := builder.Build(def)
	require.NoError(t, err)

	defJSON, err := json.Marshal(def)
	require.NoError(t, err)

	dbWorkflow := &domain.Workflow{ID: 301, Name: def.Name}
	version := &domain.WorkflowVersion{
		ID:             302,
		WorkflowID:     dbWorkflow.ID,
		Version:        1,
		DefinitionJSON: datatypes.JSON(defJSON),
	}
	return builder, wf, dbWorkflow, version
}

func newMapResumeWorkflow(t *testing.T) (*workflow.Builder, workflow.Workflow, *domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()

	builder := workflow.NewBuilder(nodes.InitNodeRegistry(nil))

	def := &definition.WorkflowDefinition{
		Name: "map_resume_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.map_render.output.results[0].primary_file_url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name: "map_render",
				Type: definition.NodeMap,
				Config: map[string]any{
					"items":    "input.items",
					"iterator": "item",
					"workflow": "dummy_sub",
					"parallel": 1,
				},
			},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "map_render", Type: definition.EdgeNormal},
			{From: "map_render", To: "end", Type: definition.EdgeNormal},
		},
	}

	wf, err := builder.Build(def)
	require.NoError(t, err)

	defJSON, err := json.Marshal(def)
	require.NoError(t, err)

	dbWorkflow := &domain.Workflow{ID: 401, Name: def.Name}
	version := &domain.WorkflowVersion{
		ID:             402,
		WorkflowID:     dbWorkflow.ID,
		Version:        1,
		DefinitionJSON: datatypes.JSON(defJSON),
	}
	return builder, wf, dbWorkflow, version
}

func newSubworkflowBranchBuilder(t *testing.T) *workflow.Builder {
	t.Helper()

	toolReg := tool.NewRegistry()
	toolReg.Register(&syncResultTool{
		name:   "decider",
		output: map[string]any{"take_true": true},
		schema: tool.DataSchema{Fields: map[string]tool.FieldSchema{"take_true": {Type: "bool", Required: true}}},
	})
	toolReg.Register(&syncResultTool{
		name:   "true_branch",
		output: map[string]any{"url": "https://example.com/true.mp4"},
		schema: tool.DataSchema{Fields: map[string]tool.FieldSchema{"url": {Type: "string", Required: true}}},
	})
	toolReg.Register(&syncResultTool{
		name:   "fail_branch",
		err:    fmt.Errorf("branch failed"),
		schema: tool.DataSchema{},
	})

	return workflow.NewBuilder(nodes.InitNodeRegistry(toolReg))
}

func newBranchWorkflow(t *testing.T) (*workflow.Builder, workflow.Workflow) {
	t.Helper()
	builder := newSubworkflowBranchBuilder(t)
	def := &definition.WorkflowDefinition{
		Name: "branch_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.true_path.output.url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "decide", Type: definition.NodeTool, Config: map[string]any{"tool": "decider"}},
			{Name: "true_path", Type: definition.NodeTool, Config: map[string]any{"tool": "true_branch"}},
			{Name: "false_path", Type: definition.NodeTool, Config: map[string]any{"tool": "fail_branch"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "decide", Type: definition.EdgeNormal},
			{From: "decide", To: "true_path", Type: definition.EdgeCondition, Condition: "decide.take_true == true"},
			{From: "decide", To: "false_path", Type: definition.EdgeCondition, Condition: "decide.take_true == false"},
			{From: "true_path", To: "end", Type: definition.EdgeNormal},
			{From: "false_path", To: "end", Type: definition.EdgeNormal},
		},
	}
	wf, err := builder.Build(def)
	require.NoError(t, err)
	return builder, wf
}

func newFailureClosureWorkflow(t *testing.T) (*workflow.Builder, workflow.Workflow) {
	t.Helper()
	toolReg := tool.NewRegistry()
	toolReg.Register(&syncResultTool{
		name:   "fail_immediately",
		err:    fmt.Errorf("boom"),
		schema: tool.DataSchema{},
	})
	builder := workflow.NewBuilder(nodes.InitNodeRegistry(toolReg))
	def := &definition.WorkflowDefinition{
		Name: "failure_closure_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.start.output.url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "fail_node", Type: definition.NodeTool, Config: map[string]any{"tool": "fail_immediately"}},
			{Name: "downstream", Type: definition.NodeTool, Config: map[string]any{"tool": "fail_immediately"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "fail_node", Type: definition.EdgeNormal},
			{From: "fail_node", To: "downstream", Type: definition.EdgeNormal},
			{From: "downstream", To: "end", Type: definition.EdgeNormal},
		},
	}
	wf, err := builder.Build(def)
	require.NoError(t, err)
	return builder, wf
}

func newEngineForTests(
	builder *workflow.Builder,
	taskRepo repository2.TaskRepository,
	nodeRepo repository2.NodeRuntimeRepository,
	workflowVersionRepo repository2.WorkflowVersionRepository,
	workflowRepo repository2.WorkflowRepository,
) *Engine {
	rebuilders := newCheckpointRebuildRegistry()
	rebuilders.Register(definition.NodeMap, func(nodeDef nodes.Node, runtime *domain.NodeRuntime) (map[string]any, error) {
		return rebuildMapNodeOutput(runtime)
	})
	rebuilders.Register(definition.NodeLoop, func(nodeDef nodes.Node, runtime *domain.NodeRuntime) (map[string]any, error) {
		return rebuildLoopNodeOutput(runtime)
	})

	return &Engine{
		taskRepo:             taskRepo,
		nodeRepo:             nodeRepo,
		WorkflowVersionRepo:  workflowVersionRepo,
		WorkflowRepo:         workflowRepo,
		builder:              builder,
		eventBus:             eventbus.NewEventBus(nil, nil),
		jobQueue:             &fakeAsyncJobQueue{},
		dLocker:              &fakeLock{},
		checkpointRebuilders: rebuilders,
	}
}

func newForkTask(taskID int64, workflowVersionID int64, input map[string]any) *domain.Task {
	inputJSON, _ := json.Marshal(input)
	parentID := int64(9001)
	return &domain.Task{
		ID:                taskID,
		RootID:            taskID,
		ForkedFrom:        &parentID,
		Status:            domain.TaskPending,
		InputJSON:         inputJSON,
		WorkflowVersionID: workflowVersionID,
	}
}

func newParentSnapshotForBuildPlan(taskID int64) *nodes.ReuseSnapshot {
	return &nodes.ReuseSnapshot{
		TaskID: taskID,
		Nodes: map[string]*domain.NodeRuntime{
			"start": {
				TaskID:         taskID,
				Name:           "start",
				State:          domain.NodeSuccess,
				ActivatedEdges: map[string]bool{"start->map_images": true},
			},
			"map_images": {
				TaskID: taskID,
				Name:   "map_images",
				State:  domain.NodeSuccess,
				Output: map[string]any{
					"results": []any{
						map[string]any{"file_path": "a.mp4"},
						map[string]any{"file_path": "b.mp4"},
					},
				},
				Checkpoint: map[string]any{
					"results": map[string]any{
						"0": map[string]any{"file_path": "a.mp4"},
						"1": map[string]any{"file_path": "b.mp4"},
					},
					"item_hashes": map[string]any{
						"0": "hash-a",
						"1": "hash-b",
					},
					"reused_items": map[string]any{
						"0": false,
						"1": false,
					},
					"done":  2,
					"total": 2,
				},
				ActivatedEdges: map[string]bool{"map_images->merge_video": true},
			},
			"merge_video": {
				TaskID:         taskID,
				Name:           "merge_video",
				State:          domain.NodeFailed,
				Output:         map[string]any{"file": "merged.mp4"},
				ActivatedEdges: map[string]bool{"merge_video->upload_storage": false},
			},
			"end": {
				TaskID: taskID,
				Name:   "end",
				State:  domain.NodeSuccess,
			},
		},
	}
}

func newParentSnapshotForMaterialize(taskID int64) *nodes.ReuseSnapshot {
	return &nodes.ReuseSnapshot{
		TaskID: taskID,
		Nodes: map[string]*domain.NodeRuntime{
			"start": {
				TaskID:         taskID,
				Name:           "start",
				State:          domain.NodeSuccess,
				ActivatedEdges: map[string]bool{"start->map_images": true},
			},
			"map_images": {
				TaskID: taskID,
				Name:   "map_images",
				State:  domain.NodeSuccess,
				Output: map[string]any{
					"results": []any{
						map[string]any{"file_path": "a.mp4"},
						map[string]any{"file_path": "b.mp4"},
					},
				},
				Checkpoint: map[string]any{
					"results": map[string]any{
						"0": map[string]any{"file_path": "a.mp4"},
						"1": map[string]any{"file_path": "b.mp4"},
					},
					"item_hashes": map[string]any{
						"0": "hash-a",
						"1": "hash-b",
					},
					"reused_items": map[string]any{
						"0": false,
						"1": false,
					},
					"done":  2,
					"total": 2,
				},
				ActivatedEdges: map[string]bool{"map_images->merge_video": true},
			},
			"merge_video": {
				TaskID:         taskID,
				Name:           "merge_video",
				State:          domain.NodeSuccess,
				Output:         map[string]any{"file": "merged.mp4"},
				OutputHash:     calculateOutputHash(map[string]any{"file": "merged.mp4"}),
				ActivatedEdges: map[string]bool{"merge_video->upload_storage": true},
			},
			"upload_storage": {
				TaskID:         taskID,
				Name:           "upload_storage",
				State:          domain.NodeSuccess,
				Output:         map[string]any{"url": "https://example.com/old.mp4"},
				ActivatedEdges: map[string]bool{"upload_storage->end": true},
			},
			"end": {
				TaskID: taskID,
				Name:   "end",
				State:  domain.NodeSuccess,
			},
		},
	}
}

func assignParentHashesForSuccessNodes(t *testing.T, e *Engine, runCtx *nodes.Context, wf workflow.Workflow) {
	t.Helper()

	planCtx, err := e.newPlanningContext(runCtx, wf, runCtx.Input)
	require.NoError(t, err)

	g := wf.Graph()
	for _, nodeName := range wf.Order() {
		parent := runCtx.ParentSnapshot.Nodes[nodeName]
		if parent == nil || parent.State != domain.NodeSuccess {
			continue
		}
		node := wf.Nodes()[nodeName]
		inputs, err := e.buildNodeInputForPlan(planCtx, node, g)
		require.NoError(t, err)
		parent.InputHash = planCtx.CalculateInputHash(
			fmt.Sprintf("%d-%s", runCtx.Task.WorkflowVersionID, node.Name),
			inputs,
		)
	}
}

func TestBuildRunPlan_InitialRunExecutesAllNodes(t *testing.T) {
	builder, wf := newPlanTestBuilder(t)
	e := newEngineForTests(builder, newFakeTaskRepo(), newFakeNodeRepo(), newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	task := &domain.Task{
		ID:                1001,
		RootID:            1001,
		Status:            domain.TaskPending,
		WorkflowVersionID: 88,
		InputJSON:         []byte(`{"image_urls":["a.jpg","b.jpg"]}`),
	}
	runCtx := e.newRunContext(context.Background(), task, wf)

	plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	require.NoError(t, err)
	require.Equal(t, RunPlanModeInitial, plan.Mode)

	for _, nodeName := range wf.Order() {
		nodePlan := plan.Nodes[nodeName]
		require.NotNil(t, nodePlan)
		require.Equal(t, PlanActionExecute, nodePlan.Action)
		require.Equal(t, ExecutionReasonNone, nodePlan.Reason)
		require.Equal(t, domain.ReuseNone, nodePlan.ReuseKind)
	}
}

func TestBuildRunPlan_ForkReusesSkippedInactiveBranchWithoutDirtyingJoin(t *testing.T) {
	builder, wf := newConditionalJoinPlanTestBuilder(t)
	e := newEngineForTests(builder, newFakeTaskRepo(), newFakeNodeRepo(), newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	task := newForkTask(1102, 92, map[string]any{})
	runCtx := e.newRunContext(context.Background(), task, wf)
	runCtx.ParentSnapshot = newConditionalJoinParentSnapshot(5102)

	assignParentHashesForSuccessNodes(t, e, runCtx, wf)

	plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	require.NoError(t, err)

	require.Equal(t, PlanActionReuse, plan.Nodes["inactive_branch"].Action)
	require.Equal(t, ExecutionReasonReuseNode, plan.Nodes["inactive_branch"].Reason)
	require.Equal(t, PlanActionReuse, plan.Nodes["join"].Action)
	require.Equal(t, ExecutionReasonReuseNode, plan.Nodes["join"].Reason)
	require.Equal(t, PlanActionReuse, plan.Nodes["end"].Action)
}

func TestBuildRunPlan_ForkMixedDecisions(t *testing.T) {
	builder, wf := newPlanTestBuilder(t)
	e := newEngineForTests(builder, newFakeTaskRepo(), newFakeNodeRepo(), newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	task := newForkTask(1002, 89, map[string]any{
		"image_urls": []string{"a.jpg", "b.jpg"},
	})
	runCtx := e.newRunContext(context.Background(), task, wf)
	runCtx.ParentSnapshot = newParentSnapshotForBuildPlan(5001)
	runCtx.ResumeFrom = "merge_video"
	runCtx.Patches = []domain.RuntimePatch{
		{
			Target: domain.PatchTargetNodeOutput,
			Node:   "map_images",
			Op:     domain.PatchOpSet,
			Path:   "results[1].file_path",
			Value:  "patched.mp4",
		},
	}

	assignParentHashesForSuccessNodes(t, e, runCtx, wf)

	plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	require.NoError(t, err)
	require.Equal(t, RunPlanModeFork, plan.Mode)
	require.Equal(t, task.ForkedFrom, plan.ParentTaskID)

	require.Equal(t, PlanActionReuse, plan.Nodes["start"].Action)
	require.Equal(t, ExecutionReasonReuseNode, plan.Nodes["start"].Reason)

	require.Equal(t, PlanActionPatch, plan.Nodes["map_images"].Action)
	require.Equal(t, ExecutionReasonPatchedNode, plan.Nodes["map_images"].Reason)
	require.Len(t, plan.Nodes["map_images"].Patches, 1)

	require.Equal(t, PlanActionExecute, plan.Nodes["merge_video"].Action)
	require.Equal(t, ExecutionReasonResumeBoundary, plan.Nodes["merge_video"].Reason)

	require.Equal(t, PlanActionExecute, plan.Nodes["upload_storage"].Action)
	require.Equal(t, ExecutionReasonMissingParent, plan.Nodes["upload_storage"].Reason)

	require.Equal(t, PlanActionExecute, plan.Nodes["end"].Action)
	require.Equal(t, ExecutionReasonUpstreamDirty, plan.Nodes["end"].Reason)
}

func TestBuildRunPlan_ParentNotReadyWinsWhenNoBoundaryOrPatch(t *testing.T) {
	builder, wf := newPlanTestBuilder(t)
	e := newEngineForTests(builder, newFakeTaskRepo(), newFakeNodeRepo(), newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	task := newForkTask(1003, 90, map[string]any{
		"image_urls": []string{"a.jpg", "b.jpg"},
	})
	runCtx := e.newRunContext(context.Background(), task, wf)
	runCtx.ParentSnapshot = newParentSnapshotForMaterialize(5002)
	runCtx.ParentSnapshot.Nodes["merge_video"].State = domain.NodeFailed

	assignParentHashesForSuccessNodes(t, e, runCtx, wf)

	plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	require.NoError(t, err)

	require.Equal(t, PlanActionExecute, plan.Nodes["merge_video"].Action)
	require.Equal(t, ExecutionReasonParentNotReady, plan.Nodes["merge_video"].Reason)
	require.Equal(t, PlanActionExecute, plan.Nodes["upload_storage"].Action)
	require.Equal(t, ExecutionReasonUpstreamDirty, plan.Nodes["upload_storage"].Reason)
}

func TestMaterializeRunPlan_AppliesReusePatchAndDirtyStates(t *testing.T) {
	builder, wf := newPlanTestBuilder(t)
	nodeRepo := newFakeNodeRepo()
	e := newEngineForTests(builder, newFakeTaskRepo(), nodeRepo, newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	task := newForkTask(1004, 91, map[string]any{
		"image_urls": []string{"a.jpg", "b.jpg"},
	})
	runCtx := e.newRunContext(context.Background(), task, wf)
	runCtx.ParentSnapshot = newParentSnapshotForMaterialize(5003)
	runCtx.ResumeFrom = "upload_storage"
	runCtx.Patches = []domain.RuntimePatch{
		{
			Target: domain.PatchTargetNodeOutput,
			Node:   "merge_video",
			Op:     domain.PatchOpSet,
			Path:   "file",
			Value:  "patched-merged.mp4",
		},
	}

	assignParentHashesForSuccessNodes(t, e, runCtx, wf)
	err := e.loadOrInitRuntime(runCtx, wf)
	require.NoError(t, err)

	plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	require.NoError(t, err)
	err = e.MaterializeRunPlan(runCtx, wf, plan)
	require.NoError(t, err)

	mapRuntime := runCtx.Runtime["map_images"]
	require.Equal(t, domain.NodeSuccess, mapRuntime.State)
	require.True(t, mapRuntime.IsInjected)
	require.False(t, mapRuntime.IsDirty)
	require.Equal(t, domain.ReuseNode, mapRuntime.ReuseKind)
	require.NotNil(t, mapRuntime.ReusedFromTaskID)
	require.Equal(t, string(ExecutionReasonReuseNode), mapRuntime.ExecutionReason)
	require.Equal(t, "b.mp4", getSecondFilePathFromAnyResults(t, runCtx.GetNodeOutput("map_images")["results"]))

	mergeRuntime := runCtx.Runtime["merge_video"]
	require.Equal(t, domain.NodeSuccess, mergeRuntime.State)
	require.False(t, mergeRuntime.IsInjected)
	require.False(t, mergeRuntime.IsDirty)
	require.Equal(t, domain.ReuseNone, mergeRuntime.ReuseKind)
	require.Equal(t, "patched-merged.mp4", mergeRuntime.Output["file"])
	require.True(t, runCtx.PatchedNodes["merge_video"])
	require.Equal(t, string(ExecutionReasonPatchedNode), mergeRuntime.ExecutionReason)

	uploadRuntime := runCtx.Runtime["upload_storage"]
	require.Equal(t, domain.NodePending, uploadRuntime.State)
	require.True(t, uploadRuntime.IsDirty)
	require.False(t, uploadRuntime.IsInjected)
	require.Equal(t, DirtyReasonResumeBoundary, uploadRuntime.DirtyReason)
	require.Equal(t, string(ExecutionReasonResumeBoundary), uploadRuntime.ExecutionReason)
	require.Nil(t, uploadRuntime.Output)
	require.Nil(t, runCtx.GetNodeOutput("upload_storage"))

	endRuntime := runCtx.Runtime["end"]
	require.Equal(t, domain.NodePending, endRuntime.State)
	require.True(t, endRuntime.IsDirty)
	require.Equal(t, DirtyReasonUpstreamDirty, endRuntime.DirtyReason)
	require.Equal(t, string(ExecutionReasonUpstreamDirty), endRuntime.ExecutionReason)
}

func TestAsyncEventListener_DuplicateCompletionOnlyResumesOnce(t *testing.T) {
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)

	task := &domain.Task{
		ID:                   2001,
		RootID:               2001,
		Status:               domain.TaskSuspended,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON:            []byte(`{}`),
	}
	taskRepo := newFakeTaskRepo(task)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID:         task.ID,
		Name:           "start",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"start->async_generate": true},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: task.ID,
		Name:   "async_generate",
		State:  domain.NodeRunning,
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: task.ID,
		Name:   "end",
		State:  domain.NodePending,
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)
	e.startAsyncNodeEventListener()

	meta := map[string]any{"url": "https://example.com/generated.mp4"}
	e.eventBus.Publish(task.ID, &domain.TaskEvent{TaskID: task.ID, Step: "async_generate", Type: domain.TaskEventNodeCompleteAsync, Meta: meta})
	e.eventBus.Publish(task.ID, &domain.TaskEvent{TaskID: task.ID, Step: "async_generate", Type: domain.TaskEventNodeCompleteAsync, Meta: meta})

	require.Eventually(t, func() bool {
		updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
		require.NoError(t, err)
		if updatedTask == nil || updatedTask.Status != domain.TaskSuccess {
			return false
		}
		runtime, err := nodeRepo.FindByTaskIDAndNode(context.Background(), task.ID, "async_generate")
		require.NoError(t, err)
		return runtime != nil && runtime.State == domain.NodeSuccess
	}, time.Second, 20*time.Millisecond)

	completionKey := fmt.Sprintf("%d:%s", task.ID, "async_generate")
	require.Equal(t, 2, nodeRepo.attemptCalls[completionKey])
	require.Equal(t, 1, nodeRepo.successfulCompletions[completionKey])

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updatedTask.OutputJSON)

	final, err := domain.ParseFinal(updatedTask.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/generated.mp4", final.PrimaryFileUrl)
}

func TestSubWorkflowSuccessListener_RejectsStaleLoopChildEvent(t *testing.T) {
	parentID := int64(3001)
	childID := int64(3002)
	parentNode := "loop_render"
	oldSubKey := "old-sub-key"
	newSubKey := "new-sub-key"

	parentTask := &domain.Task{
		ID:     parentID,
		RootID: parentID,
		Status: domain.TaskSuspended,
	}
	childTask := &domain.Task{
		ID:         childID,
		RootID:     parentID,
		Status:     domain.TaskSuccess,
		ParentID:   &parentID,
		ParentNode: &parentNode,
		SubKey:     &oldSubKey,
	}

	taskRepo := newFakeTaskRepo(parentTask, childTask)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   parentNode,
		State:  domain.NodeRunning,
		Checkpoint: map[string]any{
			nodes.CPFanoutKind(): "loop",
			"running_index":      0,
			"running_sub_key":    newSubKey,
		},
	}))

	e := newEngineForTests(
		nil,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(),
		newFakeWorkflowRepo(),
	)
	e.startSubWorkflowSuccessListener()

	e.eventBus.Publish(childID, &domain.TaskEvent{
		TaskID: childID,
		Type:   "task_succeeded",
	})

	time.Sleep(120 * time.Millisecond)

	updatedParent, err := taskRepo.GetByID(context.Background(), parentID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskSuspended, updatedParent.Status)

	runtime, err := nodeRepo.FindByTaskIDAndNode(context.Background(), parentID, parentNode)
	require.NoError(t, err)
	require.Equal(t, domain.NodeRunning, runtime.State)
	require.Empty(t, nodeRepo.attemptCalls)
}

func TestSubWorkflowSuccessListener_MapChildSuccessResumesAndCompletesParent(t *testing.T) {
	builder, _, dbWorkflow, version := newMapResumeWorkflow(t)

	parentID := int64(3101)
	childID := int64(3102)
	parentNode := "map_render"

	parentTask := &domain.Task{
		ID:                   parentID,
		RootID:               parentID,
		Status:               domain.TaskSuspended,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON:            []byte(`{"items":["frame-1"]}`),
	}
	childTask := &domain.Task{
		ID:         childID,
		RootID:     parentID,
		Status:     domain.TaskSuccess,
		ParentID:   &parentID,
		ParentNode: &parentNode,
		MapIndex:   intPtr(0),
		InputJSON:  []byte(`{"index":0,"__map_item_hash":"hash-0"}`),
		OutputJSON: mustMarshalJSON(t, map[string]any{
			"final": map[string]any{
				"result_type":      "video",
				"primary_file_url": "https://example.com/frame-1.mp4",
			},
		}),
	}

	taskRepo := newFakeTaskRepo(parentTask, childTask)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID:         parentID,
		Name:           "start",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"start->map_render": true},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   parentNode,
		State:  domain.NodeRunning,
		Checkpoint: map[string]any{
			nodes.CPFanoutKind(): "map",
			"total":              1,
			"done":               0,
			"results":            map[string]any{},
			"item_hashes":        map[string]any{},
			"reused_items":       map[string]any{},
		},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   "end",
		State:  domain.NodePending,
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)
	e.startSubWorkflowSuccessListener()

	e.eventBus.Publish(childID, &domain.TaskEvent{
		TaskID: childID,
		Type:   "task_succeeded",
	})

	require.Eventually(t, func() bool {
		parent, err := taskRepo.GetByID(context.Background(), parentID)
		require.NoError(t, err)
		return parent != nil && parent.Status == domain.TaskSuccess
	}, time.Second, 20*time.Millisecond)

	parent, err := taskRepo.GetByID(context.Background(), parentID)
	require.NoError(t, err)
	final, err := domain.ParseFinal(parent.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/frame-1.mp4", final.PrimaryFileUrl)

	mapRuntime, err := nodeRepo.FindByTaskIDAndNode(context.Background(), parentID, parentNode)
	require.NoError(t, err)
	require.Equal(t, domain.NodeSuccess, mapRuntime.State)
	require.NotNil(t, mapRuntime.Output)
	require.Equal(t, 1, len(mapRuntime.Output["results"].([]any)))
}

func TestSubWorkflowSuccessListener_MapChildDuplicateWakeupDoesNotCreateSecondChild(t *testing.T) {
	builder, _, dbWorkflow, version := newMapResumeWorkflow(t)

	parentID := int64(3111)
	child0ID := int64(3112)
	child1ID := int64(3113)
	parentNode := "map_render"

	parentTask := &domain.Task{
		ID:                   parentID,
		RootID:               parentID,
		Status:               domain.TaskSuspended,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON: mustMarshalJSON(t, map[string]any{
			"items": []any{
				map[string]any{"name": "item-0", "meta": map[string]any{"score": 0}},
				map[string]any{"name": "item-1", "meta": map[string]any{"score": 1}},
			},
		}),
	}
	child0 := &domain.Task{
		ID:         child0ID,
		RootID:     parentID,
		Status:     domain.TaskSuccess,
		ParentID:   &parentID,
		ParentNode: &parentNode,
		MapIndex:   intPtr(0),
		InputJSON: mustMarshalJSON(t, map[string]any{
			"index":           0,
			"item":            map[string]any{"name": "item-0", "meta": map[string]any{"score": 0}},
			"__map_item_hash": nodes.CalculateMapItemHash(map[string]any{"name": "item-0", "meta": map[string]any{"score": 0}}),
		}),
		OutputJSON: mustMarshalJSON(t, map[string]any{
			"final": map[string]any{
				"result_type":      "video",
				"primary_file_url": "https://example.com/frame-0.mp4",
			},
		}),
	}
	legacyInput := map[string]any{
		"item":            legacyMapItem{Name: "item-1", Meta: legacyItemMeta{Score: 1}},
		"index":           1,
		"__map_item_hash": nodes.CalculateMapItemHash(map[string]any{"name": "item-1", "meta": map[string]any{"score": 1}}),
	}
	legacySubKey := legacyBuildSubWorkflowKey(parentID, parentNode, dbWorkflow.Name, legacyInput)
	child1 := &domain.Task{
		ID:         child1ID,
		RootID:     parentID,
		Status:     domain.TaskSuspended,
		ParentID:   &parentID,
		ParentNode: &parentNode,
		MapIndex:   intPtr(1),
		SubKey:     &legacySubKey,
		InputJSON: mustMarshalJSON(t, map[string]any{
			"index":           1,
			"item":            map[string]any{"name": "item-1", "meta": map[string]any{"score": 1}},
			"__map_item_hash": nodes.CalculateMapItemHash(map[string]any{"name": "item-1", "meta": map[string]any{"score": 1}}),
		}),
	}

	taskRepo := newFakeTaskRepo(parentTask, child0, child1)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID:         parentID,
		Name:           "start",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"start->map_render": true},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   parentNode,
		State:  domain.NodeRunning,
		Checkpoint: map[string]any{
			nodes.CPFanoutKind(): "map",
			"total":              2,
			"done":               0,
			"results":            map[string]any{},
			"item_hashes":        map[string]any{},
			"reused_items":       map[string]any{},
		},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   "end",
		State:  domain.NodePending,
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)
	e.iSrv = *uuid.NewNode(3)
	e.startSubWorkflowSuccessListener()

	e.eventBus.Publish(child0ID, &domain.TaskEvent{TaskID: child0ID, Type: "task_succeeded"})
	e.eventBus.Publish(child0ID, &domain.TaskEvent{TaskID: child0ID, Type: "task_succeeded"})

	require.Eventually(t, func() bool {
		mapRuntime, err := nodeRepo.FindByTaskIDAndNode(context.Background(), parentID, parentNode)
		require.NoError(t, err)
		if mapRuntime == nil || mapRuntime.Checkpoint == nil {
			return false
		}
		return mapRuntime.Checkpoint["done"] == 1
	}, time.Second, 20*time.Millisecond)

	time.Sleep(120 * time.Millisecond)

	taskRepo.mu.Lock()
	taskCount := len(taskRepo.tasks)
	var indexOneChildren int
	for _, task := range taskRepo.tasks {
		if task.ParentID == nil || *task.ParentID != parentID {
			continue
		}
		if task.ParentNode == nil || *task.ParentNode != parentNode {
			continue
		}
		if task.MapIndex != nil && *task.MapIndex == 1 {
			indexOneChildren++
		}
	}
	taskRepo.mu.Unlock()

	require.Equal(t, 3, taskCount)
	require.Equal(t, 1, indexOneChildren)

	parent, err := taskRepo.GetByID(context.Background(), parentID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskSuspended, parent.Status)
}

func TestSubWorkflowFailedListener_MapChildFailureResumesAndFailsParent(t *testing.T) {
	builder, _, dbWorkflow, version := newMapResumeWorkflow(t)

	parentID := int64(3201)
	childID := int64(3202)
	parentNode := "map_render"

	parentTask := &domain.Task{
		ID:                   parentID,
		RootID:               parentID,
		Status:               domain.TaskSuspended,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON:            []byte(`{"items":["frame-1"]}`),
	}
	childTask := &domain.Task{
		ID:         childID,
		RootID:     parentID,
		Status:     domain.TaskFailed,
		ParentID:   &parentID,
		ParentNode: &parentNode,
		MapIndex:   intPtr(0),
		InputJSON:  []byte(`{"index":0,"__map_item_hash":"hash-0"}`),
	}

	taskRepo := newFakeTaskRepo(parentTask, childTask)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID:         parentID,
		Name:           "start",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"start->map_render": true},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   parentNode,
		State:  domain.NodeRunning,
		Checkpoint: map[string]any{
			nodes.CPFanoutKind(): "map",
			"total":              1,
			"done":               0,
			"results":            map[string]any{},
			"item_hashes":        map[string]any{},
			"reused_items":       map[string]any{},
		},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   "end",
		State:  domain.NodePending,
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)
	e.startSubWorkflowFailedListener()

	e.eventBus.Publish(childID, &domain.TaskEvent{
		TaskID:  childID,
		Type:    "task_failed",
		Message: "child failed",
	})

	require.Eventually(t, func() bool {
		parent, err := taskRepo.GetByID(context.Background(), parentID)
		require.NoError(t, err)
		return parent != nil && parent.Status == domain.TaskFailed
	}, time.Second, 20*time.Millisecond)

	parent, err := taskRepo.GetByID(context.Background(), parentID)
	require.NoError(t, err)
	require.Contains(t, parent.ErrorMessage, "map child task failed")

	mapRuntime, err := nodeRepo.FindByTaskIDAndNode(context.Background(), parentID, parentNode)
	require.NoError(t, err)
	require.Equal(t, domain.NodeFailed, mapRuntime.State)
}

func TestResumeTask_SuccessPendingEdgesCompletesTask(t *testing.T) {
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)

	task := &domain.Task{
		ID:                   4101,
		RootID:               4101,
		Status:               domain.TaskSuspended,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON:            []byte(`{}`),
	}
	taskRepo := newFakeTaskRepo(task)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID:         task.ID,
		Name:           "start",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"start->async_generate": true},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID:   task.ID,
		Name:     "async_generate",
		State:    domain.NodeSuccessPendingEdges,
		Output:   map[string]any{"url": "https://example.com/resume-success.mp4"},
		Progress: 1,
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: task.ID,
		Name:   "end",
		State:  domain.NodePending,
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)

	result := e.ResumeTask(task.ID, "async_generate", nil)
	require.Equal(t, RunSuccess, result.Status)
	require.NoError(t, result.Err)

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskSuccess, updatedTask.Status)

	final, err := domain.ParseFinal(updatedTask.OutputJSON)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/resume-success.mp4", final.PrimaryFileUrl)
}

func TestResumeTask_FailedPendingEdgesFailsTask(t *testing.T) {
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)

	task := &domain.Task{
		ID:                   4201,
		RootID:               4201,
		Status:               domain.TaskSuspended,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON:            []byte(`{}`),
	}
	taskRepo := newFakeTaskRepo(task)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID:         task.ID,
		Name:           "start",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"start->async_generate": true},
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: task.ID,
		Name:   "async_generate",
		State:  domain.NodeFailedPendingEdges,
		Error:  "async provider failed",
	}))
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: task.ID,
		Name:   "end",
		State:  domain.NodePending,
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)
	finalFailedCh := e.eventBus.Subscribe(domain.TaskEventFinalFailed)

	result := e.ResumeTask(task.ID, "async_generate", nil)
	require.Equal(t, RunFailed, result.Status)
	require.Error(t, result.Err)

	updatedTask, err := taskRepo.GetByID(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskFailed, updatedTask.Status)
	require.Contains(t, updatedTask.ErrorMessage, "async provider failed")

	select {
	case evt := <-finalFailedCh:
		require.NotNil(t, evt)
		require.Equal(t, task.ID, evt.TaskID)
		require.Equal(t, domain.TaskEventFinalFailed, evt.Type)
	default:
		t.Fatalf("expected task_final_failed event for resume failure")
	}
}

func TestResumeTask_RepeatedResumeAfterSuccessIsNoop(t *testing.T) {
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)

	task := &domain.Task{
		ID:                   4301,
		RootID:               4301,
		Status:               domain.TaskSuccess,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON:            []byte(`{}`),
	}
	taskRepo := newFakeTaskRepo(task)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: task.ID,
		Name:   "async_generate",
		State:  domain.NodeSuccess,
		Output: map[string]any{"url": "https://example.com/already.mp4"},
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)

	result := e.ResumeTask(task.ID, "async_generate", nil)
	require.Equal(t, RunNoop, result.Status)
}

func TestSubWorkflowSuccessListener_LateEventIgnoredWhenParentTerminal(t *testing.T) {
	builder, _, dbWorkflow, version := newMapResumeWorkflow(t)

	parentID := int64(4401)
	childID := int64(4402)
	parentNode := "map_render"

	parentTask := &domain.Task{
		ID:                   parentID,
		RootID:               parentID,
		Status:               domain.TaskSuccess,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: dbWorkflow.ID,
		InputJSON:            []byte(`{"items":["frame-1"]}`),
	}
	childTask := &domain.Task{
		ID:         childID,
		RootID:     parentID,
		Status:     domain.TaskSuccess,
		ParentID:   &parentID,
		ParentNode: &parentNode,
		MapIndex:   intPtr(0),
		InputJSON:  []byte(`{"index":0,"__map_item_hash":"hash-0"}`),
		OutputJSON: mustMarshalJSON(t, map[string]any{
			"final": map[string]any{
				"result_type":      "video",
				"primary_file_url": "https://example.com/late.mp4",
			},
		}),
	}

	taskRepo := newFakeTaskRepo(parentTask, childTask)
	nodeRepo := newFakeNodeRepo()
	require.NoError(t, nodeRepo.Create(context.Background(), &domain.NodeRuntime{
		TaskID: parentID,
		Name:   parentNode,
		State:  domain.NodeSuccess,
		Checkpoint: map[string]any{
			nodes.CPFanoutKind(): "map",
			"total":              1,
			"done":               1,
			"results": map[string]any{
				"0": map[string]any{"primary_file_url": "https://example.com/already.mp4"},
			},
			"item_hashes":  map[string]any{"0": "hash-0"},
			"reused_items": map[string]any{"0": false},
		},
		Output: map[string]any{
			"results": []any{map[string]any{"primary_file_url": "https://example.com/already.mp4"}},
		},
	}))

	e := newEngineForTests(
		builder,
		taskRepo,
		nodeRepo,
		newFakeWorkflowVersionRepo(version),
		newFakeWorkflowRepo(dbWorkflow),
	)
	e.startSubWorkflowSuccessListener()

	e.eventBus.Publish(childID, &domain.TaskEvent{
		TaskID: childID,
		Type:   "task_succeeded",
	})

	time.Sleep(120 * time.Millisecond)

	require.Empty(t, nodeRepo.attemptCalls)
	parent, err := taskRepo.GetByID(context.Background(), parentID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskSuccess, parent.Status)
}

func TestTransitionTaskStatus_DoesNotOverwritePersistedCancel(t *testing.T) {
	task := &domain.Task{
		ID:     5201,
		RootID: 5201,
		Status: domain.TaskRunning,
	}
	taskRepo := newFakeTaskRepo(&domain.Task{
		ID:           task.ID,
		RootID:       task.RootID,
		Status:       domain.TaskCanceled,
		ErrorMessage: "user canceled running task",
	})
	e := newEngineForTests(nil, taskRepo, newFakeNodeRepo(), nil, nil)

	err := e.transitionTaskStatus(&nodes.Context{
		Ctx:  context.Background(),
		Task: task,
	}, domain.TaskSuccess)

	require.ErrorIs(t, err, ErrTaskCanceled)
	require.Equal(t, domain.TaskCanceled, task.Status)
	persisted, err := taskRepo.GetByID(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskCanceled, persisted.Status)
	require.Equal(t, "user canceled running task", persisted.ErrorMessage)
}

func TestRunSubWorkflow_ExistingSuccessForSubworkflowNodeReturnsFinal(t *testing.T) {
	builder, wf, dbWorkflow, version := newAsyncResumeWorkflow(t)
	_ = wf

	parentTask := &domain.Task{ID: 5001, RootID: 5001, Status: domain.TaskRunning}
	taskRepo := newFakeTaskRepo(parentTask)
	workflowRepo := newFakeWorkflowRepo(dbWorkflow)
	versionRepo := newFakeWorkflowVersionRepo(version)
	e := newEngineForTests(builder, taskRepo, newFakeNodeRepo(), versionRepo, workflowRepo)

	runCtx := &nodes.Context{Ctx: context.Background(), Task: parentTask}
	outputJSON := mustMarshalJSON(t, map[string]any{
		"final": map[string]any{
			"result_type":      "video",
			"primary_file_url": "https://example.com/existing.mp4",
		},
	})
	subKey := runtimekeys.BuildSubWorkflowKey(parentTask.ID, "sub_node", dbWorkflow.Name, map[string]any{"prompt": "hello"})
	existing := &domain.Task{
		ID:         5002,
		RootID:     parentTask.RootID,
		Status:     domain.TaskSuccess,
		SubKey:     &subKey,
		OutputJSON: outputJSON,
	}
	taskRepo.tasks[existing.ID] = cloneTask(existing)

	execCtx := &nodes.NodeExecContext{
		TaskContext: runCtx,
		NodeDef:     &definition.NodeDefinition{Name: "sub_node", Type: definition.NodeSubWorkflow},
	}
	result, err := e.RunSubWorkflow(execCtx, dbWorkflow.Name, map[string]any{"prompt": "hello"})
	require.NoError(t, err)
	require.Equal(t, "https://example.com/existing.mp4", result["primary_file_url"])
}

func TestRunSubWorkflow_ExistingSuccessForMapNodeSuspends(t *testing.T) {
	builder, _, dbWorkflow, version := newMapResumeWorkflow(t)

	parentTask := &domain.Task{ID: 5101, RootID: 5101, Status: domain.TaskRunning}
	taskRepo := newFakeTaskRepo(parentTask)
	e := newEngineForTests(builder, taskRepo, newFakeNodeRepo(), newFakeWorkflowVersionRepo(version), newFakeWorkflowRepo(dbWorkflow))

	runCtx := &nodes.Context{Ctx: context.Background(), Task: parentTask}
	subInput := map[string]any{"item": "frame-a", "index": 0}
	subKey := runtimekeys.BuildSubWorkflowKey(parentTask.ID, "map_render", dbWorkflow.Name, subInput)
	taskRepo.tasks[5102] = &domain.Task{
		ID:     5102,
		RootID: parentTask.RootID,
		Status: domain.TaskSuccess,
		SubKey: &subKey,
	}

	execCtx := &nodes.NodeExecContext{
		TaskContext: runCtx,
		NodeDef:     &definition.NodeDefinition{Name: "map_render", Type: definition.NodeMap},
	}
	_, err := e.RunSubWorkflow(execCtx, dbWorkflow.Name, subInput)
	var suspendErr *domain.WorkflowSuspendedError
	require.ErrorAs(t, err, &suspendErr)
}

func TestRunSubWorkflow_ExistingFailedRequeuesAndSuspends(t *testing.T) {
	builder, wf, dbWorkflow, version := newAsyncResumeWorkflow(t)
	_ = wf
	parentTask := &domain.Task{ID: 5201, RootID: 5201, Status: domain.TaskRunning}
	taskRepo := newFakeTaskRepo(parentTask)
	e := newEngineForTests(builder, taskRepo, newFakeNodeRepo(), newFakeWorkflowVersionRepo(version), newFakeWorkflowRepo(dbWorkflow))

	runCtx := &nodes.Context{Ctx: context.Background(), Task: parentTask}
	subInput := map[string]any{"prompt": "hello"}
	subKey := runtimekeys.BuildSubWorkflowKey(parentTask.ID, "sub_node", dbWorkflow.Name, subInput)
	taskRepo.tasks[5202] = &domain.Task{
		ID:     5202,
		RootID: parentTask.RootID,
		Status: domain.TaskFailed,
		SubKey: &subKey,
	}
	execCtx := &nodes.NodeExecContext{
		TaskContext: runCtx,
		NodeDef:     &definition.NodeDefinition{Name: "sub_node", Type: definition.NodeSubWorkflow},
	}

	_, err := e.RunSubWorkflow(execCtx, dbWorkflow.Name, subInput)
	var suspendErr *domain.WorkflowSuspendedError
	require.ErrorAs(t, err, &suspendErr)

	reloaded, getErr := taskRepo.GetByID(context.Background(), 5202)
	require.NoError(t, getErr)
	require.Equal(t, domain.TaskPending, reloaded.Status)
	require.Contains(t, taskRepo.enqueues, int64(5202))
}

func TestRunSubWorkflow_CreateConflictFallsBackToSuspend(t *testing.T) {
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)
	parentTask := &domain.Task{ID: 5301, RootID: 5301, Status: domain.TaskRunning}
	taskRepo := newFakeTaskRepo(parentTask)
	taskRepo.createErr = fmt.Errorf("duplicate key")
	e := newEngineForTests(builder, taskRepo, newFakeNodeRepo(), newFakeWorkflowVersionRepo(version), newFakeWorkflowRepo(dbWorkflow))

	runCtx := &nodes.Context{Ctx: context.Background(), Task: parentTask}
	subInput := map[string]any{"prompt": "hello"}
	subKey := runtimekeys.BuildSubWorkflowKey(parentTask.ID, "sub_node", dbWorkflow.Name, subInput)
	taskRepo.tasks[5302] = &domain.Task{
		ID:     5302,
		RootID: parentTask.RootID,
		Status: domain.TaskPending,
		SubKey: &subKey,
	}
	execCtx := &nodes.NodeExecContext{
		TaskContext: runCtx,
		NodeDef:     &definition.NodeDefinition{Name: "sub_node", Type: definition.NodeSubWorkflow},
	}

	_, err := e.RunSubWorkflow(execCtx, dbWorkflow.Name, subInput)
	var suspendErr *domain.WorkflowSuspendedError
	require.ErrorAs(t, err, &suspendErr)
}

func TestRunDAG_ConditionEdgeSkipsInactiveBranch(t *testing.T) {
	builder, wf := newBranchWorkflow(t)
	task := &domain.Task{
		ID:                5401,
		RootID:            5401,
		Status:            domain.TaskPending,
		WorkflowVersionID: 1,
		InputJSON:         []byte(`{}`),
	}
	taskRepo := newFakeTaskRepo(task)
	nodeRepo := newFakeNodeRepo()
	e := newEngineForTests(builder, taskRepo, nodeRepo, newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	runCtx := e.newRunContext(context.Background(), task, wf)
	err := e.loadOrInitRuntime(runCtx, wf)
	require.NoError(t, err)
	result := e.executeTask(runCtx, wf, false)
	require.Equal(t, RunSuccess, result.Status)

	trueNode := runCtx.Runtime["true_path"]
	falseNode := runCtx.Runtime["false_path"]
	endNode := runCtx.Runtime["end"]
	require.Equal(t, domain.NodeSuccess, trueNode.State)
	require.Equal(t, domain.NodeSkipped, falseNode.State)
	require.Equal(t, domain.NodeSuccess, endNode.State)
	require.Equal(t, false, runCtx.ActivatedEdges["decide->false_path"])
	require.Equal(t, true, runCtx.ActivatedEdges["decide->true_path"])
}

func TestRunDAG_FailedNodeClosesDownstreamPath(t *testing.T) {
	builder, wf := newFailureClosureWorkflow(t)
	task := &domain.Task{
		ID:                5501,
		RootID:            5501,
		Status:            domain.TaskPending,
		WorkflowVersionID: 1,
		InputJSON:         []byte(`{}`),
	}
	taskRepo := newFakeTaskRepo(task)
	nodeRepo := newFakeNodeRepo()
	e := newEngineForTests(builder, taskRepo, nodeRepo, newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	runCtx := e.newRunContext(context.Background(), task, wf)
	err := e.loadOrInitRuntime(runCtx, wf)
	require.NoError(t, err)
	result := e.executeTask(runCtx, wf, false)
	require.Equal(t, RunFailed, result.Status)
	require.Error(t, result.Err)

	require.Equal(t, domain.NodeFailed, runCtx.Runtime["fail_node"].State)
	require.Equal(t, domain.NodeSkipped, runCtx.Runtime["downstream"].State)
	require.Equal(t, domain.NodeSkipped, runCtx.Runtime["end"].State)
	// P0 修复：EdgeNormal 父失败时边写 true（必选路径但阻塞），不是 false。
	// resolveEdgeState 根据 parent.State=Failed 推导 blocked，不再与条件边 inactive 混淆。
	require.Equal(t, true, runCtx.ActivatedEdges["fail_node->downstream"])
}

func cloneTask(task *domain.Task) *domain.Task {
	if task == nil {
		return nil
	}
	cp := *task
	if task.InputJSON != nil {
		cp.InputJSON = append([]byte(nil), task.InputJSON...)
	}
	if task.OutputJSON != nil {
		cp.OutputJSON = append([]byte(nil), task.OutputJSON...)
	}
	if task.SubKey != nil {
		v := *task.SubKey
		cp.SubKey = &v
	}
	if task.ParentID != nil {
		v := *task.ParentID
		cp.ParentID = &v
	}
	if task.ParentNode != nil {
		v := *task.ParentNode
		cp.ParentNode = &v
	}
	if task.MapIndex != nil {
		v := *task.MapIndex
		cp.MapIndex = &v
	}
	if task.ForkedFrom != nil {
		v := *task.ForkedFrom
		cp.ForkedFrom = &v
	}
	if task.ResumeFrom != nil {
		v := *task.ResumeFrom
		cp.ResumeFrom = &v
	}
	if task.EditAction != nil {
		v := *task.EditAction
		cp.EditAction = &v
	}
	if task.EditLabel != nil {
		v := *task.EditLabel
		cp.EditLabel = &v
	}
	return &cp
}

func cloneNodeRuntime(node *domain.NodeRuntime) *domain.NodeRuntime {
	if node == nil {
		return nil
	}
	cp := *node
	cp.Output = deepCloneMap(node.Output)
	cp.Checkpoint = deepCloneMap(node.Checkpoint)
	cp.ResolvedInput = deepCloneMap(node.ResolvedInput)
	cp.ActivatedEdges = cloneBoolMap(node.ActivatedEdges)
	if node.StartedAt != nil {
		v := *node.StartedAt
		cp.StartedAt = &v
	}
	if node.FinishedAt != nil {
		v := *node.FinishedAt
		cp.FinishedAt = &v
	}
	if node.LastHeartbeat != nil {
		v := *node.LastHeartbeat
		cp.LastHeartbeat = &v
	}
	if node.ReusedFromTaskID != nil {
		v := *node.ReusedFromTaskID
		cp.ReusedFromTaskID = &v
	}
	if node.ReusedFromNode != nil {
		v := *node.ReusedFromNode
		cp.ReusedFromNode = &v
	}
	if node.CheckpointedAt != nil {
		v := *node.CheckpointedAt
		cp.CheckpointedAt = &v
	}
	if node.PatchedAt != nil {
		v := *node.PatchedAt
		cp.PatchedAt = &v
	}
	if node.MaterializedAt != nil {
		v := *node.MaterializedAt
		cp.MaterializedAt = &v
	}
	if node.LastPatchLabel != nil {
		v := *node.LastPatchLabel
		cp.LastPatchLabel = &v
	}
	return &cp
}

func legacyBuildSubWorkflowKey(parentTaskID int64, nodeName string, workflowName string, input map[string]any) string {
	b, _ := json.Marshal(input)
	return fmt.Sprintf("%d-%s-%s-%x", parentTaskID, nodeName, workflowName, md5.Sum(b))
}

func getSecondFilePathFromAnyResults(t *testing.T, raw any) string {
	t.Helper()
	results, ok := raw.([]any)
	require.True(t, ok)
	require.Len(t, results, 2)

	item, ok := results[1].(map[string]any)
	require.True(t, ok)

	path, ok := item["file_path"].(string)
	require.True(t, ok)
	return path
}

func intPtr(v int) *int {
	return &v
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// calculateOutputHash mirrors nodes.calculateOutputHash for test use.
func calculateOutputHash(output map[string]any) string {
	if output == nil {
		return ""
	}
	normalized := utils.NormalizeMap(output)
	b, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", md5.Sum(b))
}
