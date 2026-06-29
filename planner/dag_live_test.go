package planner_test

// 类型 B live 证明：真实 LLM 一次性生成一张并行 DAG → 校验 → kernel 执行。
// 复用 mcp_e2e_test.go 里的 provider() 助手。
//
//	LLM_API_KEY=... LLM_BASE_URL=https://api.deepseek.com/v1 \
//	  go test ./planner/ -run TestB_LLM -v -timeout 5m

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuxi/flux/model"
	"github.com/tuxi/flux/planner"
	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

// provider 创建 OpenAI-compatible provider。零值超时 = 90s。
func provider(baseURL, apiKey string) *model.OpenAICompatibleProvider {
	return &model.OpenAICompatibleProvider{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	}
}

func TestB_LLM_GeneratesParallelDAG(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY 未设置：跳过类型 B live 测试")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))

	goal := `生成一个拆成恰好两个文件的可编译 Go 程序：
- helper.go：package main，含函数 add(a, b int) int 返回 a+b；
- main.go：package main，main 函数调用 add(2,3) 并用 fmt.Println 打印结果。
然后编译。请把两个 write_file 安排成**并行**（彼此无 depends_on），compile 依赖这两个 write。`

	p := planner.NewDAGPlanner(provider(baseURL, apiKey), modelName, goal, reg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	plan, err := p.Generate(ctx)
	if err != nil {
		t.Fatalf("生成/校验 DAG 失败: %v", err)
	}

	// 检查 LLM 真生成了并行结构：≥2 个 write_file 节点，且 compile 依赖它们。
	var writes, compiles int
	var compileDeps int
	for _, n := range plan.Nodes {
		switch n.ToolName {
		case "write_file":
			writes++
		case "compile":
			compiles++
			compileDeps = len(n.DependsOn)
		}
		t.Logf("  node %s tool=%s depends_on=%v", n.Name, n.ToolName, n.DependsOn)
	}
	if writes < 2 {
		t.Fatalf("期望 ≥2 个并行 write_file，得 %d", writes)
	}
	if compiles < 1 || compileDeps < 2 {
		t.Fatalf("期望 compile 依赖 ≥2 个 write，得 compiles=%d deps=%d", compiles, compileDeps)
	}

	// 执行
	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).WithMaxSteps(60)
	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, runtime.NewStaticSource(plan), st)
	if err != nil {
		t.Fatalf("执行 DAG: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("status=%d", res.Status)
	}

	var compiledOK bool
	for _, n := range st.Nodes() {
		if c, ok := st.Output(n)["compiled"].(bool); ok && c {
			compiledOK = true
		}
	}
	if !compiledOK {
		t.Fatal("DAG 执行后未编译通过")
	}
	// 确认确实是多文件（两个 .go 都落地）
	entries, _ := os.ReadDir(dir)
	var goFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			goFiles = append(goFiles, e.Name())
		}
	}
	t.Logf("✅✅ 类型 B 证明：LLM 一次性生成并行 DAG（%d write ∥ → compile），校验通过，kernel 调度执行，多文件编译成功：%v", writes, goFiles)
}
