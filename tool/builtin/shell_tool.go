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
// 与 compile 工具同样的反馈语义：命令非零退出/超时**不是工具错误**——Success 仍为 true，
// 把 stdout/stderr/exit_code/timed_out 放进结果供 planner 据此决定下一步。
//
// 超时与进程树击杀（被真实任务赚来的修复）：命令跑在**独立进程组**里；超时（或 ctx 取消）时
// 击杀**整个进程组**——否则像 `sh -c "go test"` 这种，超时只杀 sh、杀不掉 go test 孙子进程，
// 孙子继承 stdout 管道会让读取无限阻塞，把整个 agent 拖死。
//
// 安全边界（本版）：命令受限在 baseDir + 超时。生产级沙箱（白名单/容器/资源限额）尚未做。
type ShellTool struct {
	baseDir        string
	defaultTimeout time.Duration
}

func NewShellTool(baseDir string) *ShellTool {
	return &ShellTool{baseDir: baseDir, defaultTimeout: 60 * time.Second}
}

func (ShellTool) Name() string { return "shell" }

func (ShellTool) Description() string {
	return "在工作目录运行一条 shell 命令（sh -c），用于跑测试/构建/运行程序，例如 `go test ./...`。" +
		"返回 stdout、stderr、exit_code、timed_out。命令非零退出或超时不是工具错误。" +
		"可选 timeout_seconds 控制超时（默认 60）；对可能很慢/会卡死的命令应调小或自带 -timeout。"
}

func (ShellTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"command":         {Type: "string", Required: true, Desc: "要执行的 shell 命令行，如 go test ./..."},
		"timeout_seconds": {Type: "integer", Required: false, Desc: "命令超时秒数，默认 60；长命令或可能卡死的命令应调小"},
	}}
}

func (ShellTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"stdout":    {Type: "string", Desc: "标准输出"},
		"stderr":    {Type: "string", Desc: "标准错误"},
		"exit_code": {Type: "integer", Desc: "退出码，0 为成功，-1 表示被杀/超时"},
		"timed_out": {Type: "bool", Desc: "是否因超时被击杀"},
	}}
}

func (t ShellTool) Execute(ctx context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	command, _ := input["command"].(string)
	if command == "" {
		return tool.Fail(fmt.Errorf("shell: command 不能为空")), nil
	}
	timeout := t.defaultTimeout
	if secs := toSeconds(input["timeout_seconds"]); secs > 0 {
		timeout = secs
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = t.baseDir
	setProcessGroup(cmd) // 独立进程组，便于超时时整组击杀（见平台文件）

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return tool.Success(map[string]any{"stdout": "", "stderr": err.Error(), "exit_code": -1}), nil
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	exitCode := 0
	timedOut := false
	select {
	case err := <-done:
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			} else {
				exitCode = -1
				errBuf.WriteString("\n" + err.Error())
			}
		}
	case <-time.After(timeout):
		timedOut = true
		killGroup(cmd) // 杀整个进程组（含 go test 等孙子进程），解开管道阻塞
		<-done         // killGroup 后子进程死、管道 EOF，Wait 返回
		exitCode = -1
		errBuf.WriteString(fmt.Sprintf("\n[command timed out after %s and was killed]", timeout))
	case <-ctx.Done():
		killGroup(cmd)
		<-done
		exitCode = -1
		errBuf.WriteString("\n[canceled: " + ctx.Err().Error() + "]")
	}

	data := map[string]any{
		"stdout":    outBuf.String(),
		"stderr":    errBuf.String(),
		"exit_code": exitCode,
	}
	if timedOut {
		data["timed_out"] = true
	}
	return tool.Success(data), nil
}

func (ShellTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func toSeconds(v any) time.Duration {
	switch n := v.(type) {
	case float64:
		return time.Duration(n) * time.Second
	case int:
		return time.Duration(n) * time.Second
	case int64:
		return time.Duration(n) * time.Second
	}
	return 0
}
