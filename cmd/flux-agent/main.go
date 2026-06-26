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

	"flux/definition"
	"flux/mcp"
	"flux/model"
	"flux/planner"
	"flux/runtime"
	"flux/session"
	"flux/skill"
	"flux/tool"
	"flux/tool/builtin"
	"flux/workflow"

	"gopkg.in/yaml.v3"
)

func main() {
	baseURL := flag.String("base-url", envOr("LLM_BASE_URL", "https://api.deepseek.com/v1"), "OpenAI-compatible base url")
	modelName := flag.String("model", envOr("LLM_MODEL", "deepseek-chat"), "model name")
	maxRounds := flag.Int("max-rounds", 20, "planner 回合上限（FR6 停机保证）")
	cont := flag.Bool("continue", false, "续接该仓库上次的会话")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: flux-agent [flags] \"<goal>\"            (repo = 当前目录)")
		fmt.Fprintln(os.Stderr, "       flux-agent [flags] <repo-dir> \"<goal>\"")
		flag.PrintDefaults()
	}
	flag.Parse()

	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		fail("缺少 LLM_API_KEY 环境变量")
	}
	// 参数：<goal> 或 <repo-dir> <goal>。只给一个 ⇒ 它是 goal，repo 默认当前目录。
	args := flag.Args()
	var repoDir, goal string
	switch len(args) {
	case 1:
		repoDir, goal = ".", args[0]
	case 2:
		repoDir, goal = args[0], args[1]
	default:
		flag.Usage()
		os.Exit(2)
	}
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
	reg.Register(builtin.NewReadFileTool(repoDir))
	reg.Register(builtin.NewShellTool(repoDir))
	reg.Register(builtin.NewGrepTool(repoDir))

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

	// ── Skill Runtime 接入 ──
	// 把 ./skills 和 ~/.flux/skills 下的 SKILL.md 变成 agent 可选的工具。
	// planner 看到的还是统一的 ToolDefinition——底层是 tool/workflow/agent 它不关心。
	skillRoots := skill.DefaultRoots()
	loader := skill.NewLoader(skillRoots...)
	resolver := skill.NewResolver(func(name string) (tool.Tool, error) {
		if t, ok := reg.Get(name); ok {
			return t, nil
		}
		return nil, fmt.Errorf("skill 引用的工具 %q 未注册", name)
	})
	// WorkflowSkill 的执行入口：编译 workflow.yaml → engine.Run → 返回结果。
	// 子 workflow 用独立 scheduler 运行（隔离 trace，同一 tool registry）。
	skillRunner := func(ctx context.Context, spec *skill.SkillSpec, input map[string]any) (*tool.Result, error) {
		if spec.Dir == "" {
			return tool.Fail(fmt.Errorf("workflow skill %q: Dir 为空，无法定位 workflow.yaml", spec.Name)), nil
		}
		wfPath := filepath.Join(spec.Dir, spec.Workflow)
		wfData, err := os.ReadFile(wfPath)
		if err != nil {
			return tool.Fail(fmt.Errorf("workflow skill %q: 读取 %s 失败: %w", spec.Name, wfPath, err)), nil
		}
		// YAML → struct：yaml.v3 不认 json tag，先解成通用节点再经 JSON 桥接到 WorkflowDefinition。
		var raw any
		if err := yaml.Unmarshal(wfData, &raw); err != nil {
			return tool.Fail(fmt.Errorf("workflow skill %q: YAML 解析失败: %w", spec.Name, err)), nil
		}
		jsonBytes, _ := json.Marshal(raw)
		var wfDef definition.WorkflowDefinition
		if err := json.Unmarshal(jsonBytes, &wfDef); err != nil {
			return tool.Fail(fmt.Errorf("workflow skill %q: 定义解析失败: %w", spec.Name, err)), nil
		}
		plan, err := workflow.Compile(&wfDef, func(string) bool { return false })
		if err != nil {
			return tool.Fail(fmt.Errorf("workflow skill %q: 编译失败: %w", spec.Name, err)), nil
		}
		subSched := runtime.NewScheduler(
			planner.NewToolInvoker(reg),
			planner.NopAwait{},
			planner.NopStore{},
			planner.NopEmitter{},
		).WithMaxSteps(50) // 子 workflow 独立步数限制
		state := runtime.NewMemState(input)
		res, err := subSched.Run(ctx, runtime.NewStaticSource(plan), state)
		if err != nil {
			return tool.Fail(fmt.Errorf("workflow skill %q: 执行失败: %w", spec.Name, err)), nil
		}
		if res.Status != runtime.StatusCompleted {
			return tool.Fail(fmt.Errorf("workflow skill %q: 未完成（status=%d）", spec.Name, res.Status)), nil
		}
		// 收集所有节点输出作为 workflow 结果
		outputs := map[string]any{}
		for _, n := range state.Nodes() {
			if o := state.Output(n); len(o) > 0 {
				outputs[n] = o
			}
		}
		return tool.Success(outputs), nil
	}
	skillReg := skill.NewRegistry()
	_, _ = skill.LoadAndRegister(ctx, loader, resolver, skillReg)
	if err := skill.RegisterAsTools(skillReg, reg, skillRunner); err != nil {
		fmt.Printf("（部分 skill 未能注册为工具：%v）\n", err)
	}

	// save_as_skill：agent 跑通后把 DAG 固化成新 skill。固化到第一个 root（项目级 ./skills）。
	saveDir := skillRoots[0]
	if abs, err := filepath.Abs(saveDir); err == nil {
		saveDir = abs
	}
	saveTool := skill.NewSaveAsSkillTool(saveDir)
	reg.Register(saveTool)

	allTools := reg.List()
	builtinCount := 3 + len(mcpNames) + 1 // read_file+shell+grep + N MCP + save_as_skill
	skillCount := len(allTools) - builtinCount
	fmt.Printf("flux-agent | model=%s | repo=%s\n", *modelName, repoDir)
	fmt.Printf("工具菜单：%d 个（%d builtin + %d skill）", len(allTools), builtinCount, skillCount)
	if skillCount > 0 {
		fmt.Printf(" — skill 已进入决策菜单 ✅")
	}
	fmt.Println()
	fmt.Printf("目标：%s\n", goal)
	fmt.Println("──────────────────────────────────────────")

	// ── planner + 实时 observability ──
	p := planner.NewLLMPlanner(model.NewOpenAICompatibleProvider(*baseURL, apiKey), *modelName, goal, reg)
	p.MaxRounds = *maxRounds

	// 进化闭环：save_as_skill 成功后，重新加载并把新 skill 注册进 reg，
	// 再刷新 planner 的工具快照——使同一会话内 agent 立刻能复用刚固化的 skill。
	saveTool.OnSave = func(name string) {
		fresh := skill.NewRegistry()
		skill.LoadAndRegister(ctx, loader, resolver, fresh)
		if err := skill.RegisterAsTools(fresh, reg, skillRunner); err != nil {
			fmt.Printf("（新 skill 重注册部分失败：%v）\n", err)
		}
		p.RefreshTools()
		fmt.Printf("🌱 新 skill 已固化并上线，本会话即可复用：%s\n", name)
	}

	// 会话持久化：通过 session.Store 端口（单机 = FileStore，服务端 = Postgres）。
	sessStore := session.NewFileStore(filepath.Join(homeDir(), ".flux-agent", "sessions"))
	if *cont {
		s, err := sessStore.Load(ctx, repoDir)
		if err != nil {
			fmt.Printf("（无法续接上次会话：%v —— 按全新会话开始）\n", err)
		} else if s != nil && len(s.Messages) > 0 {
			p.SetHistory(s.Messages)
			fmt.Printf("（续接上次会话，载入 %d 条消息）\n", len(s.Messages))
		}
	}
	var finalText string
	p.OnAssistant = func(text string) {
		finalText = text                        // 完整留存最终回答
		fmt.Printf("💭 %s\n", truncate(text, 400)) // 直播仍截断，保持滚动可读
	}

	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).
		WithTrace(&consoleSink{w: os.Stdout}, "agent").
		WithMaxSteps(*maxRounds * 4)

	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, p, st)

	// 存盘，供下次 --continue 续接。
	s := &session.Session{Key: repoDir, Workdir: repoDir, Messages: p.History(), UpdatedAt: time.Now()}
	if saveErr := sessStore.Save(ctx, s); saveErr != nil {
		fmt.Printf("（会话保存失败：%v）\n", saveErr)
	}

	fmt.Println("──────────────────────────────────────────")
	if finalText != "" {
		// 完整打印最终回答 —— 对"分析/回答"型任务，这才是交付物。
		fmt.Printf("📄 最终回答：\n%s\n\n", finalText)
	}
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

func homeDir() string {
	d, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return d
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
