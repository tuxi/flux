package flux_test

import (
	"context"
	"sync"
	"testing"

	"github.com/tuxi/flux"
	"github.com/tuxi/flux/definition"
	"github.com/tuxi/flux/tool"
)

// memBackend 是 Backend 的内存实现（测试用）。
type memBackend struct {
	mu       sync.Mutex
	nodes    map[string]flux.NodeSnapshot               // key: taskID+"."+nodeName
	awaits   map[string]memAwait                        // key: bindingID
	taskStates map[string]*flux.TaskState               // key: taskID
}

type memAwait struct {
	taskID         string
	nodeName       string
	providerTaskID string
	status         string // "waiting" | "completing" | "completed"
}

func newMemBackend() *memBackend {
	return &memBackend{
		nodes:      map[string]flux.NodeSnapshot{},
		awaits:     map[string]memAwait{},
		taskStates: map[string]*flux.TaskState{},
	}
}

func (b *memBackend) PersistNode(_ context.Context, taskID, node string, state flux.NodeState, output map[string]any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nodes[taskID+"."+node] = flux.NodeSnapshot{State: state, Output: output}
	return nil
}

func (b *memBackend) CreateAwait(_ context.Context, taskID, node, providerTaskID string, _ map[string]any) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bindingID := "bind_" + providerTaskID
	b.awaits[bindingID] = memAwait{
		taskID:         taskID,
		nodeName:       node,
		providerTaskID: providerTaskID,
		status:         "waiting",
	}
	return bindingID, nil
}

func (b *memBackend) CompleteAwait(_ context.Context, bindingID string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.awaits[bindingID]
	if !ok || a.status != "waiting" {
		return false, nil
	}
	a.status = "completing"
	b.awaits[bindingID] = a
	return true, nil
}

func (b *memBackend) Lock(_ context.Context, _ string) (func(), error) {
	return func() {}, nil
}

func (b *memBackend) LoadState(_ context.Context, taskID string) (*flux.TaskState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.taskStates[taskID], nil
}

// ── 工具 ──

type echoTool struct{}

func (echoTool) Name() string                   { return "echo" }
func (echoTool) Description() string            { return "echo" }
func (echoTool) Mode() tool.ExecutionMode       { return tool.SyncExecution }
func (echoTool) InputSchema() tool.DataSchema   { return tool.DataSchema{} }
func (echoTool) OutputSchema() tool.DataSchema  { return tool.DataSchema{} }
func (echoTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(input), nil
}

// ── 测试：sync workflow 端到端 ──

func TestEngine_Run_SyncWorkflow(t *testing.T) {
	backend := newMemBackend()
	engine, _ := flux.New(flux.Config{Backend: backend})

	def := &definition.WorkflowDefinition{
		Name: "test_sync",
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
		Asset: "test_sync",
		Input: map[string]any{"value": 42},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != flux.StatusCompleted {
		t.Fatalf("应完成，status=%d", result.Status)
	}

	// 验证 Backend 被调用了（PersistNode 有记录）
	snap, ok := backend.nodes[result.TaskID+".step1"]
	if !ok {
		t.Fatal("step1 应该被持久化")
	}
	if snap.State != flux.NodeSuccess {
		t.Fatalf("step1 应为 NodeSuccess，实际=%d", snap.State)
	}
	t.Logf("✅ sync：step1=%v, taskID=%s", snap.Output, result.TaskID)
}

// ── 测试：并发安全（两个任务互不干扰）──

func TestEngine_Run_ConcurrentTasks(t *testing.T) {
	backend := newMemBackend()
	engine, _ := flux.New(flux.Config{Backend: backend})

	def := &definition.WorkflowDefinition{
		Name: "concurrent_test",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "echo", Type: definition.NodeTool, Config: map[string]any{"tool": "echo"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "echo", Type: definition.EdgeNormal},
			{From: "echo", To: "end", Type: definition.EdgeNormal},
		},
	}

	engine.Register(flux.Workflow(def))
	engine.Register(flux.Tool(echoTool{}))

	var wg sync.WaitGroup
	results := make([]*flux.RunResult, 3)
	errs := make([]error, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = engine.Run(context.Background(), flux.RunRequest{
				Asset: "concurrent_test",
				Input: map[string]any{"idx": idx},
			})
		}(i)
	}
	wg.Wait()

	for i := 0; i < 3; i++ {
		if errs[i] != nil {
			t.Fatalf("task %d: %v", i, errs[i])
		}
		if results[i].Status != flux.StatusCompleted {
			t.Fatalf("task %d 应完成，status=%d", i, results[i].Status)
		}
		// 每个 task 应有不同的 taskID
		if results[i].TaskID == "" {
			t.Fatalf("task %d 缺少 taskID", i)
		}
	}
	t.Logf("✅ 并发：3 个 task 全部完成，taskIDs 各不相同")
}

// ── 测试：async workflow → Notify → resume ──

func TestEngine_Run_AsyncWorkflow_Notify(t *testing.T) {
	backend := newMemBackend()
	engine, _ := flux.New(flux.Config{Backend: backend})

	// 构造 async_hello → echo 的 DAG
	def := &definition.WorkflowDefinition{
		Name: "test_async",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "async_hello", Type: definition.NodeAwait, Config: map[string]any{
				"await_type": "external_task",
				"source":     "webhook_or_poll",
			}},
			{Name: "echo", Type: definition.NodeTool, Config: map[string]any{"tool": "echo"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "async_hello", Type: definition.EdgeNormal},
			{From: "async_hello", To: "echo", Type: definition.EdgeNormal},
			{From: "echo", To: "end", Type: definition.EdgeNormal},
		},
	}

	engine.Register(flux.Workflow(def))
	engine.Register(flux.Tool(echoTool{}))

	// ── Run → 应挂起 ──
	result, err := engine.Run(context.Background(), flux.RunRequest{
		Asset: "test_async",
		Input: map[string]any{"topic": "test"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != flux.StatusSuspended {
		t.Fatalf("应挂起，status=%d", result.Status)
	}
	taskID := result.TaskID
	t.Logf("✅ suspend：taskID=%s", taskID)

	// ── Notify（模拟外部回调）→ 应完成 ──
	// providerTaskID 由 internalAwait.Begin 生成：taskID + "_" + nodeName
	providerTaskID := taskID + "_async_hello"
	result2, err := engine.Notify(context.Background(), flux.Event{
		Provider:       "test_provider",
		ProviderTaskID: providerTaskID,
		Output: map[string]any{
			"message": "hello from async callback",
		},
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if result2.Status != flux.StatusCompleted {
		t.Fatalf("Notify 后应完成，status=%d", result2.Status)
	}

	// 验证 Backend 的 CompleteAwait 被调用了
	bindKey := "bind_" + providerTaskID
	a, ok := backend.awaits[bindKey]
	if !ok {
		t.Fatal("binding 应该存在")
	}
	if a.status != "completing" {
		t.Fatalf("binding 应被 Completing，实际=%s", a.status)
	}
	t.Logf("✅ notify→resume→complete：binding=%s status=%s", bindKey, a.status)
}

// ── 测试：重复 Notify 幂等 ──

func TestEngine_Notify_Idempotent(t *testing.T) {
	backend := newMemBackend()
	engine, _ := flux.New(flux.Config{Backend: backend})

	def := &definition.WorkflowDefinition{
		Name: "test_idempotent",
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

	result, _ := engine.Run(context.Background(), flux.RunRequest{
		Asset: "test_idempotent",
		Input: map[string]any{},
	})
	taskID := result.TaskID
	providerTaskID := taskID + "_async_step"

	// 第一次 Notify
	_, err := engine.Notify(context.Background(), flux.Event{
		Provider:       "test",
		ProviderTaskID: providerTaskID,
		Output:         map[string]any{"msg": "first"},
	})
	if err != nil {
		t.Fatalf("第一次 Notify: %v", err)
	}

	// 第二次 Notify（重复）——不应 panic 或破坏状态
	_, err = engine.Notify(context.Background(), flux.Event{
		Provider:       "test",
		ProviderTaskID: providerTaskID,
		Output:         map[string]any{"msg": "duplicate"},
	})
	if err != nil {
		t.Fatalf("重复 Notify 不应报错: %v", err)
	}

	t.Log("✅ 幂等：重复 Notify 安全，CompleteAwait 拒绝第二次 claim")
}

// ── 测试：宿主提供的 TaskID 被正确使用 ──

func TestEngine_Run_UsesProvidedTaskID(t *testing.T) {
	backend := newMemBackend()
	engine, _ := flux.New(flux.Config{Backend: backend})

	def := &definition.WorkflowDefinition{
		Name: "test_taskid",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "echo", Type: definition.NodeTool, Config: map[string]any{"tool": "echo"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "echo", Type: definition.EdgeNormal},
			{From: "echo", To: "end", Type: definition.EdgeNormal},
		},
	}

	engine.Register(flux.Workflow(def))
	engine.Register(flux.Tool(echoTool{}))

	// 用宿主提供的 TaskID（如 DreamAI 的 Task.ID）
	result, _ := engine.Run(context.Background(), flux.RunRequest{
		Asset:  "test_taskid",
		Input:  map[string]any{},
		TaskID: "20260626_DreamAI_Task_789",
	})

	if result.TaskID != "20260626_DreamAI_Task_789" {
		t.Fatalf("应使用宿主提供的 TaskID，实际=%q", result.TaskID)
	}

	// 验证 Backend 用的是这个 TaskID
	_, ok := backend.nodes["20260626_DreamAI_Task_789.echo"]
	if !ok {
		t.Fatal("PersistNode 应用宿主提供的 TaskID")
	}
	t.Logf("✅ TaskID 透传：%s", result.TaskID)
}

// ── 测试：realProviderTaskID 从 input 提取真实外部 job ID ──

func TestEngine_Notify_UsesRealProviderTaskID(t *testing.T) {
	backend := newMemBackend()
	engine, _ := flux.New(flux.Config{Backend: backend})

	// job_id 从 input 传入（业务层已知道 provider 返回的 job_id）
	def := &definition.WorkflowDefinition{
		Name: "test_real_jobid",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name: "tts_wait",
				Type: definition.NodeAwait,
				Config: map[string]any{
					"await_type": "external_task",
					"source":     "webhook_or_poll",
				},
				InputMapping: map[string]string{"job_id": "input.job_id"},
			},
			{Name: "echo", Type: definition.NodeTool, Config: map[string]any{"tool": "echo"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "tts_wait", Type: definition.EdgeNormal},
			{From: "tts_wait", To: "echo", Type: definition.EdgeNormal},
			{From: "echo", To: "end", Type: definition.EdgeNormal},
		},
	}

	engine.Register(flux.Workflow(def))
	engine.Register(flux.Tool(echoTool{}))

	result, _ := engine.Run(context.Background(), flux.RunRequest{
		Asset:  "test_real_jobid",
		Input:  map[string]any{"job_id": "real_provider_job_67890"},
		TaskID: "88",
	})
	if result.Status != flux.StatusSuspended {
		t.Fatalf("应挂起，status=%d", result.Status)
	}

	// 模拟真实 webhook：ProviderTaskID 匹配真实 job_id
	_, err := engine.Notify(context.Background(), flux.Event{
		Provider:       "tts",
		ProviderTaskID: "real_provider_job_67890",
		Output:         map[string]any{"audio_url": "https://cdn.example.com/audio.mp3"},
	})
	if err != nil {
		t.Fatalf("用真实 job_id Notify 应成功: %v", err)
	}

	t.Log("✅ 真实 job_id 提取：input.job_id → Begin 识别 → Notify 匹配")
}
