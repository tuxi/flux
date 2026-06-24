package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"flux/tool"
)

// ShellTool 在工作目录运行一条 shell 命令（sh -c），让代码 agent 能跑测试/构建/运行程序。
//
// 与 compile 工具同样的反馈语义：命令非零退出**不是工具错误**——Success 仍为 true，
// 把 stdout/stderr/exit_code 放进结果供 planner 据此决定下一步。
//
// 安全边界（本版）：命令受限在 baseDir（cmd.Dir）+ 超时。这是代码 agent 的必备能力
// （等同 Claude Code 的 Bash 工具），但**生产级沙箱**（命令白名单 / 容器隔离 / 资源限额）
// 尚未做——真做产品时必须补，这里诚实标注。
type ShellTool struct {
	baseDir string
	timeout time.Duration
}

func NewShellTool(baseDir string) *ShellTool {
	return &ShellTool{baseDir: baseDir, timeout: 60 * time.Second}
}

func (ShellTool) Name() string { return "shell" }

func (ShellTool) Description() string {
	return "在工作目录运行一条 shell 命令（sh -c），用于跑测试/构建/运行程序，例如 `go test ./...`。返回 stdout、stderr、exit_code。命令非零退出不是工具错误。"
}

func (ShellTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"command": {Type: "string", Required: true, Desc: "要执行的 shell 命令行，如 go test ./..."},
	}}
}

func (ShellTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"stdout":    {Type: "string", Desc: "标准输出"},
		"stderr":    {Type: "string", Desc: "标准错误"},
		"exit_code": {Type: "integer", Desc: "退出码，0 为成功"},
	}}
}

func (t ShellTool) Execute(ctx context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	command, _ := input["command"].(string)
	if command == "" {
		return tool.Fail(fmt.Errorf("shell: command 不能为空")), nil
	}

	runCtx := ctx
	if t.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Dir = t.baseDir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1 // 无法启动（命令不存在 / 超时等）
			errBuf.WriteString("\n" + err.Error())
		}
	}

	return tool.Success(map[string]any{
		"stdout":    outBuf.String(),
		"stderr":    errBuf.String(),
		"exit_code": exitCode,
	}), nil
}

func (ShellTool) Mode() tool.ExecutionMode { return tool.SyncExecution }
