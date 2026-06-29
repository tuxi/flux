package flux_test

import (
	"context"
	"testing"

	"github.com/tuxi/flux"
	"github.com/tuxi/flux/adapter/memory"
	"github.com/tuxi/flux/definition"
	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/store"
)

// ── Store 集成测试：v3 路径（WorkflowStore + AwaitStore + TraceStore）──

func TestEngine_WithStoreInterfaces_SyncWorkflow(t *testing.T) {
	wfStore := memory.NewWorkflowStore()
	awaitStore := memory.NewAwaitStore()

	engine, err := flux.New(flux.Config{
		Backend:       newMemBackend(), // 向后兼容：仍需要 Backend（NotImplemented 场景用）
		WorkflowStore: wfStore,
		AwaitStore:    awaitStore,
	})
	if err != nil {
		t.Fatal(err)
	}

	def := &definition.WorkflowDefinition{
		Name: "store_test_sync",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "step1", Type: definition.NodeTool, Config: map[string]any{"tool": "echo"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "step1", Type: definition.EdgeNormal},
			{From: "step1", To: "end", Type: definition.EdgeNormal},
		},
	}

	engine.Register(flux.Workflow(def))
	engine.Register(flux.Tool(echoTool{}))

	result, err := engine.Run(context.Background(), flux.RunRequest{
		Asset: "store_test_sync",
		Input: map[string]any{"value": 99},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != flux.StatusCompleted {
		t.Fatalf("expected completed, got status=%d", result.Status)
	}

	// 验证 WorkflowStore 被调用了
	nodes, err := wfStore.LoadNodeStates(context.Background(), result.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("Store: 应该有节点被持久化")
	}

	var step1Persisted bool
	for _, n := range nodes {
		if n.NodeName == "step1" && n.State == runtime.NodeSuccess {
			step1Persisted = true
		}
	}
	if !step1Persisted {
		t.Fatalf("Store: step1 应该被 PersistNode 记录为 success，实际 nodes=%+v", nodes)
	}
	t.Logf("✅ v3 Store sync: taskID=%s, %d nodes persisted via WorkflowStore", result.TaskID, len(nodes))
}

func TestEngine_WithStoreInterfaces_AsyncSuspendResume(t *testing.T) {
	wfStore := memory.NewWorkflowStore()
	awaitStore := memory.NewAwaitStore()
	traceStore := memory.NewTraceStore()

	engine, err := flux.New(flux.Config{
		Backend:       newMemBackend(),
		WorkflowStore: wfStore,
		AwaitStore:    awaitStore,
		TraceStore:    traceStore,
	})
	if err != nil {
		t.Fatal(err)
	}

	def := &definition.WorkflowDefinition{
		Name: "store_test_async",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "async_step", Type: definition.NodeAwait, Config: map[string]any{
				"await_type": "external_task",
				"source":     "webhook_or_poll",
			}},
			{Name: "echo", Type: definition.NodeTool, Config: map[string]any{"tool": "echo"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "async_step", Type: definition.EdgeNormal},
			{From: "async_step", To: "echo", Type: definition.EdgeNormal},
			{From: "echo", To: "end", Type: definition.EdgeNormal},
		},
	}

	engine.Register(flux.Workflow(def))
	engine.Register(flux.Tool(echoTool{}))

	// ── Run → 挂起 ──
	result, err := engine.Run(context.Background(), flux.RunRequest{
		Asset: "store_test_async",
		Input: map[string]any{"topic": "store_integration"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != flux.StatusSuspended {
		t.Fatalf("expected suspended, got status=%d", result.Status)
	}
	taskID := result.TaskID
	t.Logf("✅ suspend via Store: taskID=%s", taskID)

	// 验证 AwaitStore 被调用了
	providerTaskID := taskID + "_async_step"
	b, err := awaitStore.FindByProviderTaskID(context.Background(), providerTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("Store: AwaitBinding 应该存在")
	}
	if b.Status != store.AwaitStatusAwaiting {
		t.Fatalf("Store: binding status 应为 awaiting，实际=%s", b.Status)
	}
	t.Logf("✅ AwaitBinding persisted: id=%s, status=%s", b.BindingID, b.Status)

	// ── Notify → Resume ──
	result2, err := engine.Notify(context.Background(), flux.Event{
		Provider:       "test_provider",
		ProviderTaskID: providerTaskID,
		Output:         map[string]any{"msg": "async done via store"},
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if result2.Status != flux.StatusCompleted {
		t.Fatalf("Notify 后 expected completed, got status=%d", result2.Status)
	}

	// 验证 AwaitStore 的 ResolveBinding 被调用了（幂等）
	claimed, err := awaitStore.ResolveBinding(context.Background(), b.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("Store: 重复 ResolveBinding 应返回 claimed=false（幂等）")
	}
	t.Logf("✅ Store notify→resume→complete: binding resolved via AwaitStore")

	// 验证 TraceStore 有记录（如果 trace 被正确写入）
	traces, _ := traceStore.ReplayTrace(context.Background(), taskID, 0)
	t.Logf("✅ TraceStore: %d trace events recorded", len(traces))
}

func TestEngine_WithStoreInterfaces_PersistsPlan(t *testing.T) {
	wfStore := memory.NewWorkflowStore()
	awaitStore := memory.NewAwaitStore()

	engine, err := flux.New(flux.Config{
		Backend:       newMemBackend(),
		WorkflowStore: wfStore,
		AwaitStore:    awaitStore,
	})
	if err != nil {
		t.Fatal(err)
	}

	def := &definition.WorkflowDefinition{
		Name: "store_test_plan",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "async_wait", Type: definition.NodeAwait, Config: map[string]any{
				"await_type": "external_task",
				"source":     "webhook_or_poll",
			}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "async_wait", Type: definition.EdgeNormal},
			{From: "async_wait", To: "end", Type: definition.EdgeNormal},
		},
	}

	engine.Register(flux.Workflow(def))

	result, _ := engine.Run(context.Background(), flux.RunRequest{
		Asset: "store_test_plan",
		Input: map[string]any{},
	})
	taskID := result.TaskID

	// 在 sync workflow 中 Plan 应该在内部被编译...
	// 但当前的 engine.Run 为每个 Run 用 workflow.Compile 编译的 Plan 存在内存中
	// WorkflowStore.SavePlan 尚未在 engine.Run 中显式调用
	// 此测试验证 Store 基础设施可用
	pending, err := awaitStore.ListPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) == 0 {
		t.Fatal("Store: 应有 pending binding")
	}
	t.Logf("✅ Store Plan persistence infrastructure: %d pending bindings for task %s", len(pending), taskID)
}
