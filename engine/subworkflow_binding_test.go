package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"flux/domain"
	"flux/workflow/nodes"
	"github.com/stretchr/testify/require"
)

// upsertSubWorkflowBinding 应创建一条 AwaitTypeSubWorkflow 的 waiting binding，
// 且对同一 (task, node) 重复调用走更新而非新建（子任务复活场景）。
func TestUpsertSubWorkflowBinding_CreatesAndIsIdempotent(t *testing.T) {
	awaitRepo := newFakeAwaitBindingRepo()
	e := &Engine{awaitBindingRepo: awaitRepo}
	runCtx := &nodes.Context{
		Ctx:  context.Background(),
		Task: &domain.Task{ID: 5001, RootID: 5001, WorkflowVersionID: 77},
	}

	require.NoError(t, e.upsertSubWorkflowBinding(runCtx, "story_t2i", 6001, "subkey-1"))

	b, err := awaitRepo.GetByTaskAndNode(context.Background(), 5001, "story_t2i")
	require.NoError(t, err)
	require.NotNil(t, b)
	require.Equal(t, domain.AwaitTypeSubWorkflow, b.AwaitType)
	require.Equal(t, domain.AwaitSourceSubWorkflow, b.Source)
	require.Equal(t, domain.AwaitBindingWaiting, b.Status)
	// child_task_id 以字符串存（雪花 int64 经 JSON 会丢精度）。
	require.Equal(t, "6001", b.Correlation["child_task_id"])
	require.Equal(t, "subkey-1", b.Correlation["sub_key"])
	require.NotNil(t, b.NextPollAt)
	require.Equal(t, 1, awaitRepo.createCount)

	// 复活场景：同一节点再次绑定到新的子任务 id，应更新而非新建。
	require.NoError(t, e.upsertSubWorkflowBinding(runCtx, "story_t2i", 6002, "subkey-1"))
	require.Equal(t, 1, awaitRepo.createCount, "should reuse existing binding row, not create a new one")

	b2, err := awaitRepo.GetByTaskAndNode(context.Background(), 5001, "story_t2i")
	require.NoError(t, err)
	require.Equal(t, "6002", b2.Correlation["child_task_id"])
	require.Equal(t, domain.AwaitBindingWaiting, b2.Status)
}

// 已是终态的 binding（子任务被取消/失败后复活）再次 upsert 时应被重新置为 waiting。
func TestUpsertSubWorkflowBinding_ReactivatesTerminalBinding(t *testing.T) {
	awaitRepo := newFakeAwaitBindingRepo(&domain.AwaitBinding{
		ID:        1,
		TaskID:    5001,
		NodeName:  "story_t2i",
		AwaitType: domain.AwaitTypeSubWorkflow,
		Source:    domain.AwaitSourceSubWorkflow,
		Status:    domain.AwaitBindingCanceled,
	})
	e := &Engine{awaitBindingRepo: awaitRepo}
	runCtx := &nodes.Context{Ctx: context.Background(), Task: &domain.Task{ID: 5001, RootID: 5001}}

	require.NoError(t, e.upsertSubWorkflowBinding(runCtx, "story_t2i", 6003, "subkey-1"))

	b, _ := awaitRepo.GetByTaskAndNode(context.Background(), 5001, "story_t2i")
	require.Equal(t, domain.AwaitBindingWaiting, b.Status)
	require.Equal(t, "6003", b.Correlation["child_task_id"])
	require.Equal(t, 0, awaitRepo.createCount)
}

// 事件快路径：存在 subworkflow binding 时，子任务完成应通过 CompleteAwaitNode 唤醒挂起的父任务。
func TestSubWorkflowBinding_CompletionResumesParent(t *testing.T) {
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)

	parent := &domain.Task{ID: 9101, RootID: 9101, Status: domain.TaskSuspended, WorkflowVersionID: version.ID}
	taskRepo := newFakeTaskRepo(parent)
	nodeRepo := newFakeNodeRepo()
	nodeRepo.nodes[parent.ID] = map[string]*domain.NodeRuntime{
		"start": {
			TaskID:         parent.ID,
			Name:           "start",
			State:          domain.NodeSuccess,
			ActivatedEdges: map[string]bool{"start->async_generate": true},
			Output:         map[string]any{},
			ResolvedInput:  map[string]any{},
		},
		// 模拟 P1：subworkflow 父节点挂起后落在 NodeAwaiting。
		"async_generate": {
			TaskID:         parent.ID,
			Name:           "async_generate",
			State:          domain.NodeAwaiting,
			ActivatedEdges: map[string]bool{"async_generate->end": true},
		},
		"end": {TaskID: parent.ID, Name: "end", State: domain.NodePending},
	}

	awaitRepo := newFakeAwaitBindingRepo(&domain.AwaitBinding{
		ID:                8001,
		TaskID:            parent.ID,
		RootTaskID:        parent.RootID,
		NodeName:          "async_generate",
		WorkflowVersionID: version.ID,
		AwaitType:         domain.AwaitTypeSubWorkflow,
		Source:            domain.AwaitSourceSubWorkflow,
		Status:            domain.AwaitBindingWaiting,
		Correlation:       map[string]any{"child_task_id": int64(9102), "sub_key": "k"},
	})

	e := newEngineForTests(builder, taskRepo, nodeRepo, newFakeWorkflowVersionRepo(version), newFakeWorkflowRepo(dbWorkflow))
	e.awaitBindingRepo = awaitRepo

	handled := e.tryCompleteSubWorkflowBinding(parent.ID, "async_generate", map[string]any{"url": "https://child/out.png"}, "")
	require.True(t, handled, "binding should route completion via CompleteAwaitNode")

	updated, err := taskRepo.GetByID(context.Background(), parent.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TaskSuccess, updated.Status)

	rt, err := nodeRepo.FindByTaskIDAndNode(context.Background(), parent.ID, "async_generate")
	require.NoError(t, err)
	require.Equal(t, domain.NodeSuccess, rt.State)

	b, err := awaitRepo.GetByID(context.Background(), 8001)
	require.NoError(t, err)
	require.Equal(t, domain.AwaitBindingCompleted, b.Status)
}

// 无 binding 时应回退旧路径（返回 false）。
func TestTryCompleteSubWorkflowBinding_FallbackWhenNoBinding(t *testing.T) {
	e := &Engine{awaitBindingRepo: newFakeAwaitBindingRepo()}
	require.False(t, e.tryCompleteSubWorkflowBinding(1, "n", nil, ""))
}

// 非 subworkflow 类型的 binding（如 await 外部任务）不应被 subworkflow 完成路径接管。
func TestTryCompleteSubWorkflowBinding_IgnoresNonSubworkflowBinding(t *testing.T) {
	awaitRepo := newFakeAwaitBindingRepo(&domain.AwaitBinding{
		ID:        1,
		TaskID:    1,
		NodeName:  "n",
		AwaitType: domain.AwaitTypeExternalTask,
		Status:    domain.AwaitBindingWaiting,
	})
	e := &Engine{awaitBindingRepo: awaitRepo}
	require.False(t, e.tryCompleteSubWorkflowBinding(1, "n", nil, ""))
}

// ===== P2: AwaitPollWorker 的 subworkflow 对账兜底 =====

// newSubWorkflowReconcileFixture 构造一个挂起的父任务（async_generate 落 NodeAwaiting）
// + 一个指定状态的子任务 + 一条 waiting 的 subworkflow binding（child_task_id 以字符串存）。
func newSubWorkflowReconcileFixture(
	t *testing.T,
	childStatus domain.TaskStatus,
	childFinal map[string]any,
	childErr string,
	nextPollAt *time.Time,
) (*Engine, *fakeTaskRepo, *fakeNodeRepo, *fakeAwaitBindingRepo) {
	t.Helper()
	builder, _, dbWorkflow, version := newAsyncResumeWorkflow(t)

	parent := &domain.Task{ID: 9201, RootID: 9201, Status: domain.TaskSuspended, WorkflowVersionID: version.ID}
	parentNode := "async_generate"

	var childOut []byte
	if childFinal != nil {
		childOut, _ = json.Marshal(map[string]any{"final": childFinal})
	}
	child := &domain.Task{
		ID:           9202,
		ParentID:     &parent.ID,
		RootID:       parent.RootID,
		ParentNode:   &parentNode,
		Status:       childStatus,
		OutputJSON:   childOut,
		ErrorMessage: childErr,
	}

	taskRepo := newFakeTaskRepo(parent, child)
	nodeRepo := newFakeNodeRepo()
	nodeRepo.nodes[parent.ID] = map[string]*domain.NodeRuntime{
		"start": {
			TaskID:         parent.ID,
			Name:           "start",
			State:          domain.NodeSuccess,
			ActivatedEdges: map[string]bool{"start->async_generate": true},
			Output:         map[string]any{},
			ResolvedInput:  map[string]any{},
		},
		"async_generate": {
			TaskID:         parent.ID,
			Name:           "async_generate",
			State:          domain.NodeAwaiting,
			ActivatedEdges: map[string]bool{"async_generate->end": true},
		},
		"end": {TaskID: parent.ID, Name: "end", State: domain.NodePending},
	}

	awaitRepo := newFakeAwaitBindingRepo(&domain.AwaitBinding{
		ID:                8201,
		TaskID:            parent.ID,
		RootTaskID:        parent.RootID,
		NodeName:          parentNode,
		WorkflowVersionID: version.ID,
		AwaitType:         domain.AwaitTypeSubWorkflow,
		Source:            domain.AwaitSourceSubWorkflow,
		Status:            domain.AwaitBindingWaiting,
		// 用字符串覆盖写入态，顺带验证大整数无损解析。
		Correlation: map[string]any{"child_task_id": "9202", "sub_key": "k"},
		NextPollAt:  nextPollAt,
	})

	e := newEngineForTests(builder, taskRepo, nodeRepo, newFakeWorkflowVersionRepo(version), newFakeWorkflowRepo(dbWorkflow))
	e.awaitBindingRepo = awaitRepo
	return e, taskRepo, nodeRepo, awaitRepo
}

// 子任务已成功但唤醒事件丢失：poll 对账应直接查到子任务终态并唤醒父任务（核心兜底）。
func TestReconcileSubWorkflowBinding_ChildSuccessResumesParent(t *testing.T) {
	e, taskRepo, nodeRepo, awaitRepo := newSubWorkflowReconcileFixture(
		t, domain.TaskSuccess, map[string]any{"url": "https://child/out.png"}, "", nil)

	res := e.ReconcileSubWorkflowBinding(8201)
	require.Equal(t, RunSuccess, res.Status)

	parent, _ := taskRepo.GetByID(context.Background(), 9201)
	require.Equal(t, domain.TaskSuccess, parent.Status)

	rt, _ := nodeRepo.FindByTaskIDAndNode(context.Background(), 9201, "async_generate")
	require.Equal(t, domain.NodeSuccess, rt.State)

	b, _ := awaitRepo.GetByID(context.Background(), 8201)
	require.Equal(t, domain.AwaitBindingCompleted, b.Status)
}

// 子任务仍在执行：只重排下次对账，不动 binding 终态，父任务保持挂起。
func TestReconcileSubWorkflowBinding_ChildRunningReschedules(t *testing.T) {
	orig := time.Now().Add(-time.Minute)
	e, taskRepo, _, awaitRepo := newSubWorkflowReconcileFixture(
		t, domain.TaskRunning, nil, "", &orig)

	res := e.ReconcileSubWorkflowBinding(8201)
	require.Equal(t, RunNoop, res.Status)

	b, _ := awaitRepo.GetByID(context.Background(), 8201)
	require.Equal(t, domain.AwaitBindingWaiting, b.Status, "still waiting for the running child")
	require.NotNil(t, b.NextPollAt)
	require.True(t, b.NextPollAt.After(orig), "next poll should be pushed forward")
	require.Equal(t, 1, b.PollAttempts)

	parent, _ := taskRepo.GetByID(context.Background(), 9201)
	require.Equal(t, domain.TaskSuspended, parent.Status, "parent stays suspended")
}

// 子任务卡在 pending：重新入队 + 重排对账。
func TestReconcileSubWorkflowBinding_ChildPendingReEnqueues(t *testing.T) {
	e, taskRepo, _, awaitRepo := newSubWorkflowReconcileFixture(
		t, domain.TaskPending, nil, "", nil)

	res := e.ReconcileSubWorkflowBinding(8201)
	require.Equal(t, RunNoop, res.Status)

	require.Contains(t, taskRepo.enqueues, int64(9202), "pending child should be re-enqueued")

	b, _ := awaitRepo.GetByID(context.Background(), 8201)
	require.Equal(t, domain.AwaitBindingWaiting, b.Status)
	require.Equal(t, 1, b.PollAttempts)
}

// 子任务终态失败：把 binding 完成为失败（父节点失败后由父任务重试 + RunSubWorkflow 复活子任务）。
func TestReconcileSubWorkflowBinding_ChildFailedFailsBinding(t *testing.T) {
	e, _, nodeRepo, awaitRepo := newSubWorkflowReconcileFixture(
		t, domain.TaskFailed, nil, "child boom", nil)

	e.ReconcileSubWorkflowBinding(8201)

	rt, _ := nodeRepo.FindByTaskIDAndNode(context.Background(), 9201, "async_generate")
	require.Equal(t, domain.NodeFailed, rt.State)

	b, _ := awaitRepo.GetByID(context.Background(), 8201)
	require.Equal(t, domain.AwaitBindingFailed, b.Status)
}

// child_task_id 解析：字符串（雪花大整数无损）+ 兼容历史数字形态 + 缺失返回 0。
func TestSubWorkflowChildIDFromCorrelation(t *testing.T) {
	require.EqualValues(t, int64(2060177765247758336),
		subWorkflowChildIDFromCorrelation(map[string]any{"child_task_id": "2060177765247758336"}))
	require.EqualValues(t, int64(42), subWorkflowChildIDFromCorrelation(map[string]any{"child_task_id": int64(42)}))
	require.EqualValues(t, int64(42), subWorkflowChildIDFromCorrelation(map[string]any{"child_task_id": float64(42)}))
	require.Zero(t, subWorkflowChildIDFromCorrelation(nil))
	require.Zero(t, subWorkflowChildIDFromCorrelation(map[string]any{}))
}
