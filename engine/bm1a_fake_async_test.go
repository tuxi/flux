package engine

// B-M1a：FakeAsyncProvider — 全链路 async 验证。
//
// engine.RunWithResult() 从零启动 → 执行到异步节点 → executeAwaitNode 创建 binding +
// WorkflowSuspendedError 挂起 → 外部模拟 async 完成 → CompleteAwaitNode →
// ResumeTask → runDAG 恢复 → 后继节点执行 → TaskSuccess。
//
// 这是 B 的命门——ResumeTask 在真实 engine DAG 循环中被首次完整压测。
// 不碰 Redis、不碰外部 API、不引入分布式锁（用 newEngineForTests 的 fakes）。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/tool"
	"flux/workflow"
	"flux/workflow/nodes"

	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// echoTool 回显 input——充当后继节点，验证数据从上游 async 节点流到下游。
type echoTool struct{}

func (echoTool) Name() string                                { return "echo" }
func (echoTool) Description() string                         { return "回显 input" }
func (echoTool) InputSchema() tool.DataSchema                { return tool.DataSchema{} }
func (echoTool) OutputSchema() tool.DataSchema               { return tool.DataSchema{} }
func (echoTool) Mode() tool.ExecutionMode                    { return tool.SyncExecution }
func (echoTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(input), nil
}

func bm1aWorkflow(t *testing.T) (*definition.WorkflowDefinition, *workflow.Builder) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Register(echoTool{})
	builder := workflow.NewBuilder(nodes.InitNodeRegistry(reg))

	def := &definition.WorkflowDefinition{
		Name: "bm1a_fake_async",
		Output: definition.OutputDefinition{
			ResultType: "text",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name: "async_hello",
				Type: definition.NodeAwait, // ← 走 executeAwaitNode 路径
				Config: map[string]any{
					"await_type": "external_task",
					"source":     "webhook_or_poll",
				},
			},
			{
				Name:   "echo",
				Type:   definition.NodeTool,
				Config: map[string]any{"tool": "echo"},
				// B-M1a 聚焦 async 链路，暂不引入 InputMapping 表达式
				//（NodeSuccessPendingEdges 阶段输出尚未可用，expr 求值需 edge 激活后）
			},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "async_hello", Type: definition.EdgeNormal},
			{From: "async_hello", To: "echo", Type: definition.EdgeNormal},
			{From: "echo", To: "end", Type: definition.EdgeNormal},
		},
	}
	return def, builder
}

func TestBM1a_FakeAsyncProvider_FullChain(t *testing.T) {
	def, builder := bm1aWorkflow(t)

	dbWorkflow := &domain.Workflow{ID: 101, Name: def.Name}
	version := &domain.WorkflowVersion{ID: 201, WorkflowID: dbWorkflow.ID, Version: 1}
	defJSON, _ := json.Marshal(def)
	version.DefinitionJSON = datatypes.JSON(defJSON)

	taskRepo := newFakeTaskRepo()
	nodeRepo := newFakeNodeRepo()
	awaitRepo := newFakeAwaitBindingRepo()

	e := newEngineForTests(builder, taskRepo, nodeRepo,
		newFakeWorkflowVersionRepo(version), newFakeWorkflowRepo(dbWorkflow))
	e.awaitBindingRepo = awaitRepo

	task := &domain.Task{
		ID:                501,
		RootID:            501,
		Status:            domain.TaskPending,
		WorkflowVersionID: version.ID,
	}
	inputJSON, _ := json.Marshal(map[string]any{"topic": "B-M1a"})
	task.InputJSON = inputJSON

	_ = taskRepo.Create(context.Background(), task)

	// 为新 task 手动初始化 runtime（RunWithResult 对非 fork task 不执行 MaterializeRunPlan）
	wf, _ := builder.Build(def)
	runCtx := e.newRunContext(context.Background(), task, wf)
	_ = e.loadOrInitRuntime(runCtx, wf)
	plan, planErr := e.BuildRunPlan(runCtx, wf, runCtx.Input)
	require.NoError(t, planErr)
	_ = e.MaterializeRunPlan(runCtx, wf, plan)

	// ── 阶段 1：RunWithResult → 应挂起在 async_hello ──
	result := e.RunWithResult(context.Background(), task, def)
	require.Equal(t, RunSuspended, result.Status, "engine 应在 async 节点挂起, got=%s", result.Status)

	// 查 binding
	binding, bindErr := awaitRepo.GetByTaskAndNode(context.Background(), task.ID, "async_hello")
	require.NoError(t, bindErr)
	require.NotNil(t, binding, "应创建 async_hello 的 AwaitBinding")
	require.Equal(t, domain.AwaitBindingWaiting, binding.Status)

	// 查 node 状态
	node, nodeErr := nodeRepo.FindByTaskIDAndNode(context.Background(), task.ID, "async_hello")
	require.NoError(t, nodeErr)
	require.Equal(t, domain.NodeAwaiting, node.State, "async 节点应挂起")

	// echo 还没跑
	echoNode, _ := nodeRepo.FindByTaskIDAndNode(context.Background(), task.ID, "echo")
	require.Equal(t, domain.NodePending, echoNode.State, "后继节点不应在挂起时执行")

	t.Logf("B-M1a ✅ suspend：task=%d node=%s → %s, binding=%d status=%s",
		task.ID, node.Name, node.State, binding.ID, binding.Status)

	// ── 阶段 2：模拟 async 完成 → ResumeTask ──
	time.Sleep(10 * time.Millisecond)

	resumeResult := e.CompleteAwaitNode(binding.ID, map[string]any{
		"message": "hello from async world!",
	}, "", "test:bm1a")

	if resumeResult.Status != RunSuccess {
		t.Logf("CompleteAwaitNode 失败: status=%s err=%v", resumeResult.Status, resumeResult.Err)
	}
	require.Equal(t, RunSuccess, resumeResult.Status,
		"CompleteAwaitNode 应成功恢复 task（ResumeTask 通过）")

	// 查 task
	updated, _ := taskRepo.GetByID(context.Background(), task.ID)
	require.Equal(t, domain.TaskSuccess, updated.Status, "task 应最终成功")

	// async_hello 应完成
	nodeAfter, _ := nodeRepo.FindByTaskIDAndNode(context.Background(), task.ID, "async_hello")
	require.Equal(t, domain.NodeSuccess, nodeAfter.State)
	require.Equal(t, "hello from async world!", nodeAfter.Output["message"])

	// echo 执行成功
	echoAfter, _ := nodeRepo.FindByTaskIDAndNode(context.Background(), task.ID, "echo")
	require.Equal(t, domain.NodeSuccess, echoAfter.State)

	// binding 应 completed
	bindingAfter, _ := awaitRepo.GetByID(context.Background(), binding.ID)
	require.Equal(t, domain.AwaitBindingCompleted, bindingAfter.Status)

	t.Logf("B-M1a ✅ resume：task=%d → %s, async=%s, echo=%s",
		task.ID, updated.Status, nodeAfter.State, echoAfter.State)
	t.Log("B-M1a ✅✅ 命门通过：RunWithResult → async suspend → CompleteAwaitNode → ResumeTask → runDAG → TaskSuccess")
}
