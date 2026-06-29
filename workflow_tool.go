package flux

import (
	"context"
	"fmt"

	"github.com/tuxi/flux/model"
	"github.com/tuxi/flux/planner"
	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/store"
	"github.com/tuxi/flux/tool"
)

// WorkflowTool 将 Flux Workflow Engine 封装为一个 Tool，供任何 Agent Runtime 调用。
//
// Agent Runtime 将此 Tool 注册到自己的 Tool Registry 后，LLM 即可通过 plan_workflow
// 调用 Flux 的 DAG 规划 + 执行能力。
//
// 使用方式：
//
//	wt := flux.NewWorkflowTool(flux.WorkflowToolConfig{
//	    Provider:   llmProvider,           // *model.OpenAICompatibleProvider
//	    ModelName:  "deepseek-chat",
//	    ToolReg:    myToolRegistry,        // flux 的 tool.Registry（Provider 工具）
//	    WFStore:    myWorkflowStore,        // 可选：v3 Store 接口
//	    AwaitStore: myAwaitStore,           // 可选：v3 Store 接口
//	})
type WorkflowTool struct {
	provider   *model.OpenAICompatibleProvider
	modelName  string
	toolReg    *tool.Registry
	wfStore    store.WorkflowStore
	awaitStore store.AwaitStore
	traceStore store.TraceStore
	maxRepairs int
}

// WorkflowToolConfig 是 WorkflowTool 的配置。
type WorkflowToolConfig struct {
	// Provider 是 DAGPlanner 使用的 LLM provider（OpenAI-compatible）。必填。
	Provider *model.OpenAICompatibleProvider

	// ModelName 是传给 LLM 的模型名。默认 "deepseek-chat"。
	ModelName string

	// ToolReg 是 Workflow 执行时可用的工具注册表。必填。
	// 这些工具是 Workflow DAG 中的节点——图片/视频/TTS Provider 等，不是 Agent 的对话工具。
	ToolReg *tool.Registry

	// ── v3 Store 接口（可选）──
	WFStore    store.WorkflowStore // nil → 内存模式（仅 sync workflow）
	AwaitStore store.AwaitStore    // nil → async 节点会失败
	TraceStore store.TraceStore    // nil → 不记录 trace

	// MaxRepairs 是 DAGPlanner 的 validate→repair 最大轮数。默认 3。
	MaxRepairs int
}

// NewWorkflowTool 创建一个 Workflow Tool。
func NewWorkflowTool(cfg WorkflowToolConfig) *WorkflowTool {
	if cfg.MaxRepairs <= 0 {
		cfg.MaxRepairs = 3
	}
	if cfg.ModelName == "" {
		cfg.ModelName = "deepseek-chat"
	}
	return &WorkflowTool{
		provider:   cfg.Provider,
		modelName:  cfg.ModelName,
		toolReg:    cfg.ToolReg,
		wfStore:    cfg.WFStore,
		awaitStore: cfg.AwaitStore,
		traceStore: cfg.TraceStore,
		maxRepairs: cfg.MaxRepairs,
	}
}

// ── tool.Tool 接口实现 ──

func (t *WorkflowTool) Name() string            { return "plan_workflow" }
func (t *WorkflowTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

// Registry 返回 WorkflowTool 内部的工具注册表，供 DAGPlanner 使用。
func (t *WorkflowTool) Registry() *tool.Registry { return t.toolReg }

func (t *WorkflowTool) Description() string {
	return "给定目标和可用工具目录，生成并执行一个多步 Workflow DAG。" +
		"Flux Engine 会将目标编译为一张有向无环图（DAG），进行依赖求解和并行执行，并返回每个节点的产出。" +
		"适用于需要多步、有依赖关系、可能并行的复杂任务（如图片/视频生成 pipeline）。"
}

func (t *WorkflowTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"goal": {Type: "string", Required: true, Desc: "要完成的目标描述"},
		},
	}
}

func (t *WorkflowTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

// Execute 执行 Workflow Tool。
//
// 流程：
//  1. DAGPlanner 根据 goal + tool catalog 生成 runtime.Plan
//  2. Scheduler 执行 Plan（依赖求解、并行调度）
//  3. 返回所有节点的产出
func (t *WorkflowTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	goal, _ := input["goal"].(string)
	if goal == "" {
		return tool.Fail(fmt.Errorf("missing required argument: goal")), nil
	}

	// 1. DAG 生成
	dagPlanner := planner.NewDAGPlanner(t.provider, t.modelName, goal, t.toolReg)
	dagPlanner.MaxRepairs = t.maxRepairs

	plan, err := dagPlanner.Generate(ctx)
	if err != nil {
		return tool.Fail(fmt.Errorf("DAG generation failed: %w", err)), nil
	}

	// 给 emitter 发一条规划完成事件
	if emitter != nil {
		emitter.EmitToolEvent(tool.ToolEvent{
			Type:    "log",
			Message: fmt.Sprintf("DAG generated: %d nodes", len(plan.Nodes)),
		})
	}

	// 2. 构建 Scheduler
	sched := runtime.NewScheduler(
		planner.NewToolInvoker(t.toolReg),
		&wfToolAwait{store: t.awaitStore},
		&wfToolStore{store: t.wfStore},
		&wfToolEmitter{emitter: emitter},
	).WithMaxSteps(200)

	if t.traceStore != nil {
		sched = sched.WithTrace(&wfToolTraceSink{store: t.traceStore}, "wf_"+goal)
	}

	// 3. 执行
	state := runtime.NewMemState(input)
	res, err := sched.Run(ctx, runtime.NewStaticSource(plan), state)
	if err != nil {
		return tool.Fail(fmt.Errorf("DAG execution failed: %w", err)), nil
	}
	if res.Status != runtime.StatusCompleted {
		return tool.Fail(fmt.Errorf("DAG incomplete: status=%d", res.Status)), nil
	}

	// 4. 收集所有节点产出
	output := make(map[string]any)
	for _, name := range state.Nodes() {
		if out := state.Output(name); out != nil {
			output[name] = out
		}
	}

	return tool.Success(output), nil
}

// ── 内部适配器 ──

type wfToolAwait struct {
	store store.AwaitStore
}

func (a *wfToolAwait) Begin(ctx context.Context, node *runtime.PlanNode, input map[string]any) (int64, error) {
	if a.store == nil {
		return 0, fmt.Errorf("async node %q: no AwaitStore configured", node.Name)
	}
	b := store.AwaitBinding{
		BindingID:      fmt.Sprintf("wf_%s", node.Name),
		TaskID:         "workflow_tool",
		NodeName:       node.Name,
		ProviderTaskID: fmt.Sprintf("%s_%v", node.Name, input),
		Status:         store.AwaitStatusAwaiting,
		Input:          input,
	}
	if err := a.store.CreateBinding(ctx, b); err != nil {
		return 0, err
	}
	return 1, nil
}

type wfToolStore struct {
	store store.WorkflowStore
}

func (s *wfToolStore) PersistNode(ctx context.Context, node string, state runtime.NodeState, out map[string]any) error {
	if s.store == nil {
		return nil
	}
	return s.store.PersistNode(ctx, "workflow_tool", node, state, out)
}

type wfToolEmitter struct {
	emitter tool.ToolEmitter
}

func (e *wfToolEmitter) Emit(ev runtime.Event) {
	if e.emitter == nil {
		return
	}
	e.emitter.EmitToolEvent(tool.ToolEvent{
		Type:    ev.Type,
		Message: ev.Message,
		Data:    ev.Data,
	})
}

type wfToolTraceSink struct {
	store store.TraceStore
}

func (s *wfToolTraceSink) EmitExecution(e runtime.TraceEvent) {
	if s.store != nil {
		_ = s.store.AppendTrace(context.Background(), "workflow_tool", []runtime.TraceEvent{e})
	}
}

func (s *wfToolTraceSink) EmitControl(e runtime.TraceEvent) {
	if s.store != nil {
		_ = s.store.AppendTrace(context.Background(), "workflow_tool", []runtime.TraceEvent{e})
	}
}

var _ tool.Tool = (*WorkflowTool)(nil)
