package mcp_test

// A.2：把 MCP 工具注册进 flux/tool.Registry，再从 Registry 取出执行 —— 证明 MCP 工具
// 在 Flux 里就是普通 tool.Tool，planner 无需知道它是远端的。
//
//	MCP_E2E=1 go test ./mcp/ -run TestMCP_Adapter -v -timeout 3m

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuxi/flux/mcp"
	"github.com/tuxi/flux/tool"
)

func TestMCP_Adapter_RegistryRoundTrip(t *testing.T) {
	requireE2E(t)

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c, err := mcp.NewStdioClient(ctx, "npx",
		[]string{"-y", "@modelcontextprotocol/server-filesystem", dir}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	reg := tool.NewRegistry()
	names, err := mcp.RegisterAll(ctx, c, reg, "fs_") // 前缀防与本地 write_file 撞名
	if err != nil {
		t.Fatalf("register all: %v", err)
	}
	t.Logf("注册 %d 个 MCP 工具（带 fs_ 前缀）", len(names))

	// 从 Registry 取出 —— planner 也是这么拿的
	tl, ok := reg.Get("fs_list_directory")
	if !ok {
		t.Fatalf("fs_list_directory 未注册，实际: %v", names)
	}

	// 1) 原生 JSON Schema 直供（阶段 C：DefinedTool.Definition()，不走有损 DataSchema）
	dt, ok := tl.(tool.DefinedTool)
	if !ok {
		t.Fatal("MCP 适配器应实现 tool.DefinedTool")
	}
	if !json.Valid(dt.Definition().InputSchema) {
		t.Fatal("Definition().InputSchema 应是合法 JSON Schema")
	}

	// 2) 从 Registry 执行，行为与直接 client 调用一致
	res, err := tl.Execute(ctx, map[string]any{"path": dir}, nil)
	if err != nil {
		t.Fatalf("execute via registry: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected Success=true (MCP isError 走 Data，不应是 run 错误)")
	}
	content, _ := res.Data["content"].(string)
	if !strings.Contains(content, "alpha.txt") {
		t.Fatalf("结果应含 alpha.txt，实际: %v", res.Data)
	}
	t.Logf("✅ MCP 工具经 tool.Registry 执行命中 alpha.txt；isError=%v", res.Data["isError"])

	// 3) isError 作为反馈而非崩溃：调一个不存在的路径
	res2, err := tl.Execute(ctx, map[string]any{"path": filepath.Join(dir, "nope_does_not_exist")}, nil)
	if err != nil {
		t.Fatalf("不存在路径不应是传输错误: %v", err)
	}
	if !res2.Success {
		t.Fatal("工具内部错误应仍 Success=true（反馈给 planner，不终结 run）")
	}
	if isErr, _ := res2.Data["isError"].(bool); !isErr {
		t.Logf("注：该 server 对不存在路径未置 isError（也可接受），content=%v", res2.Data["content"])
	} else {
		t.Logf("✅ isError=true 被当作反馈装进 Data（不崩 run）")
	}
}
