package runtime_test

// 冒烟测试：验证 runtime 底座可行性。
// 关键点：本测试是 external test package（runtime_test），可以 import flux/tool，
// 但 runtime 生产包本身始终只依赖 stdlib —— 依赖反转在编译期就被钉死。

import (
	"context"
	"fmt"
	"testing"

	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

// ── 端口适配器（最小实现）─────────────────────────────────────────────

// toolInvoker 把 tool.Registry 适配成 runtime.Invoker（Tool-First 的落点）。
type toolInvoker struct{ reg *tool.Registry }

func (ti toolInvoker) Invoke(ctx context.Context, name string, input map[string]any, emit runtime.Emitter) (map[string]any, error) {
	t, ok := ti.reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	res, err := t.Execute(ctx, input, toolEmitterBridge{emit: emit, node: name})
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

// toolEmitterBridge 把 runtime.Emitter 适配成 tool.ToolEmitter。
type toolEmitterBridge struct {
	emit runtime.Emitter
	node string
}

func (b toolEmitterBridge) EmitToolEvent(e tool.ToolEvent) {
	if b.emit == nil {
		return
	}
	b.emit.Emit(runtime.Event{
		Node:     b.node,
		Type:     e.Type,
		Message:  e.Message,
		Progress: e.Progress,
		Data:     e.Data,
	})
}

// memState 最小 in-memory ExecState（生产环境由 nodes.Context 实现）。
type memState struct {
	input  map[string]any
	out    map[string]map[string]any
	states map[string]runtime.NodeState
}

func newMemState(input map[string]any) *memState {
	return &memState{
		input:  input,
		out:    map[string]map[string]any{},
		states: map[string]runtime.NodeState{},
	}
}

func (m *memState) Input() map[string]any              { return m.input }
func (m *memState) Output(node string) map[string]any  { return m.out[node] }
func (m *memState) SetOutput(node string, o map[string]any) { m.out[node] = o }
func (m *memState) State(node string) runtime.NodeState { return m.states[node] } // 默认 0 = NodePending
func (m *memState) Transition(node string, to runtime.NodeState) { m.states[node] = to }
func (m *memState) Nodes() []string {
	names := make([]string, 0, len(m.states))
	for n := range m.states {
		names = append(names, n)
	}
	return names
}

// 其余端口的 no-op / fake 实现。
type noopStore struct{}

func (noopStore) PersistNode(context.Context, string, runtime.NodeState, map[string]any) error {
	return nil
}

type noopEmitter struct{}

func (noopEmitter) Emit(runtime.Event) {}

// fakeAwait 登记一个等待（不提交真实外部任务），返回假 bindingID。
type fakeAwait struct{ began []string }

func (f *fakeAwait) Begin(_ context.Context, node *runtime.PlanNode, _ map[string]any) (int64, error) {
	f.began = append(f.began, node.Name)
	return 1, nil
}

func newReg() *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool()) // 真实的同步工具：原路返回 input
	return reg
}

// ── 测试 1：纯同步两节点 DAG（a → b）跑通 ─────────────────────────────

func TestSmoke_SyncChain(t *testing.T) {
	reg := newReg()

	plan := &runtime.Plan{Nodes: map[string]*runtime.PlanNode{
		"a": {
			Name:     "a",
			ToolName: "merge_result",
			Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
				return map[string]any{"x": 1}, nil
			},
		},
		"b": {
			Name:      "b",
			ToolName:  "merge_result",
			DependsOn: []string{"a"},
			Resolve: func(_ context.Context, st runtime.ExecState) (map[string]any, error) {
				// 读上游产出，证明数据流串通
				up := st.Output("a")
				return map[string]any{"y": up["x"]}, nil
			},
		},
	}}

	sched := runtime.NewScheduler(toolInvoker{reg}, &fakeAwait{}, noopStore{}, noopEmitter{})
	st := newMemState(map[string]any{"topic": "t"})

	res, err := sched.Run(context.Background(), runtime.NewStaticSource(plan), st)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("expected Completed, got status=%d", res.Status)
	}
	if st.State("a") != runtime.NodeSuccess || st.State("b") != runtime.NodeSuccess {
		t.Fatalf("nodes not success: a=%d b=%d", st.State("a"), st.State("b"))
	}
	if got := st.Output("b")["y"]; got != 1 {
		t.Fatalf("dataflow broken: b.y = %v (want 1)", got)
	}
}

// ── 测试 2：async 节点挂起 → Resume → 完成（异步半条路）─────────────────

func TestSmoke_AsyncSuspendResume(t *testing.T) {
	reg := newReg()
	await := &fakeAwait{}

	plan := &runtime.Plan{Nodes: map[string]*runtime.PlanNode{
		"gen": {
			Name:     "gen",
			ToolName: "merge_result",
			Async:    true, // ← 走挂起路径，对应 executor.go:133
			Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
				return map[string]any{"prompt": "cat"}, nil
			},
		},
		"post": {
			Name:      "post",
			ToolName:  "merge_result",
			DependsOn: []string{"gen"},
			Resolve: func(_ context.Context, st runtime.ExecState) (map[string]any, error) {
				return map[string]any{"final": st.Output("gen")["url"]}, nil
			},
		},
	}}

	sched := runtime.NewScheduler(toolInvoker{reg}, await, noopStore{}, noopEmitter{})
	st := newMemState(nil)
	src := runtime.NewStaticSource(plan)

	// 第一程：gen 挂起，整体 Suspended
	res, err := sched.Run(context.Background(), src, st)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != runtime.StatusSuspended {
		t.Fatalf("expected Suspended, got status=%d", res.Status)
	}
	if st.State("gen") != runtime.NodeAwaiting {
		t.Fatalf("gen should be Awaiting, got %d", st.State("gen"))
	}
	if len(await.began) != 1 || await.began[0] != "gen" {
		t.Fatalf("await.Begin not called for gen: %v", await.began)
	}
	if st.State("post") == runtime.NodeSuccess {
		t.Fatalf("post must not run before gen completes")
	}

	// 外部事件到达：Resume 唤醒 gen
	res, err = sched.Resume(context.Background(), src, st, "gen", map[string]any{"url": "http://img"})
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("expected Completed after resume, got status=%d", res.Status)
	}
	if got := st.Output("post")["final"]; got != "http://img" {
		t.Fatalf("post dataflow broken after resume: final = %v", got)
	}
}
