package builtin

import (
	"context"
	"os/exec"

	"github.com/tuxi/flux/tool"
)

// CompileTool 在工作目录运行 `go build ./...`。
// 关键语义：编译失败**不是工具错误**——Success 始终为 true（只要命令跑起来了），
// 把 compiled(bool) 与 output(编译器报错) 放进结果，供 planner 看到报错后去修复。
// 这正是 control loop 的反馈来源。
type CompileTool struct{ baseDir string }

func NewCompileTool(baseDir string) *CompileTool { return &CompileTool{baseDir: baseDir} }

func (CompileTool) Name() string { return "compile" }

func (CompileTool) Description() string {
	return "在工作目录运行 `go build ./...`。返回 compiled(是否通过) 与 output(编译器输出，失败时为报错)。"
}

func (CompileTool) InputSchema() tool.DataSchema { return tool.DataSchema{} }

func (CompileTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"compiled": {Type: "bool", Desc: "是否编译通过"},
		"output":   {Type: "string", Desc: "编译器输出（失败时为报错）"},
	}}
}

func (t CompileTool) Execute(ctx context.Context, _ map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = t.baseDir
	out, err := cmd.CombinedOutput()
	return tool.Success(map[string]any{
		"compiled": err == nil,
		"output":   string(out),
	}), nil
}

func (CompileTool) Mode() tool.ExecutionMode { return tool.SyncExecution }
