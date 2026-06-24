//go:build !unix

package builtin

import "os/exec"

// 非 unix 平台退化实现：进程组击杀不可用，只杀直接子进程。
func setProcessGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
