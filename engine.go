package flux

import (
	"context"

	"flux/definition"
	"flux/runtime"
	"flux/skill"
	"flux/tool"
	"flux/workflow"
)

// Engine 是 Flux 执行内核的外部面。宿主创建一个 Engine，注册能力，然后只调 Run 和 Notify。
type Engine struct {
	backend  Backend
	toolReg  *tool.Registry
	wfReg    map[string]*definition.WorkflowDefinition
	skillReg *skill.Registry

	// 当前执行状态（Run 和 Notify 之间保持）
	plan      *runtime.Plan
	scheduler *runtime.Scheduler
	state     *runtime.MemState
	taskID    string
}

// New 创建一个 Engine。
func New(cfg Config) (*Engine, error) {
	return &Engine{
		backend:  cfg.Backend,
		toolReg:  tool.NewRegistry(),
		wfReg:    map[string]*definition.WorkflowDefinition{},
		skillReg: skill.NewRegistry(),
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

// Run 执行一个已注册的 Asset。
func (e *Engine) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	def, ok := e.wfReg[req.Asset]
	if !ok {
		return &RunResult{Status: StatusFailed, Err: nil, TaskID: req.Asset}, nil
	}

	plan, err := workflow.Compile(def, func(toolName string) bool {
		t, ok := e.toolReg.Get(toolName)
		if !ok {
			return false
		}
		return t.Mode() == tool.AsyncExecution
	})
	if err != nil {
		return &RunResult{Status: StatusFailed, Err: err}, err
	}
	e.plan = plan
	e.state = runtime.NewMemState(req.Input)
	e.taskID = req.Asset

	sched := runtime.NewScheduler(
		&internalInvoker{reg: e.toolReg},
		&internalAwait{backend: e.backend, taskID: e.taskID},
		&internalStore{backend: e.backend, taskID: e.taskID},
		nil,
	)
	e.scheduler = sched

	res, err := sched.Run(ctx, runtime.NewStaticSource(plan), e.state)
	return &RunResult{
		Status: RunStatus(res.Status),
		TaskID: e.taskID,
		Err:    err,
	}, err
}

// Notify 通知 Engine：外部异步任务已完成。
func (e *Engine) Notify(ctx context.Context, event Event) (*RunResult, error) {
	if e.scheduler == nil || e.state == nil || e.plan == nil {
		return nil, nil
	}
	// Phase 1: Event.ProviderTaskID 用作 node 名（简单映射）
	res, err := e.scheduler.Resume(ctx, runtime.NewStaticSource(e.plan), e.state,
		event.Output["node"].(string), event.Output)
	return &RunResult{
		Status: RunStatus(res.Status),
		TaskID: e.taskID,
		Err:    err,
	}, err
}
