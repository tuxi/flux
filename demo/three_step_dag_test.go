// 三步 DAG Pipeline 演示：A (echo hello) ∥ B (echo world) → C (merge)
//
// 这是 "SDK 直接构造 Plan" 模式 —— 不需要 LLM，直接拼 PlanNode，
// 交给 Scheduler 做依赖求解和并行调度。
//
// 运行：
//
//	go test ./demo/ -run TestThreeStepDAG -v
package demo_test

import (
	"context"
	"strings"
	"testing"

	"flux/planner"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

// TestThreeStepDAG 演示三步 DAG pipeline：
//
//	步骤A: echo "hello"  ──┐
//	                       ├──→ 步骤C: merge 结果
//	步骤B: echo "world"  ──┘
//
// A 和 B 并行执行（无相互依赖），C 等两者都完成后合并输出。
func TestThreeStepDAG(t *testing.T) {
	// 1. 工具注册：shell（执行命令）+ merge_result（合并结果）
	reg := tool.NewRegistry()
	reg.Register(builtin.NewShellTool(t.TempDir()))
	reg.Register(builtin.NewMergeResultTool())

	// 2. 手工构造三步 Plan
	//    节点 A: echo hello
	//    节点 B: echo world
	//    节点 C: merge_result，依赖 A+B，引用两者的 stdout
	plan := &runtime.Plan{
		Nodes: map[string]*runtime.PlanNode{
			// ── 步骤 A：echo hello ──
			"echo_hello": {
				Name:     "echo_hello",
				ToolName: "shell",
				Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
					return map[string]any{"command": "echo hello"}, nil
				},
			},

			// ── 步骤 B：echo world（与 A 并行）──
			"echo_world": {
				Name:     "echo_world",
				ToolName: "shell",
				Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
					return map[string]any{"command": "echo world"}, nil
				},
			},

			// ── 步骤 C：合并 A 和 B 的结果 ──
			"merge": {
				Name:      "merge",
				ToolName:  "merge_result",
				DependsOn: []string{"echo_hello", "echo_world"},
				Resolve: func(_ context.Context, state runtime.ExecState) (map[string]any, error) {
					// 从上游节点取 stdout
					helloOut := state.Output("echo_hello")
					worldOut := state.Output("echo_world")

					return map[string]any{
						"hello":   strings.TrimSpace(helloOut["stdout"].(string)),
						"world":   strings.TrimSpace(worldOut["stdout"].(string)),
						"merged":  strings.TrimSpace(helloOut["stdout"].(string)) + " " + strings.TrimSpace(worldOut["stdout"].(string)),
					}, nil
				},
			},
		},
	}

	// 3. Scheduler 执行
	sched := runtime.NewScheduler(
		planner.NewToolInvoker(reg),
		planner.NopAwait{},
		planner.NopStore{},
		planner.NopEmitter{},
	).WithMaxSteps(30)

	state := runtime.NewMemState(nil)
	res, err := sched.Run(context.Background(), runtime.NewStaticSource(plan), state)
	if err != nil {
		t.Fatalf("DAG 执行失败: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("DAG 未完成: status=%d", res.Status)
	}

	// 4. 验证结果
	t.Log("═══ DAG 执行结果 ═══")
	for _, name := range state.Nodes() {
		out := state.Output(name)
		t.Logf("节点 %q → %v", name, out)
	}

	// 验证 merge 节点拿到了上游的 stdout
	mergeOut := state.Output("merge")
	if mergeOut["hello"] != "hello" {
		t.Fatalf("期望 merge.hello='hello'，得 %q", mergeOut["hello"])
	}
	if mergeOut["world"] != "world" {
		t.Fatalf("期望 merge.world='world'，得 %q", mergeOut["world"])
	}
	if mergeOut["merged"] != "hello world" {
		t.Fatalf("期望 merge.merged='hello world'，得 %q", mergeOut["merged"])
	}

	t.Log("✅ 三步 DAG pipeline 执行成功：A(echo hello) ∥ B(echo world) → C(merge) → 'hello world'")
}
