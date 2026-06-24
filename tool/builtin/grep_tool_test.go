package builtin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flux/tool/builtin"
)

func TestGrepTool(t *testing.T) {
	dir := t.TempDir()
	wf := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wf("a.go", "package p\n\nfunc Hello() string { return \"hi\" }\n")
	wf("b.go", "package p\n\nfunc Goodbye() string { return \"bye\" }\n")
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	wf("sub/c.go", "package p\n\nfunc HelloWorld() string { return Hello() + \" world\" }\n")
	_ = os.MkdirAll(filepath.Join(dir, "empty"), 0o755)

	g := builtin.NewGrepTool(dir)
	ctx := context.Background()

	// 1) 匹配到内容：grep "Hello"
	res, err := g.Execute(ctx, map[string]any{"pattern": "Hello"}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("应为 success: %s", res.Error)
	}
	matches, _ := res.Data["matches"].(string)
	if !strings.Contains(matches, "a.go") || !strings.Contains(matches, "c.go") {
		t.Fatalf("Hello 应命中 a.go 和 c.go，得 %q", matches)
	}
	if c, _ := res.Data["count"].(int); c < 2 {
		t.Fatalf("count 应 ≥2，得 %d", c)
	}

	// 2) 限定路径：只搜 sub/
	res2, _ := g.Execute(ctx, map[string]any{"pattern": "Hello", "path": "sub"}, nil)
	if res2.Data["count"].(int) != 1 {
		t.Fatalf("限定 sub 应只命中 1 个，得 %d", res2.Data["count"])
	}

	// 3) 无匹配：不是工具错误
	res3, _ := g.Execute(ctx, map[string]any{"pattern": "NotFoundXYZ"}, nil)
	if !res3.Success || res3.Data["count"] != 0 {
		t.Fatalf("无匹配 success=true count=0，实际 success=%v count=%d", res3.Success, res3.Data["count"])
	}
	t.Logf("✅ grep：Hello 命中 a/c、限定路径 sub、无匹配不报错")
}
