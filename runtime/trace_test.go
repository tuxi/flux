package runtime_test

// 验证 sidecar trace（Phase 1）三件事：
//  1. 双写捕获完整：execution 流 + control 流（plan_extend）都被记录，payload 回放完整。
//  2. 单序列：execution 与 control 共享同一条 Seq 全序（跨流因果可回放）。
//  3. 决策无副作用：开/关 trace，执行结果完全一致（trace 不驱动任何决策）。
//
// 复用 smoke_test.go 里的 toolInvoker / memState / fakeAwait / noopStore / noopEmitter / newReg。

import (
	"context"
	"sort"
	"testing"

	"github.com/tuxi/flux/runtime"
)

type recSink struct {
	exec []runtime.TraceEvent
	ctrl []runtime.TraceEvent
}

func (r *recSink) EmitExecution(e runtime.TraceEvent) { r.exec = append(r.exec, e) }
func (r *recSink) EmitControl(e runtime.TraceEvent)    { r.ctrl = append(r.ctrl, e) }

func twoNodeChain() *runtime.Plan {
	return &runtime.Plan{Nodes: map[string]*runtime.PlanNode{
		"a": {
			Name: "a", ToolName: "merge_result",
			Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
				return map[string]any{"x": 1}, nil
			},
		},
		"b": {
			Name: "b", ToolName: "merge_result", DependsOn: []string{"a"},
			Resolve: func(_ context.Context, st runtime.ExecState) (map[string]any, error) {
				return map[string]any{"y": st.Output("a")["x"]}, nil
			},
		},
	}}
}

func TestTrace_CaptureSingleSeqAndInert(t *testing.T) {
	// ── 带 trace 跑 ──
	sink := &recSink{}
	sched := runtime.NewScheduler(toolInvoker{newReg()}, &fakeAwait{}, noopStore{}, noopEmitter{}).
		WithTrace(sink, "run-1")
	st := newMemState(nil)

	res, err := sched.Run(context.Background(), runtime.NewStaticSource(twoNodeChain()), st)
	if err != nil || res.Status != runtime.StatusCompleted {
		t.Fatalf("run failed: status=%d err=%v", res.Status, err)
	}

	// 1) control 流：一次 plan_extend，含两个节点
	if len(sink.ctrl) != 1 || sink.ctrl[0].Type != runtime.TracePlanExtend {
		t.Fatalf("expected 1 plan_extend control event, got %+v", sink.ctrl)
	}

	// execution 流：每个节点都应有 node_start / input / output
	want := map[string]map[runtime.TraceType]bool{
		"a": {runtime.TraceNodeStart: false, runtime.TraceInput: false, runtime.TraceOutput: false},
		"b": {runtime.TraceNodeStart: false, runtime.TraceInput: false, runtime.TraceOutput: false},
	}
	var sawOutputB bool
	for _, e := range sink.exec {
		if m, ok := want[e.Node]; ok {
			if _, tracked := m[e.Type]; tracked {
				m[e.Type] = true
			}
		}
		if e.Node == "b" && e.Type == runtime.TraceOutput {
			// payload 回放完整：output 带实际产出
			if out, _ := e.Payload["output"].(map[string]any); out["y"] == 1 {
				sawOutputB = true
			}
		}
	}
	for node, m := range want {
		for typ, seen := range m {
			if !seen {
				t.Fatalf("missing execution event %s/%s", node, typ)
			}
		}
	}
	if !sawOutputB {
		t.Fatalf("output payload not replay-complete for b")
	}

	// 2) 单序列：合并 exec+ctrl，Seq 必须唯一且连续 1..N（证明共享一条全序）
	var seqs []int64
	for _, e := range sink.exec {
		seqs = append(seqs, e.Seq)
	}
	for _, e := range sink.ctrl {
		seqs = append(seqs, e.Seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, s := range seqs {
		if s != int64(i+1) {
			t.Fatalf("Seq not a single contiguous total order: got %v", seqs)
		}
	}
	// 且 plan_extend（control）应排在所有 execution 之前（先有计划，才有执行）
	if sink.ctrl[0].Seq != 1 {
		t.Fatalf("plan_extend should be Seq=1 (plan precedes execution), got %d", sink.ctrl[0].Seq)
	}

	// 3) 决策无副作用：关掉 trace 重跑，结果必须一致
	sched2 := runtime.NewScheduler(toolInvoker{newReg()}, &fakeAwait{}, noopStore{}, noopEmitter{})
	st2 := newMemState(nil)
	res2, err2 := sched2.Run(context.Background(), runtime.NewStaticSource(twoNodeChain()), st2)
	if err2 != nil || res2.Status != runtime.StatusCompleted {
		t.Fatalf("no-trace run failed: status=%d err=%v", res2.Status, err2)
	}
	if st.Output("b")["y"] != st2.Output("b")["y"] {
		t.Fatalf("trace changed execution result: with=%v without=%v",
			st.Output("b")["y"], st2.Output("b")["y"])
	}
}
