package workflow_test

// 验证：一条"人写的 WorkflowDefinition" 经 workflow.Compile 编译后，能在 runtime 底座上原样跑通。
// 并且证明真实的 nodes.Context 可以充当 runtime.ExecState（替掉手搓的 memState）。
//
// 外部测试包（workflow_test）：可同时 import workflow / nodes / runtime / tool，不影响生产包依赖方向。

import (
	"context"
	"fmt"
	"testing"

	"flux/definition"
	"flux/domain"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
	"flux/workflow"
	"flux/workflow/nodes"
)

// ── 端口适配器（最小实现）─────────────────────────────────────────────

type toolInvoker struct{ reg *tool.Registry }

func (ti toolInvoker) Invoke(ctx context.Context, name string, input map[string]any, emit runtime.Emitter) (map[string]any, error) {
	t, ok := ti.reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	res, err := t.Execute(ctx, input, noopToolEmitter{})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return map[string]any{}, nil
	}
	if !res.Success {
		return nil, fmt.Errorf("tool %s failed: %s", name, res.Error)
	}
	return res.Data, nil
}

type noopToolEmitter struct{}

func (noopToolEmitter) EmitToolEvent(tool.ToolEvent) {}

type noopStore struct{}

func (noopStore) PersistNode(context.Context, string, runtime.NodeState, map[string]any) error {
	return nil
}

type noopEmitter struct{}

func (noopEmitter) Emit(runtime.Event) {}

type fakeAwait struct{}

func (fakeAwait) Begin(context.Context, *runtime.PlanNode, map[string]any) (int64, error) {
	return 1, nil
}

func newReg() *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())
	return reg
}

// 一条真实的人写 DAG：start → gen → post → end，带 input_mapping 表达式。
func sampleDef() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		Name: "smoke",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "gen", Type: definition.NodeTool,
				Config:       map[string]any{"tool": "merge_result"},
				InputMapping: map[string]string{"topic": "input.topic"}},
			{Name: "post", Type: definition.NodeTool,
				Config:       map[string]any{"tool": "merge_result"},
				InputMapping: map[string]string{"echo": "gen.topic"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "gen"},
			{From: "gen", To: "post"},
			{From: "post", To: "end"},
		},
	}
}

func syncOnly(string) bool { return false }

// ── 测试 A：编译 + 在底座上跑（memState）──────────────────────────────

func TestCompile_RunOnSubstrate(t *testing.T) {
	plan, err := workflow.Compile(sampleDef(), syncOnly)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// start/end 被吸收，gen 成为根
	if len(plan.Nodes) != 2 {
		t.Fatalf("expected 2 plan nodes (gen,post), got %d", len(plan.Nodes))
	}
	if d := plan.Nodes["gen"].DependsOn; len(d) != 0 {
		t.Fatalf("gen should be a root after start absorbed, deps=%v", d)
	}

	sched := runtime.NewScheduler(toolInvoker{newReg()}, fakeAwait{}, noopStore{}, noopEmitter{})
	st := newMemState(map[string]any{"topic": "hello"})

	res, err := sched.Run(context.Background(), runtime.NewStaticSource(plan), st)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("expected Completed, got %d", res.Status)
	}
	if got := st.Output("post")["echo"]; got != "hello" {
		t.Fatalf("expr input_mapping dataflow broken: post.echo=%v (want hello)", got)
	}
}

// ── 测试 B：同一条编译计划，用真实 nodes.Context 当 ExecState 跑 ──────────

func TestCompile_RunWithRealContext(t *testing.T) {
	plan, err := workflow.Compile(sampleDef(), syncOnly)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// 真实 nodes.Context：只填 ExecState 所需的导出字段（Input + Runtime）。
	ctx := &nodes.Context{
		Input: map[string]any{"topic": "hello"},
		Runtime: map[string]*domain.NodeRuntime{
			"gen":  {Name: "gen", State: domain.NodePending},
			"post": {Name: "post", State: domain.NodePending},
		},
	}

	sched := runtime.NewScheduler(toolInvoker{newReg()}, fakeAwait{}, noopStore{}, noopEmitter{})
	res, err := sched.Run(context.Background(), runtime.NewStaticSource(plan), ctxState{ctx})
	if err != nil {
		t.Fatalf("Run(real Context): %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("expected Completed, got %d", res.Status)
	}
	if got := ctx.GetNodeOutput("post")["echo"]; got != "hello" {
		t.Fatalf("real-Context dataflow broken: post.echo=%v (want hello)", got)
	}
	if ctx.Runtime["post"].State != domain.NodeSuccess {
		t.Fatalf("post state not success: %s", ctx.Runtime["post"].State)
	}
}

// ── memState：最小 in-memory ExecState ────────────────────────────────

type memState struct {
	input  map[string]any
	out    map[string]map[string]any
	states map[string]runtime.NodeState
}

func newMemState(input map[string]any) *memState {
	return &memState{input: input, out: map[string]map[string]any{}, states: map[string]runtime.NodeState{}}
}

func (m *memState) Input() map[string]any                    { return m.input }
func (m *memState) Output(n string) map[string]any           { return m.out[n] }
func (m *memState) SetOutput(n string, o map[string]any)     { m.out[n] = o }
func (m *memState) State(n string) runtime.NodeState         { return m.states[n] }
func (m *memState) Transition(n string, to runtime.NodeState) { m.states[n] = to }
func (m *memState) Nodes() []string {
	out := make([]string, 0, len(m.states))
	for n := range m.states {
		out = append(out, n)
	}
	return out
}

// ── ctxState：把真实 nodes.Context 适配成 runtime.ExecState（只用导出 API）──

type ctxState struct{ c *nodes.Context }

var _ runtime.ExecState = ctxState{}

func (s ctxState) Input() map[string]any          { return s.c.Input }
func (s ctxState) Output(n string) map[string]any { return s.c.GetNodeOutput(n) }
func (s ctxState) SetOutput(n string, o map[string]any) {
	_ = s.c.SetNodeOutput(n, o, tool.DataSchema{}) // 空 schema → 仅写入，不校验
}
func (s ctxState) State(n string) runtime.NodeState {
	rt := s.c.Runtime[n]
	if rt == nil {
		return runtime.NodePending
	}
	return fromDomainState(rt.State)
}
func (s ctxState) Transition(n string, to runtime.NodeState) {
	rt := s.c.Runtime[n]
	if rt == nil {
		rt = &domain.NodeRuntime{Name: n}
		s.c.Runtime[n] = rt
	}
	rt.State = toDomainState(to)
}
func (s ctxState) Nodes() []string {
	out := make([]string, 0, len(s.c.Runtime))
	for n := range s.c.Runtime {
		out = append(out, n)
	}
	return out
}

func fromDomainState(d domain.NodeState) runtime.NodeState {
	switch d {
	case domain.NodeRunning:
		return runtime.NodeRunning
	case domain.NodeAwaiting:
		return runtime.NodeAwaiting
	case domain.NodeSuccess:
		return runtime.NodeSuccess
	case domain.NodeFailed:
		return runtime.NodeFailed
	case domain.NodeSkipped:
		return runtime.NodeSkipped
	default:
		return runtime.NodePending
	}
}

func toDomainState(r runtime.NodeState) domain.NodeState {
	switch r {
	case runtime.NodeRunning:
		return domain.NodeRunning
	case runtime.NodeAwaiting:
		return domain.NodeAwaiting
	case runtime.NodeSuccess:
		return domain.NodeSuccess
	case runtime.NodeFailed:
		return domain.NodeFailed
	case runtime.NodeSkipped:
		return domain.NodeSkipped
	default:
		return domain.NodePending
	}
}
