package mcp_test

// A.1 端到端：连真实的 @modelcontextprotocol/server-filesystem（stdio），
// initialize → tools/list → tools/call(list_directory)。
//
// 门控：默认 skip。设 MCP_E2E=1 才跑（需要 npx + 首次会联网下载 npm 包），
// 这样普通 `go test ./...` 不被拖慢/联网。
//
//	MCP_E2E=1 go test ./mcp/ -run TestMCP_Filesystem -v -timeout 3m

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flux/mcp"
)

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("MCP_E2E") == "" {
		t.Skip("MCP_E2E 未设置：跳过真实 MCP server 端到端测试")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx 不在 PATH：跳过")
	}
}

func TestMCP_Filesystem_ListAndCall(t *testing.T) {
	requireE2E(t)

	dir := t.TempDir()
	// macOS 的 /var/folders 是指向 /private/var/... 的符号链接；filesystem server 会把
	// 允许目录解析成 realpath，故传参前先解析，否则会"path outside allowed directories"。
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
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
	t.Logf("connected: server=%s v%s proto=%s", c.ServerName, c.ServerVersion, c.ProtocolVersion)

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected non-empty tool list")
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	t.Logf("server 暴露 %d 个工具，例如: list_directory=%v read_file=%v", len(tools), names["list_directory"], names["read_file"])
	if !names["list_directory"] {
		t.Fatalf("filesystem server 应有 list_directory，实际: %v", keys(names))
	}

	res, err := c.CallTool(ctx, "list_directory", map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("call list_directory: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_directory 返回 isError: %s", res.Text())
	}
	text := res.Text()
	if !strings.Contains(text, "hello.txt") {
		t.Fatalf("list_directory 结果应包含 hello.txt，实际:\n%s", text)
	}
	t.Logf("✅ tools/call list_directory 命中 hello.txt:\n%s", text)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
