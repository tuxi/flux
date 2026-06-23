package planner

// M1 验证（内核半边）：kernel 能托管 code→compile 的 control loop。
//
// 用真实工具（write_file + 真实 `go build`）跑通：
//   写一份编译不过的代码 → 编译(失败) → 看到报错后写修复版 → 编译(通过) → done。
// planner 用脚本化的 IncrementalPlanSource 站在 LLM 的位置（同一个 PlanSource 接缝）；
// 它读 ExecState 的 compile 结果来决定下一步，证明"反馈驱动"。
// 下一步把它换成真实 Claude planner（实现同一个 Next 形状）。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

const badGo = "package main\n\nfunc main() {\n\tx := 1\n}\n"  // x 声明未使用 → 编译失败
const goodGo = "package main\n\nfunc main() {\n\t_ = 1\n}\n" // 通过

// scriptedSource 模拟 LLM：每轮返回下一个动作节点，done 由它判定。
// 它读 ExecState 决定"写修复版 vs 结束"——这就是反馈循环。
type scriptedSource struct{ step int }

func actionNode(name, toolName string, input map[string]any) *runtime.PlanNode {
	return &runtime.PlanNode{
		Name:     name,
		ToolName: toolName,
		Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
			return input, nil // planner 直接给具体值（不走 expr）
		},
	}
}

func (s *scriptedSource) Next(_ context.Context, st runtime.ExecState) ([]*runtime.PlanNode, bool, error) {
	switch s.step {
	case 0: // 写一份会编译失败的代码
		s.step++
		return []*runtime.PlanNode{actionNode("write_1", "write_file",
			map[string]any{"path": "main.go", "content": badGo})}, false, nil
	case 1: // 编译
		s.step++
		return []*runtime.PlanNode{actionNode("compile_1", "compile", nil)}, false, nil
	case 2: // 反馈：看上次是否通过，没过就写修复版
		s.step++
		if c, _ := st.Output("compile_1")["compiled"].(bool); c {
			return nil, true, nil
		}
		return []*runtime.PlanNode{actionNode("write_2", "write_file",
			map[string]any{"path": "main.go", "content": goodGo})}, false, nil
	case 3: // 再编译
		s.step++
		return []*runtime.PlanNode{actionNode("compile_2", "compile", nil)}, false, nil
	default: // 结束
		return nil, true, nil
	}
}

func TestM1_CodeCompileLoop(t *testing.T) {
	dir := t.TempDir()
	// 最小 Go module，让 `go build ./...` 能跑
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))

	sched := runtime.NewScheduler(NewToolInvoker(reg), NopAwait{}, NopStore{}, NopEmitter{}).
		WithMaxSteps(20) // FR6：硬上限，防 runaway
	st := runtime.NewMemState(nil)

	res, err := sched.Run(context.Background(), &scriptedSource{}, st)
	if err != nil {
		t.Fatalf("loop run error: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("expected Completed, got status=%d", res.Status)
	}

	// 反馈前提：第一次编译确实失败（否则证明不了反馈）
	if c, _ := st.Output("compile_1")["compiled"].(bool); c {
		t.Fatalf("compile_1 应当失败")
	}
	// control loop 收敛：修复后编译通过
	if c, _ := st.Output("compile_2")["compiled"].(bool); !c {
		t.Fatalf("compile_2 应当通过，output=%v", st.Output("compile_2")["output"])
	}
	// 真实文件确实落地了
	if got := st.Output("write_2")["path"]; got == nil {
		t.Fatalf("write_2 未产出路径")
	}
}
