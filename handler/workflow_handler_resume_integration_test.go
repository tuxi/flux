package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"flux/internal/consts"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flux/definition"
	"flux/domain"
	aidto "flux/dto"
	"flux/eventbus"
	repository2 "flux/repository"
	"flux/service"
	"flux/tool"
	"flux/workflow"
	wfnodes "flux/workflow/nodes"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resumeFlowTaskRepo struct {
	tasks    map[int64]*domain.Task
	bySubKey map[string]*domain.Task
	enqueued []int64
}

func newResumeFlowTaskRepo(tasks ...*domain.Task) *resumeFlowTaskRepo {
	repo := &resumeFlowTaskRepo{
		tasks:    map[int64]*domain.Task{},
		bySubKey: map[string]*domain.Task{},
	}
	for _, task := range tasks {
		if task == nil {
			continue
		}
		cp := cloneTask(task)
		repo.tasks[cp.ID] = cp
		if cp.SubKey != nil && *cp.SubKey != "" {
			repo.bySubKey[*cp.SubKey] = cp
		}
	}
	return repo
}

func (r *resumeFlowTaskRepo) Create(ctx context.Context, task *domain.Task) error { return nil }
func (r *resumeFlowTaskRepo) GetByID(ctx context.Context, id int64) (*domain.Task, error) {
	task := r.tasks[id]
	if task == nil {
		return nil, nil
	}
	return cloneTask(task), nil
}
func (r *resumeFlowTaskRepo) Update(ctx context.Context, task *domain.Task) error {
	cp := cloneTask(task)
	r.tasks[cp.ID] = cp
	if cp.SubKey != nil && *cp.SubKey != "" {
		r.bySubKey[*cp.SubKey] = cp
	}
	return nil
}
func (r *resumeFlowTaskRepo) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (r *resumeFlowTaskRepo) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	return nil, nil
}
func (r *resumeFlowTaskRepo) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	return nil, nil
}
func (r *resumeFlowTaskRepo) ListByUser(ctx context.Context, userID int64, params aidto.PageRequest) ([]*domain.Task, int64, error) {
	return nil, 0, nil
}
func (r *resumeFlowTaskRepo) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	out := make([]*domain.Task, 0)
	for _, task := range r.tasks {
		if task == nil || task.ParentID == nil || *task.ParentID != parentID {
			continue
		}
		out = append(out, cloneTask(task))
	}
	return out, nil
}
func (r *resumeFlowTaskRepo) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	return nil
}
func (r *resumeFlowTaskRepo) Enqueue(ctx context.Context, taskID int64) error {
	r.enqueued = append(r.enqueued, taskID)
	return nil
}
func (r *resumeFlowTaskRepo) TryClaimTask(ctx context.Context, taskID int64, workerID string) (bool, error) {
	return false, nil
}
func (r *resumeFlowTaskRepo) FindBySubKey(ctx context.Context, subKey string) (*domain.Task, error) {
	task := r.bySubKey[subKey]
	if task == nil {
		return nil, nil
	}
	return cloneTask(task), nil
}
func (r *resumeFlowTaskRepo) ListByParentNode(ctx context.Context, parentID int64, nodeName string) ([]*domain.Task, error) {
	out := make([]*domain.Task, 0)
	for _, task := range r.tasks {
		if task == nil || task.ParentID == nil || task.ParentNode == nil {
			continue
		}
		if *task.ParentID != parentID || *task.ParentNode != nodeName {
			continue
		}
		out = append(out, cloneTask(task))
	}
	return out, nil
}
func (r *resumeFlowTaskRepo) CreateFork(ctx context.Context, source *domain.Task, newTaskID int64, newInput []byte, editAction, editLabel string) (*domain.Task, error) {
	return nil, nil
}
func (r *resumeFlowTaskRepo) ListByUserV2(ctx context.Context, userID int64, req aidto.TaskListReq) ([]*aidto.Task, int64, error) {
	return nil, 0, nil
}
func (r *resumeFlowTaskRepo) GetRootTaskByIDAndUser(ctx context.Context, taskID int64, userID int64) (*domain.Task, error) {
	return nil, nil
}
func (r *resumeFlowTaskRepo) GetTaskDetail(ctx context.Context, taskID int64) (*aidto.TaskDetail, error) {
	return nil, nil
}

type resumeFlowNodeRepo struct {
	nodes map[int64]map[string]*domain.NodeRuntime
}

func newResumeFlowNodeRepo() *resumeFlowNodeRepo {
	return &resumeFlowNodeRepo{nodes: map[int64]map[string]*domain.NodeRuntime{}}
}

func (r *resumeFlowNodeRepo) put(taskID int64, node *domain.NodeRuntime) {
	if node == nil {
		return
	}
	if r.nodes[taskID] == nil {
		r.nodes[taskID] = map[string]*domain.NodeRuntime{}
	}
	r.nodes[taskID][node.Name] = cloneRuntime(node)
}

func (r *resumeFlowNodeRepo) Create(ctx context.Context, n *domain.NodeRuntime) error { return nil }
func (r *resumeFlowNodeRepo) Update(ctx context.Context, n *domain.NodeRuntime) error {
	r.put(n.TaskID, n)
	return nil
}
func (r *resumeFlowNodeRepo) FindByTaskID(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error) {
	nodeMap := r.nodes[taskID]
	out := make([]*domain.NodeRuntime, 0, len(nodeMap))
	for _, node := range nodeMap {
		out = append(out, cloneRuntime(node))
	}
	return out, nil
}
func (r *resumeFlowNodeRepo) FindByTaskIDAndNode(ctx context.Context, taskID int64, node string) (*domain.NodeRuntime, error) {
	nodeMap := r.nodes[taskID]
	if nodeMap == nil || nodeMap[node] == nil {
		return nil, nil
	}
	return cloneRuntime(nodeMap[node]), nil
}
func (r *resumeFlowNodeRepo) MarkRunningAsRetrying(ctx context.Context, taskID int64) error {
	return nil
}
func (r *resumeFlowNodeRepo) MarkAsRetrying(ctx context.Context, taskID int64, name string) error {
	return nil
}
func (r *resumeFlowNodeRepo) MarkFailed(ctx context.Context, taskID int64, name string, errMessage string) error {
	return nil
}
func (r *resumeFlowNodeRepo) FindExpiredRunningNodes(ctx context.Context, expire time.Time) ([]*domain.NodeRuntime, error) {
	return nil, nil
}
func (r *resumeFlowNodeRepo) AttemptCompletePendingEdges(ctx context.Context, taskID int64, nodeName string, output map[string]any, errMsg string) (bool, error) {
	return false, nil
}
func (r *resumeFlowNodeRepo) CloneCheckpoint(ctx context.Context, fromTaskID, toTaskID int64) error {
	return nil
}

type resumeFlowWorkflowVersionRepo struct {
	versions map[int64]*domain.WorkflowVersion
}

func (r *resumeFlowWorkflowVersionRepo) Create(ctx context.Context, version *domain.WorkflowVersion) error {
	return nil
}
func (r *resumeFlowWorkflowVersionRepo) Get(ctx context.Context, id int64) (*domain.WorkflowVersion, error) {
	if r.versions[id] == nil {
		return nil, nil
	}
	cp := *r.versions[id]
	cp.DefinitionJSON = append([]byte(nil), r.versions[id].DefinitionJSON...)
	return &cp, nil
}
func (r *resumeFlowWorkflowVersionRepo) GetLatestByWorkflowID(ctx context.Context, id int64) (*domain.WorkflowVersion, error) {
	return nil, nil
}
func (r *resumeFlowWorkflowVersionRepo) GetLatestByWorkflowName(ctx context.Context, name string) (*domain.WorkflowVersion, error) {
	return nil, nil
}
func (r *resumeFlowWorkflowVersionRepo) UpdateDefinitionJSON(ctx context.Context, versionID int64, json []byte) error {
	return nil
}

type noopAwaitBindingRepo struct{}

func (noopAwaitBindingRepo) Create(ctx context.Context, b *domain.AwaitBinding) error { return nil }
func (noopAwaitBindingRepo) Update(ctx context.Context, b *domain.AwaitBinding) error { return nil }
func (noopAwaitBindingRepo) GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (noopAwaitBindingRepo) ListByTaskID(ctx context.Context, taskID int64) ([]*domain.AwaitBinding, error) {
	return nil, nil
}
func (noopAwaitBindingRepo) GetByTaskAndNode(ctx context.Context, taskID int64, nodeName string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (noopAwaitBindingRepo) FindWaitingByProviderTaskID(ctx context.Context, provider, providerTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (noopAwaitBindingRepo) FindWaitingByAPITaskID(ctx context.Context, provider, apiTaskID string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (noopAwaitBindingRepo) FindWaitingBySignal(ctx context.Context, signalName, callbackToken string) (*domain.AwaitBinding, error) {
	return nil, nil
}
func (noopAwaitBindingRepo) TransitionStatus(ctx context.Context, id int64, from domain.AwaitBindingStatus, to domain.AwaitBindingStatus) (bool, error) {
	return false, nil
}
func (noopAwaitBindingRepo) ClaimCompleting(ctx context.Context, id int64, expectedStatuses []domain.AwaitBindingStatus) (bool, error) {
	return false, nil
}
func (noopAwaitBindingRepo) FindPollDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return nil, nil
}
func (noopAwaitBindingRepo) FindTimeoutDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return nil, nil
}

type resumeFlowExecutor struct {
	taskRepo repository2.TaskRepository
	nodeRepo repository2.NodeRuntimeRepository
}

func (e *resumeFlowExecutor) RunSubWorkflow(execCtx *wfnodes.NodeExecContext, workflowName string, input map[string]any) (map[string]any, error) {
	return nil, &domain.WorkflowSuspendedError{Reason: domain.SuspendSubWorkflow}
}
func (e *resumeFlowExecutor) TaskRepo() repository2.TaskRepository { return e.taskRepo }
func (e *resumeFlowExecutor) NodeRepo() repository2.NodeRuntimeRepository {
	return e.nodeRepo
}

func TestResumeTask_MapFailedChildRevivedAndParentCanComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)

	parentDef := &definition.WorkflowDefinition{
		Name: "parent_map_workflow",
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
					"workflow": "map_child_workflow",
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
	childDef := &definition.WorkflowDefinition{
		Name: "map_child_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.end.output.primary_file_url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "end", Type: definition.EdgeNormal},
		},
	}

	parentVersion := &domain.WorkflowVersion{ID: 1001, WorkflowID: 5001, DefinitionJSON: mustJSONBytes(t, parentDef)}
	childVersion := &domain.WorkflowVersion{ID: 2001, WorkflowID: 6001, DefinitionJSON: mustJSONBytes(t, childDef)}

	parentTask := &domain.Task{
		ID:                101,
		RootID:            101,
		Status:            domain.TaskFailed,
		WorkflowVersionID: parentVersion.ID,
		InputJSON:         mustJSONBytes(t, map[string]any{"items": []any{map[string]any{"frame": "a"}}}),
	}
	childSubKey := "101-map_render-map_child_workflow-item0"
	parentNode := "map_render"
	childParentID := parentTask.ID
	childTask := &domain.Task{
		ID:                202,
		RootID:            101,
		ParentID:          &childParentID,
		ParentNode:        &parentNode,
		Status:            domain.TaskFailed,
		WorkflowVersionID: childVersion.ID,
		SubKey:            &childSubKey,
		MapIndex:          intPtr(0),
		InputJSON:         mustJSONBytes(t, map[string]any{"index": 0, "item": map[string]any{"frame": "a"}}),
	}

	taskRepo := newResumeFlowTaskRepo(parentTask, childTask)
	nodeRepo := newResumeFlowNodeRepo()
	nodeRepo.put(parentTask.ID, &domain.NodeRuntime{
		TaskID: parentTask.ID,
		Name:   "map_render",
		State:  domain.NodeFailed,
		Error:  "map child task failed at index=0",
		Checkpoint: map[string]any{
			"fanout_kind":  "map",
			"total":        1,
			"done":         0,
			"results":      map[string]any{},
			"item_hashes":  map[string]any{},
			"reused_items": map[string]any{},
		},
	})
	nodeRepo.put(childTask.ID, &domain.NodeRuntime{
		TaskID: childTask.ID,
		Name:   "end",
		State:  domain.NodeFailed,
		Error:  "child failed",
	})

	registry := wfnodes.InitNodeRegistry(tool.NewRegistry())
	builder := workflow.NewBuilder(registry)
	retrySvc := service.NewTaskRetryService(
		&resumeFlowWorkflowVersionRepo{versions: map[int64]*domain.WorkflowVersion{
			parentVersion.ID: parentVersion,
			childVersion.ID:  childVersion,
		}},
		taskRepo,
		nodeRepo,
		noopAwaitBindingRepo{},
		builder,
	)
	handler := &WorkflowHandler{
		taskRepo:         taskRepo,
		taskRetryService: retrySvc,
		nodeRuntimeRepo:  nodeRepo,
		eventRepo:        stubEventRepository{},
		billingTaskSvc:   nil,
	}

	body := bytes.NewBufferString(`{"task_id":101}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/resume", body)
	c.Request.Header.Set("Content-Type", "application/json")

	handler.ResumeTask(c)

	require.Equal(t, http.StatusOK, w.Code)

	var resp aidto.ApiResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, 0, resp.Code)

	updatedParent := taskRepo.tasks[parentTask.ID]
	require.NotNil(t, updatedParent)
	require.Equal(t, domain.TaskPending, updatedParent.Status)

	updatedChild := taskRepo.tasks[childTask.ID]
	require.NotNil(t, updatedChild)
	require.Equal(t, domain.TaskPending, updatedChild.Status)

	require.ElementsMatch(t, []int64{childTask.ID, parentTask.ID}, taskRepo.enqueued)

	parentRuntime := nodeRepo.nodes[parentTask.ID]["map_render"]
	require.NotNil(t, parentRuntime)
	require.Equal(t, domain.NodePending, parentRuntime.State)
	require.Nil(t, parentRuntime.Output)
	require.Equal(t, 0.0, parentRuntime.Progress)

	childRuntime := nodeRepo.nodes[childTask.ID]["end"]
	require.NotNil(t, childRuntime)
	require.Equal(t, domain.NodePending, childRuntime.State)

	updatedChild.Status = domain.TaskSuccess
	updatedChild.ErrorMessage = ""
	updatedChild.OutputJSON = mustJSONBytes(t, map[string]any{
		"final": map[string]any{
			"result_type":      "video",
			"primary_file_url": "https://example.com/recovered.mp4",
		},
	})
	require.NoError(t, taskRepo.Update(context.Background(), updatedChild))

	step := wfnodes.NewMapStep("input.items", "item", "map_child_workflow", 1)
	execCtx := &wfnodes.NodeExecContext{
		TaskContext: &wfnodes.Context{
			Ctx: context.Background(),
			Task: &domain.Task{
				ID:                parentTask.ID,
				RootID:            parentTask.RootID,
				WorkflowVersionID: parentTask.WorkflowVersionID,
			},
			Workflow: parentDef,
			Input: map[string]any{
				"items": []any{map[string]any{"frame": "a"}},
			},
			Output: map[string]any{
				"input": map[string]any{"items": []any{map[string]any{"frame": "a"}}},
				"nodes": map[string]any{},
			},
			Runtime: map[string]*domain.NodeRuntime{
				"map_render": cloneRuntime(parentRuntime),
			},
			EventBus:       eventbus.NewEventBus(nil, nil),
			ActivatedEdges: map[string]bool{},
		},
		Input: map[string]any{
			"items": []any{map[string]any{"frame": "a"}},
		},
		Output: map[string]any{},
		NodeDef: &definition.NodeDefinition{
			Name:   "map_render",
			Type:   definition.NodeMap,
			Config: parentDef.Nodes[1].Config,
		},
		Executor: &resumeFlowExecutor{taskRepo: taskRepo, nodeRepo: nodeRepo},
	}
	execCtx.TaskContext.EnsureOutputInitialized()

	err := step.Run(execCtx)
	require.NoError(t, err)

	finalRuntime := nodeRepo.nodes[parentTask.ID]["map_render"]
	require.NotNil(t, finalRuntime)
	require.NotNil(t, finalRuntime.Output)
	results, ok := finalRuntime.Output["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	result0, ok := results[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/recovered.mp4", result0["primary_file_url"])
}

func TestCancelTask_CascadesCancelableChildren(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now()
	parentNode := "map_render"
	parentID := int64(9101)
	mapIndex := 0

	parent := &domain.Task{
		ID:        parentID,
		RootID:    parentID,
		UserID:    42,
		Status:    domain.TaskSuspended,
		UpdatedAt: now.Add(-20 * time.Minute),
		CreatedAt: now.Add(-30 * time.Minute),
	}
	childPending := &domain.Task{ID: 9102, RootID: parentID, UserID: 42, ParentID: &parentID, ParentNode: &parentNode, MapIndex: &mapIndex, Status: domain.TaskPending, OutputJSON: []byte(`{"final":{"stale":true}}`), Progress: 0.4}
	childRunning := &domain.Task{ID: 9103, RootID: parentID, UserID: 42, ParentID: &parentID, ParentNode: &parentNode, MapIndex: &mapIndex, Status: domain.TaskRunning, OutputJSON: []byte(`{"final":{"stale":true}}`), Progress: 0.7}
	childSuspended := &domain.Task{ID: 9104, RootID: parentID, UserID: 42, ParentID: &parentID, ParentNode: &parentNode, MapIndex: &mapIndex, Status: domain.TaskSuspended, OutputJSON: []byte(`{"final":{"stale":true}}`), Progress: 0.5}
	childSuccess := &domain.Task{ID: 9105, RootID: parentID, UserID: 42, ParentID: &parentID, ParentNode: &parentNode, MapIndex: &mapIndex, Status: domain.TaskSuccess, Progress: 1}
	childFailed := &domain.Task{ID: 9106, RootID: parentID, UserID: 42, ParentID: &parentID, ParentNode: &parentNode, MapIndex: &mapIndex, Status: domain.TaskFailed, ErrorMessage: "already failed"}
	grandchild := &domain.Task{ID: 9107, RootID: parentID, UserID: 42, ParentID: &childRunning.ID, ParentNode: &parentNode, Status: domain.TaskRunning, OutputJSON: []byte(`{"final":{"deep":true}}`), Progress: 0.3}

	taskRepo := newResumeFlowTaskRepo(parent, childPending, childRunning, childSuspended, childSuccess, childFailed, grandchild)
	nodeRepo := newResumeFlowNodeRepo()
	nowHeartbeat := now.Add(-time.Minute)
	nodeRepo.put(parent.ID, &domain.NodeRuntime{TaskID: parent.ID, Name: parentNode, State: domain.NodeAwaiting, LastHeartbeat: &nowHeartbeat, Progress: 0.8})
	nodeRepo.put(childPending.ID, &domain.NodeRuntime{TaskID: childPending.ID, Name: "prepare", State: domain.NodePending, Progress: 0.1})
	nodeRepo.put(childRunning.ID, &domain.NodeRuntime{TaskID: childRunning.ID, Name: "render", State: domain.NodeRunning, LastHeartbeat: &nowHeartbeat, Progress: 0.6})
	nodeRepo.put(childSuspended.ID, &domain.NodeRuntime{TaskID: childSuspended.ID, Name: "await", State: domain.NodeAwaiting, LastHeartbeat: &nowHeartbeat, Progress: 0.5})
	nodeRepo.put(childSuccess.ID, &domain.NodeRuntime{TaskID: childSuccess.ID, Name: "done", State: domain.NodeSuccess, Progress: 1})
	nodeRepo.put(grandchild.ID, &domain.NodeRuntime{TaskID: grandchild.ID, Name: "deep_render", State: domain.NodeRetrying, LastHeartbeat: &nowHeartbeat, Progress: 0.4})
	handler := NewWorkflowHandler(
		nil,
		nil,
		taskRepo,
		nil,
		nil,
		nodeRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	body, err := json.Marshal(map[string]any{"task_id": parentID})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/works/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(consts.UserID, int64(42))

	handler.CancelTask(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, domain.TaskCanceled, taskRepo.tasks[parentID].Status)
	require.Equal(t, domain.TaskCanceled, taskRepo.tasks[9102].Status)
	require.Equal(t, domain.TaskCanceled, taskRepo.tasks[9103].Status)
	require.Equal(t, domain.TaskCanceled, taskRepo.tasks[9104].Status)
	require.Equal(t, domain.TaskCanceled, taskRepo.tasks[9107].Status)
	require.Nil(t, taskRepo.tasks[9102].OutputJSON)
	require.Nil(t, taskRepo.tasks[9103].OutputJSON)
	require.Nil(t, taskRepo.tasks[9104].OutputJSON)
	require.Nil(t, taskRepo.tasks[9107].OutputJSON)
	require.Zero(t, taskRepo.tasks[9102].Progress)
	require.Zero(t, taskRepo.tasks[9103].Progress)
	require.Zero(t, taskRepo.tasks[9104].Progress)
	require.Zero(t, taskRepo.tasks[9107].Progress)
	require.Equal(t, "canceled by parent task", taskRepo.tasks[9102].ErrorMessage)
	require.Equal(t, domain.NodeCanceled, nodeRepo.nodes[parent.ID][parentNode].State)
	require.Equal(t, domain.NodeCanceled, nodeRepo.nodes[childPending.ID]["prepare"].State)
	require.Equal(t, domain.NodeCanceled, nodeRepo.nodes[childRunning.ID]["render"].State)
	require.Equal(t, domain.NodeCanceled, nodeRepo.nodes[childSuspended.ID]["await"].State)
	require.Equal(t, domain.NodeCanceled, nodeRepo.nodes[grandchild.ID]["deep_render"].State)
	require.Nil(t, nodeRepo.nodes[childRunning.ID]["render"].LastHeartbeat)
	require.Zero(t, nodeRepo.nodes[childRunning.ID]["render"].Progress)
	require.Equal(t, "canceled by parent task", nodeRepo.nodes[childRunning.ID]["render"].Error)
	require.Equal(t, domain.NodeSuccess, nodeRepo.nodes[childSuccess.ID]["done"].State)
	require.Equal(t, domain.TaskSuccess, taskRepo.tasks[9105].Status)
	require.Equal(t, domain.TaskFailed, taskRepo.tasks[9106].Status)
}

func cloneTask(task *domain.Task) *domain.Task {
	if task == nil {
		return nil
	}
	cp := *task
	cp.InputJSON = append([]byte(nil), task.InputJSON...)
	cp.OutputJSON = append([]byte(nil), task.OutputJSON...)
	if task.ParentID != nil {
		parentID := *task.ParentID
		cp.ParentID = &parentID
	}
	if task.SubKey != nil {
		subKey := *task.SubKey
		cp.SubKey = &subKey
	}
	if task.ParentNode != nil {
		parentNode := *task.ParentNode
		cp.ParentNode = &parentNode
	}
	if task.MapIndex != nil {
		mapIndex := *task.MapIndex
		cp.MapIndex = &mapIndex
	}
	return &cp
}

func cloneRuntime(runtime *domain.NodeRuntime) *domain.NodeRuntime {
	if runtime == nil {
		return nil
	}
	cp := *runtime
	cp.Output = cloneMapAny(runtime.Output)
	cp.Checkpoint = cloneMapAny(runtime.Checkpoint)
	cp.ActivatedEdges = cloneBoolMap(runtime.ActivatedEdges)
	cp.ResolvedInput = cloneMapAny(runtime.ResolvedInput)
	return &cp
}

func cloneMapAny(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = cloneAny(v)
	}
	return dst
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMapAny(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = cloneAny(x[i])
		}
		return out
	default:
		return x
	}
}

func mustJSONBytes(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
