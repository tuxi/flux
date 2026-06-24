package planner_test

// A.3 端到端：LLM planner 在**同一个目标**里同时使用
//   - 本地工具：write_file / compile
//   - 远端 MCP 工具：fs_list_directory（@modelcontextprotocol/server-filesystem）
//
// 关键设计：本地工具没有"列目录"能力，所以 LLM 不调 MCP 的 fs_list_directory 就无从
// 知道目录里有哪些文件 —— 物理上强制"跨工具源协作"。这才真证明 MCP 工具被 planner
// 当一等公民调用（主线二 阶段 A 的命题）。
//
// 门控（两个都要设）：
//   LLM_API_KEY / LLM_BASE_URL / (LLM_MODEL)  —— 同 M1 live 测试
//   MCP_E2E=1                                 —— 允许跑真实 MCP server（需 npx）
//
//	LLM_API_KEY=... LLM_BASE_URL=https://api.deepseek.com/v1 MCP_E2E=1 \
//	  go test ./planner/ -run TestA3 -v -timeout 5m

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flux/mcp"
	"flux/model"
	"flux/planner"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

func TestA3_LLM_LocalPlusMCP(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" || os.Getenv("MCP_E2E") == "" {
		t.Skip("需同时设置 LLM_API_KEY 与 MCP_E2E=1：跳过本地+MCP 混合端到端测试")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx 不在 PATH：跳过")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		t.Fatal("LLM_BASE_URL 必填")
	}
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	// 工作目录：放几个 .txt 让 LLM 必须先列目录才知道叫什么。
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, fn := range []string{"apple.txt", "banana.txt", "cherry.txt"} {
		if err := os.WriteFile(filepath.Join(dir, fn), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module m\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// 本地工具
	reg := tool.NewRegistry()
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))

	// 远端 MCP 工具（同一目录），带 fs_ 前缀注册
	mc, err := mcp.NewStdioClient(ctx, "npx",
		[]string{"-y", "@modelcontextprotocol/server-filesystem", dir}, nil)
	if err != nil {
		t.Fatalf("connect mcp: %v", err)
	}
	defer mc.Close()
	mcpNames, err := mcp.RegisterAll(ctx, mc, reg, "fs_")
	if err != nil {
		t.Fatalf("register mcp tools: %v", err)
	}
	t.Logf("工具池：本地 write_file/compile + %d 个 MCP 工具", len(mcpNames))

	goal := `工作目录里有若干 .txt 文件，但你不知道具体文件名。请：
1) 调用 fs_list_directory（参数 path 用 "` + dir + `"）列出目录，得到 .txt 文件名；
2) 调用 write_file 写一个 main.go：把这些 .txt 文件名（仅文件名，不含路径）放进一个 []string，
   并在 main 中用 fmt 逐行打印；
3) 调用 compile 编译；若 compiled=false，读 output 报错修复后再 compile；
4) compiled=true 后停止调用任何工具。`

	p := planner.NewLLMPlanner(provider(baseURL, apiKey), modelName, goal, reg)
	p.MaxRounds = 12

	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).
		WithMaxSteps(60)

	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, p, st)
	if err != nil {
		t.Logf("loop run error: %v", err)
	}

	// ── 断言 ──
	var usedMCP, compiledOK bool
	for _, n := range st.Nodes() {
		if strings.Contains(n, "fs_list_directory") {
			usedMCP = true
		}
		if c, ok := st.Output(n)["compiled"].(bool); ok && c {
			compiledOK = true
		}
	}
	for _, n := range st.Nodes() {
		t.Logf("  node %s", n)
	}
	if !usedMCP {
		t.Fatalf("LLM 未调用 MCP 工具 fs_list_directory —— 没证明跨工具源协作 (status=%d)", res.Status)
	}
	if !compiledOK {
		t.Fatalf("最终未编译通过 (status=%d)", res.Status)
	}
	t.Logf("✅✅ 主线二 A 证明：LLM 在同一目标里调了远端 MCP 工具 + 本地工具，最终编译通过")
}

func provider(baseURL, apiKey string) *model.OpenAICompatibleProvider {
	return model.NewOpenAICompatibleProvider(baseURL, apiKey)
}
