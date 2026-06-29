package flux

import (
	"context"
	"fmt"
	"sync"

	"github.com/tuxi/flux/definition"
	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/skill"
	"github.com/tuxi/flux/store"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/workflow"
)

// Engine 是 Flux 执行内核的外部面。宿主创建一个 Engine，注册能力，然后只调 Run 和 Notify。
type Engine struct {
	backend  Backend
	toolReg  *tool.Registry
	wfReg    map[string]*definition.WorkflowDefinition
	skillReg *skill.Registry

	// v3 Store 接口（可选）。非 nil 时优先使用，nil 时回退到 Backend。
	workflowStore store.WorkflowStore
	awaitStore    store.AwaitStore
	traceStore    store.TraceStore

	mu    sync.Mutex
	tasks map[string]*taskRuntime // taskID → 运行时状态（支持并发任务）
}

// taskRuntime 是一个任务的完整执行状态（任务间隔离）。
type taskRuntime struct {
	plan      *runtime.Plan
	scheduler *runtime.Scheduler
	state     *runtime.MemState

	// binding 索引：providerTaskID → {bindingID, nodeName}
	awaitIndex map[string]bindingRef
}

type bindingRef struct {
	bindingID string
	nodeName  string
}

// New 创建一个 Engine。
// Backend 为必填（向后兼容）。当 Store 接口（v3）非 nil 时，Engine 优先使用 Store 接口进行持久化。
func New(cfg Config) (*Engine, error) {
	return &Engine{
		backend:       cfg.Backend,
		toolReg:       tool.NewRegistry(),
		wfReg:         map[string]*definition.WorkflowDefinition{},
		skillReg:      skill.NewRegistry(),
		tasks:         map[string]*taskRuntime{},
		workflowStore: cfg.WorkflowStore,
		awaitStore:    cfg.AwaitStore,
		traceStore:    cfg.TraceStore,
	}, nil
}

// Register 注册一个 Asset。同名重复注册会 panic。
func (e *Engine) Register(a Asset) error {
	switch v := a.(type) {
	case *workflowAsset:
		e.wfReg[v.name()] = v.def
	case *toolAsset:
		e.toolReg.Register(v.tool)
	case *skillAsset:
		e.skillReg.Register(v.name(), nil) // Phase 2: 展开为 ExecutableSkill
	}
	return nil
}

// Run 执行一个已注册的 Asset。并发安全。
func (e *Engine) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	def, ok := e.wfReg[req.Asset]
	if !ok {
		return &RunResult{Status: StatusFailed, TaskID: req.Asset}, nil
	}

	// 使用宿主提供的 taskID，没有则自动生成
	taskID := req.TaskID
	if taskID == "" {
		e.mu.Lock()
		taskID = fmt.Sprintf("%s_%d", req.Asset, len(e.tasks)+1)
		e.mu.Unlock()
	}

	plan, err := workflow.Compile(def, func(toolName string) bool {
		t, ok := e.toolReg.Get(toolName)
		if !ok {
			return false
		}
		return t.Mode() == tool.AsyncExecution
	})
	if err != nil {
		return &RunResult{Status: StatusFailed, Err: err, TaskID: taskID}, err
	}

	state := runtime.NewMemState(req.Input)
	tr := &taskRuntime{
		plan:       plan,
		state:      state,
		awaitIndex: map[string]bindingRef{},
	}

	sched := runtime.NewScheduler(
		&internalInvoker{reg: e.toolReg},
		&internalAwait{backend: e.backend, taskID: taskID, tr: tr, awaitStore: e.awaitStore},
		&internalStore{backend: e.backend, taskID: taskID, workflowStore: e.workflowStore},
		nil,
	)
	// v3: 如果配置了 TraceStore，启用 trace
	if e.traceStore != nil {
		sched = sched.WithTrace(&storeTraceSink{store: e.traceStore, taskID: taskID}, taskID)
	}
	tr.scheduler = sched

	e.mu.Lock()
	e.tasks[taskID] = tr
	e.mu.Unlock()

	res, err := sched.Run(ctx, runtime.NewStaticSource(plan), state)
	return &RunResult{
		Status: RunStatus(res.Status),
		TaskID: taskID,
		Err:    err,
	}, err
}

// Notify 通知 Engine：外部异步任务已完成。
// 内部流程：Provider+ProviderTaskID → 查找 binding → 原子 ClaimCompleting → Resume。
func (e *Engine) Notify(ctx context.Context, event Event) (*RunResult, error) {
	e.mu.Lock()
	// 遍历所有 task 查找匹配的 binding
	var foundTR *taskRuntime
	var foundRef bindingRef
	for _, tr := range e.tasks {
		if ref, ok := tr.awaitIndex[event.ProviderTaskID]; ok {
			foundTR = tr
			foundRef = ref
			break
		}
	}
	e.mu.Unlock()

	if foundTR == nil {
		return nil, fmt.Errorf("flux: 未找到 providerTaskID=%s 对应的 binding", event.ProviderTaskID)
	}

	// 原子幂等保护
	if event.Error == "" {
		var claimed bool
		var err error
		if e.awaitStore != nil {
			// v3 path: 使用 AwaitStore
			claimed, err = e.awaitStore.ResolveBinding(ctx, foundRef.bindingID)
		} else {
			// Backend path (向后兼容)
			claimed, err = e.backend.CompleteAwait(ctx, foundRef.bindingID)
		}
		if err != nil {
			return nil, fmt.Errorf("flux: CompleteAwait: %w", err)
		}
		if !claimed {
			// 已被其他线程完成——幂等返回
			return &RunResult{Status: StatusCompleted, TaskID: ""}, nil
		}
	}

	res, err := foundTR.scheduler.Resume(ctx, runtime.NewStaticSource(foundTR.plan), foundTR.state,
		foundRef.nodeName, event.Output)
	return &RunResult{
		Status: RunStatus(res.Status),
		Err:    err,
	}, err
}
