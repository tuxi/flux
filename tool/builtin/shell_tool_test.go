package builtin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flux/tool/builtin"
)

func TestShellTool(t *testing.T) {
	dir := t.TempDir()
	sh := builtin.NewShellTool(dir)
	ctx := context.Background()

	// 1) 成功
	res, err := sh.Execute(ctx, map[string]any{"command": "echo hello"}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out, _ := res.Data["stdout"].(string); !strings.Contains(out, "hello") {
		t.Fatalf("stdout 应含 hello，得 %q", out)
	}
	if res.Data["exit_code"] != 0 {
		t.Fatalf("exit_code 应为 0，得 %v", res.Data["exit_code"])
	}

	// 2) 非零退出是反馈，不是工具错误
	res2, err := sh.Execute(ctx, map[string]any{"command": "exit 3"}, nil)
	if err != nil {
		t.Fatalf("非零退出不应是 Go 错误: %v", err)
	}
	if !res2.Success {
		t.Fatal("非零退出 Success 应仍为 true")
	}
	if res2.Data["exit_code"] != 3 {
		t.Fatalf("exit_code 应为 3，得 %v", res2.Data["exit_code"])
	}

	// 3) 工作目录受限在 baseDir
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res3, _ := sh.Execute(ctx, map[string]any{"command": "ls"}, nil)
	if out, _ := res3.Data["stdout"].(string); !strings.Contains(out, "marker.txt") {
		t.Fatalf("ls 应在 baseDir 看到 marker.txt，得 %q", out)
	}
}

// TestShellTool_TimeoutKillsProcessTree 复现并验证修复：被赚来的真实缺口——
// 卡死的命令（带孙子进程）超时后必须被整组击杀，而不是把工具/agent 无限拖住。
// `sh -c 'sleep 30'` 模拟 `sh -c "go test"` 那种"孙子进程持有 stdout 管道"的情形。
func TestShellTool_TimeoutKillsProcessTree(t *testing.T) {
	dir := t.TempDir()
	sh := builtin.NewShellTool(dir)

	start := time.Now()
	res, err := sh.Execute(context.Background(),
		map[string]any{"command": "sh -c 'sleep 30'", "timeout_seconds": 1}, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("应在 ~1s 超时返回（整组被杀），实际 %v —— 进程树没被杀掉，回归了", elapsed)
	}
	if res.Data["timed_out"] != true {
		t.Fatalf("应标记 timed_out=true，得 %v", res.Data["timed_out"])
	}
	if res.Data["exit_code"] == 0 {
		t.Fatalf("超时被杀 exit_code 不应为 0")
	}
	t.Logf("✅ sleep 30（含孙子进程）在 %v 内被整组击杀，timed_out=%v", elapsed, res.Data["timed_out"])
}
