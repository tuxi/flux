package planner_test

// v2 第一个真实产品：代码 agent（M2）。
//
// 与 M1 玩具（从零盲写）的本质区别：这里改**现有代码**——agent 必须先读源码、跑测试、
// 看失败、修、再跑，直到测试绿。这会逼出读文件/shell/loop 规模化等真实缺口。
//
// 组装（刻意复用已有，不预先建任何 M2.x 基建）：
//   - 类型 A 的 LLMPlanner（control loop）
//   - MCP filesystem server（stage A）→ fs_read_file / fs_write_file / fs_list_directory ...
//   - 本地 shell 工具（唯一新增）→ 跑 `go test`
//
// 门控：需 LLM_API_KEY + MCP_E2E=1（npx）。
//
//	LLM_API_KEY=... LLM_BASE_URL=https://api.deepseek.com/v1 MCP_E2E=1 \
//	  go test ./planner/ -run TestCodeAgent -v -timeout 6m

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flux/mcp"
	"flux/planner"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

func TestCodeAgent_FixFailingTest(t *testing.T) {
	if os.Getenv("LLM_API_KEY") == "" || os.Getenv("MCP_E2E") == "" {
		t.Skip("需同时设置 LLM_API_KEY 与 MCP_E2E=1：跳过代码 agent 端到端测试")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx 不在 PATH：跳过")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	// ── 造一个 go test 失败的小仓库 ──
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	writeFile(t, dir, "go.mod", "module mathx\n\ngo 1.20\n")
	// bug：Add 返回 a-b
	writeFile(t, dir, "math.go", "package mathx\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n")
	writeFile(t, dir, "math_test.go", "package mathx\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2,3)=%d, want 5\", got)\n\t}\n}\n")

	// 前置确认：现在确实 fail（否则证明不了 agent 修好了）
	if cmd := exec.Command("go", "test", "./..."); func() bool { cmd.Dir = dir; return cmd.Run() == nil }() {
		t.Fatal("前置：仓库应当 go test 失败")
	}

	// ── 工具池：MCP filesystem（fs_ 前缀）+ 本地 shell ──
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	reg := tool.NewRegistry()
	reg.Register(builtin.NewShellTool(dir))

	mc, err := mcp.NewStdioClient(ctx, "npx",
		[]string{"-y", "@modelcontextprotocol/server-filesystem", dir}, nil)
	if err != nil {
		t.Fatalf("connect mcp: %v", err)
	}
	defer mc.Close()
	if _, err := mcp.RegisterAll(ctx, mc, reg, "fs_"); err != nil {
		t.Fatalf("register mcp: %v", err)
	}

	goal := `工作目录是一个 Go module，` + "`go test ./...`" + ` 当前失败。请：
1) 用 fs_ 工具（如 fs_list_directory、fs_read_file）读源码，找出 bug；
2) 用 fs_write_file 写修复后的文件；
3) 用 shell 运行 ` + "`go test ./...`" + ` 验证；若仍失败，读输出继续修；
4) 测试通过（exit_code=0）后停止调用任何工具。`

	p := planner.NewLLMPlanner(provider(baseURL, apiKey()), modelName, goal, reg)
	p.MaxRounds = 14

	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).WithMaxSteps(60)
	st := runtime.NewMemState(nil)

	res, err := sched.Run(ctx, p, st)
	if err != nil {
		t.Logf("loop run error: %v", err)
	}

	var usedFS, usedShell bool
	for _, n := range st.Nodes() {
		t.Logf("  node %s", n)
		if strings.Contains(n, "fs_") {
			usedFS = true
		}
		if strings.Contains(n, "shell") {
			usedShell = true
		}
	}

	// ── 硬证据：仓库现在 go test 通过 ──
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("agent 处理后 go test 仍失败 (status=%d):\n%s", res.Status, out)
	}
	if !usedFS {
		t.Fatal("agent 未使用 MCP filesystem 工具（没真读现有代码？）")
	}
	if !usedShell {
		t.Fatal("agent 未使用 shell 跑测试")
	}
	fixed, _ := os.ReadFile(filepath.Join(dir, "math.go"))
	t.Logf("✅✅ 代码 agent：读现有代码 → 修 bug → shell 跑 go test → 绿。修复后 math.go:\n%s", fixed)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func apiKey() string { return os.Getenv("LLM_API_KEY") }
