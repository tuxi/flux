// Command flux-agent 是 v2 的第一个真实产品：一个代码 agent。
//
// 它把已有的零件组装成可对着真实仓库运行的东西：
//   - 类型 A 的 LLMPlanner（control loop）
//   - MCP filesystem server（读/写/列目录）指向目标仓库
//   - 本地 shell 工具（跑测试/构建/运行）
//   - 实时 observability：把 kernel 的 trace/assistant 流接到 stdout
//     （thinking / planning / executing），而不是只在结束时 dump 日志。
//
// 用法：
//
//	export LLM_API_KEY=...                       # 必填
//	export LLM_BASE_URL=https://api.deepseek.com/v1   # 可选，默认 deepseek
//	go run ./cmd/flux-agent <repo-dir> "<目标>"
//
// 需要 npx（MCP filesystem server 是 npm 包）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"flux/mcp"
	"flux/model"
	"flux/planner"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

func main() {
	baseURL := flag.String("base-url", envOr("LLM_BASE_URL", "https://api.deepseek.com/v1"), "OpenAI-compatible base url")
	modelName := flag.String("model", envOr("LLM_MODEL", "deepseek-chat"), "model name")
	maxRounds := flag.Int("max-rounds", 20, "planner 回合上限（FR6 停机保证）")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: flux-agent [flags] <repo-dir> \"<goal>\"")
		flag.PrintDefaults()
	}
	flag.Parse()

	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		fail("缺少 LLM_API_KEY 环境变量")
	}
	args := flag.Args()
	if len(args) < 2 {
		flag.Usage()
		os.Exit(2)
	}
	repoDir, goal := args[0], args[1]
	if abs, err := filepath.Abs(repoDir); err == nil {
		repoDir = abs
	}
	if r, err := filepath.EvalSymlinks(repoDir); err == nil {
		repoDir = r // filesystem server 按 realpath 校验允许目录
	}
	if fi, err := os.Stat(repoDir); err != nil || !fi.IsDir() {
		fail(fmt.Sprintf("repo-dir 不是有效目录: %s", repoDir))
	}
	if _, err := exec.LookPath("npx"); err != nil {
		fail("需要 npx（MCP filesystem server 是 npm 包），未在 PATH 找到")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// ── 工具池 ──
	reg := tool.NewRegistry()
	reg.Register(builtin.NewShellTool(repoDir))

	mc, err := mcp.NewStdioClient(ctx, "npx",
		[]string{"-y", "@modelcontextprotocol/server-filesystem", repoDir}, nil)
	if err != nil {
		fail("连接 MCP filesystem server 失败: " + err.Error())
	}
	defer mc.Close()
	mcpNames, err := mcp.RegisterAll(ctx, mc, reg, "fs_")
	if err != nil {
		fail("注册 MCP 工具失败: " + err.Error())
	}

	fmt.Printf("flux-agent | model=%s | repo=%s\n", *modelName, repoDir)
	fmt.Printf("工具：shell + %d 个 MCP filesystem 工具\n", len(mcpNames))
	fmt.Printf("目标：%s\n", goal)
	fmt.Println("──────────────────────────────────────────")

	// ── planner + 实时 observability ──
	p := planner.NewLLMPlanner(model.NewOpenAICompatibleProvider(*baseURL, apiKey), *modelName, goal, reg)
	p.MaxRounds = *maxRounds
	p.OnAssistant = func(text string) {
		fmt.Printf("💭 %s\n", truncate(text, 400))
	}

	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).
		WithTrace(&consoleSink{w: os.Stdout}, "agent").
		WithMaxSteps(*maxRounds * 4)

	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, p, st)

	fmt.Println("──────────────────────────────────────────")
	var gaveUp *planner.GaveUpError
	switch {
	case errors.As(err, &gaveUp):
		// FR6：agent 主动判定不可达并报告原因 —— 不是失败。
		fmt.Printf("🛑 agent 主动终止：%s\n", gaveUp.Reason)
	case err != nil:
		fmt.Printf("✗ 失败: %v (status=%d)\n", err, res.Status)
		os.Exit(1)
	case res.Status == runtime.StatusCompleted:
		fmt.Printf("✅ 完成（%d 个执行节点）\n", len(st.Nodes()))
	default:
		fmt.Printf("⚠️ 结束 status=%d（%d 个节点）\n", res.Status, len(st.Nodes()))
	}
}

// consoleSink 把 kernel 的 trace 流渲染成实时控制台输出（observability）。
// 用的是建 kernel 时就留好的 TraceSink 端口 —— 接出来，不新建。
type consoleSink struct{ w io.Writer }

var nodeRe = regexp.MustCompile(`^r\d+_(.+)_\d+$`)

func toolOf(node string) string {
	if m := nodeRe.FindStringSubmatch(node); m != nil {
		return m[1]
	}
	return node
}

func (s *consoleSink) EmitControl(e runtime.TraceEvent) {
	if e.Type != runtime.TracePlanExtend {
		return
	}
	for _, n := range toStrings(e.Payload["nodes"]) {
		fmt.Fprintf(s.w, "🧠 planner → %s\n", toolOf(n))
	}
}

func (s *consoleSink) EmitExecution(e runtime.TraceEvent) {
	switch e.Type {
	case runtime.TraceInput:
		fmt.Fprintf(s.w, "   ▶ %s %s\n", toolOf(e.Node), truncate(compact(e.Payload["input"]), 200))
	case runtime.TraceOutput:
		fmt.Fprintf(s.w, "   ← %s\n", truncate(compact(e.Payload["output"]), 240))
	case runtime.TraceFail:
		fmt.Fprintf(s.w, "   ✗ %v\n", e.Payload["error"])
	}
}

func toStrings(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func compact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "flux-agent:", msg)
	os.Exit(1)
}
