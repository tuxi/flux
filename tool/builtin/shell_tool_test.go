package builtin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
