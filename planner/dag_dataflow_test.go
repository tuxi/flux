package planner

// 类型 B 数据流接线的 hermetic 证明（无 LLM）：
//   1. 运行时：下游节点用 {"$from":上游,"field":x} 拿到上游真实产出；
//   2. FR5：引用未声明为 depends_on 边 → 被校验拦下。
//
// 用 merge_result（回显 input）当"产出值"的上游，纯内存、可断言。

import (
	"context"
	"strings"
	"testing"

	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

func mergeReg() *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())
	return reg
}

func TestDataflow_UpstreamOutputFlowsDownstream(t *testing.T) {
	reg := mergeReg()

	// A 产出 {name:"alice"}；B 的参数 greeting_for 引用 A.name。
	spec := planSpec{Nodes: []nodeSpec{
		{ID: "A", Tool: "merge_result", Arguments: map[string]any{"name": "alice"}},
		{ID: "B", Tool: "merge_result",
			DependsOn: []string{"A"},
			Arguments: map[string]any{
				"greeting_for": map[string]any{"$from": "A", "field": "name"},
				"whole_a":      map[string]any{"$from": "A"}, // 整 output
			}},
	}}
	if errs := validatePlan(spec, reg); len(errs) != 0 {
		t.Fatalf("合法引用计划不应有错误: %v", errs)
	}

	plan := buildPlan(spec)
	sched := runtime.NewScheduler(NewToolInvoker(reg), NopAwait{}, NopStore{}, NopEmitter{}).
		WithMaxSteps(10)
	st := runtime.NewMemState(nil)
	res, err := sched.Run(context.Background(), runtime.NewStaticSource(plan), st)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("status=%d", res.Status)
	}

	// B 回显 input → 它的 output 应含被解析后的 greeting_for="alice"
	bOut := st.Output("B")
	if bOut["greeting_for"] != "alice" {
		t.Fatalf("引用 A.name 未解析到下游：greeting_for=%v（want alice）", bOut["greeting_for"])
	}
	// 整 output 引用：whole_a 应是 A 的 output map（含 name:alice）
	whole, ok := bOut["whole_a"].(map[string]any)
	if !ok || whole["name"] != "alice" {
		t.Fatalf("整 output 引用未解析：whole_a=%v", bOut["whole_a"])
	}
	t.Logf("✅ 数据流：B 在运行时拿到了 A 的产出（greeting_for=%v）", bOut["greeting_for"])
}

func TestDataflow_FR5_ReferenceMustBeDependsOnEdge(t *testing.T) {
	reg := mergeReg()

	// B 引用 A，但**没把 A 放进 depends_on** → 校验必须拦下。
	spec := planSpec{Nodes: []nodeSpec{
		{ID: "A", Tool: "merge_result", Arguments: map[string]any{"name": "bob"}},
		{ID: "B", Tool: "merge_result", // 缺 DependsOn
			Arguments: map[string]any{"x": map[string]any{"$from": "A", "field": "name"}}},
	}}
	joined := strings.Join(validatePlan(spec, reg), " | ")
	if !strings.Contains(joined, "not in depends_on") {
		t.Fatalf("应拦下'引用但未声明依赖'，得 %q", joined)
	}

	// 引用一个根本不存在的节点
	spec2 := planSpec{Nodes: []nodeSpec{
		{ID: "B", Tool: "merge_result", DependsOn: []string{"ghost"},
			Arguments: map[string]any{"x": map[string]any{"$from": "ghost"}}},
	}}
	joined2 := strings.Join(validatePlan(spec2, reg), " | ")
	if !strings.Contains(joined2, "unknown node") {
		t.Fatalf("应拦下'引用未知节点'，得 %q", joined2)
	}
	t.Logf("✅ FR5：未声明为边的引用 + 引用未知节点 都被拦下")
}
