package planner

// 类型 B 的 hermetic 证明（无 LLM/无网络）：
//   1. FR5 校验逻辑（工具存在/依赖/无环/必填参数）；
//   2. buildPlan 产出的扇出 DAG 被 kernel 正确调度（3 个独立 write 并行就绪，
//      compile 依赖全部三者 → join），真实 go build 多文件包编译通过。
//
// 这覆盖类型 B 里"LLM 之外"的全部机制；LLM 生成那一环由 gated live 测试覆盖。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

func dagTestReg(dir string) *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))
	return reg
}

func TestValidatePlan_FR5(t *testing.T) {
	reg := dagTestReg(t.TempDir())

	valid := planSpec{Nodes: []nodeSpec{
		{ID: "wa", Tool: "write_file", Arguments: map[string]any{"path": "a.go", "content": "x"}},
		{ID: "wb", Tool: "write_file", Arguments: map[string]any{"path": "b.go", "content": "y"}},
		{ID: "c", Tool: "compile", DependsOn: []string{"wa", "wb"}},
	}}
	if errs := validatePlan(valid, reg); len(errs) != 0 {
		t.Fatalf("合法计划不应有错误，得 %v", errs)
	}

	cases := []struct {
		name string
		spec planSpec
		want string
	}{
		{"工具不存在", planSpec{Nodes: []nodeSpec{{ID: "x", Tool: "ghost_tool"}}}, "does not exist"},
		{"依赖未知节点", planSpec{Nodes: []nodeSpec{{ID: "x", Tool: "compile", DependsOn: []string{"nope"}}}}, "unknown node"},
		{"依赖成环", planSpec{Nodes: []nodeSpec{
			{ID: "a", Tool: "compile", DependsOn: []string{"b"}},
			{ID: "b", Tool: "compile", DependsOn: []string{"a"}},
		}}, "cycle"},
		{"缺必填参数", planSpec{Nodes: []nodeSpec{
			{ID: "w", Tool: "write_file", Arguments: map[string]any{"path": "a.go"}}, // 缺 content
		}}, "missing required argument"},
		{"空计划", planSpec{}, "no nodes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := strings.Join(validatePlan(tc.spec, reg), " | ")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("期望错误含 %q，得 %q", tc.want, joined)
			}
		})
	}
}

func TestDAGPlan_FanOutThroughKernel_FR4(t *testing.T) {
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := dagTestReg(dir)

	// 扇出：三个独立 write（并行就绪）→ compile 依赖全部三者（join）。
	// 三个文件互相引用（helperA/helperB 被 main 调用），故 compile 必须等三者都写完才能过——
	// 若 join 失效、compile 早跑，多文件包必然编译失败。这就把依赖调度钉死。
	spec := planSpec{Nodes: []nodeSpec{
		{ID: "wa", Tool: "write_file", Arguments: map[string]any{"path": "a.go", "content": "package main\n\nfunc helperA() int { return 1 }\n"}},
		{ID: "wb", Tool: "write_file", Arguments: map[string]any{"path": "b.go", "content": "package main\n\nfunc helperB() int { return 2 }\n"}},
		{ID: "wmain", Tool: "write_file", Arguments: map[string]any{"path": "main.go", "content": "package main\n\nfunc main() { _ = helperA() + helperB() }\n"}},
		{ID: "compile", Tool: "compile", DependsOn: []string{"wa", "wb", "wmain"}},
	}}
	if errs := validatePlan(spec, reg); len(errs) != 0 {
		t.Fatalf("spec 应合法: %v", errs)
	}

	plan := buildPlan(spec)
	sched := runtime.NewScheduler(NewToolInvoker(reg), NopAwait{}, NopStore{}, NopEmitter{}).
		WithMaxSteps(20)
	st := runtime.NewMemState(nil)

	res, err := sched.Run(context.Background(), runtime.NewStaticSource(plan), st)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("status=%d", res.Status)
	}
	for _, id := range []string{"wa", "wb", "wmain"} {
		if st.State(id) != runtime.NodeSuccess {
			t.Fatalf("并行节点 %s 未成功", id)
		}
	}
	if c, _ := st.Output("compile")["compiled"].(bool); !c {
		t.Fatalf("compile 应通过（join 生效，三文件齐全），output=%v", st.Output("compile")["output"])
	}
}
