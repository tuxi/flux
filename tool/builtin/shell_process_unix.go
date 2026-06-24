//go:build unix

package builtin

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让命令在独立进程组里运行（pgid = 子进程 pid）。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup 击杀整个进程组（负 pid）—— 连同 go test / 测试二进制等孙子进程一起杀掉，
// 否则它们继承的 stdout 管道会让 cmd.Wait() 无限阻塞。
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
