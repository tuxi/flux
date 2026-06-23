package planner_test

// 真实 LLM 跑 code→compile control loop。
//
// 环境变量（用户自己设）：
//   LLM_API_KEY   必填。任何 OpenAI-Compatible 提供商的 key。
//   LLM_BASE_URL  必填。例如 https://api.deepseek.com/v1
//   LLM_MODEL     可选。默认 deepseek-chat。
//
// 没设 LLM_API_KEY 时本测试 t.Skip ——`go test ./...` 在普通环境不会失败。
//
// 验证命题：
//   - 真实 LLM（不是脚本）通过 PlanSource 接缝驱动 kernel；
//   - kernel 不变（同一个 Scheduler.Run / Invoker / MemState）；
//   - 反馈闭环成立：第一次编译失败的报错被回喂给 LLM，LLM 据此修代码；
//   - 终止：编译通过后 LLM 不再调工具，loop 干净结束。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"flux/model"
	"flux/planner"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

// reportRounds 按节点名前缀 r{round}_ 把执行分组打印，让我们看到 LLM 实际走了几轮、每轮调了什么。
// 节点名是 LLMPlanner 起的：r{round}_{tool}_{idx}。
func reportRounds(t *testing.T, st *runtime.MemState) {
	t.Helper()
	rounds := map[string][]string{}
	for _, n := range st.Nodes() {
		parts := strings.SplitN(n, "_", 2)
		key := parts[0] // r1, r2, ...
		rounds[key] = append(rounds[key], n)
	}
	keys := make([]string, 0, len(rounds))
	for k := range rounds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("── 执行回合一览（共 %d 个节点，%d 轮）──", len(st.Nodes()), len(keys))
	for _, k := range keys {
		ns := rounds[k]
		sort.Strings(ns)
		for _, n := range ns {
			out := st.Output(n)
			summary := "(无输出)"
			if c, ok := out["compiled"].(bool); ok {
				summary = fmt.Sprintf("compiled=%v", c)
				if !c {
					if s, _ := out["output"].(string); s != "" {
						lines := strings.SplitN(strings.TrimSpace(s), "\n", 6)
						summary += " err:" + strings.Join(lines, " | ")
					}
				}
			} else if p, ok := out["path"].(string); ok {
				summary = "wrote " + filepath.Base(p)
			}
			t.Logf("  %s  %s", n, summary)
		}
	}
	// 简洁的"是否真触发了反馈"诊断
	var compiles, failedCompiles int
	for _, n := range st.Nodes() {
		if c, ok := st.Output(n)["compiled"].(bool); ok {
			compiles++
			if !c {
				failedCompiles++
			}
		}
	}
	switch {
	case compiles >= 2 && failedCompiles >= 1:
		t.Logf("✅ 反馈环触发：%d 次编译，其中 %d 次失败后 LLM 据此修复并重试", compiles, failedCompiles)
	case compiles == 1:
		t.Logf("⚠️  LLM 一次写对，没触发反馈环（mechanism 通了，但 control-loop 这次没展开）")
	default:
		t.Logf("🤔 编译次数=%d 失败=%d", compiles, failedCompiles)
	}
}

func TestM1_LLMPlanner_CodeCompileLoop(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY 未设置：跳过 live LLM 测试")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		t.Fatal("LLM_BASE_URL 必填，例如 https://api.deepseek.com/v1")
	}
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	// 1) 干净的 Go module 工作目录
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module m\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2) 工具
	reg := tool.NewRegistry()
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))

	// 3) Planner：真实 LLM
	provider := model.NewOpenAICompatibleProvider(baseURL, apiKey)
	goal := `请写一个最小可编译的 Go 程序到 main.go，main 函数打印 "hello"。
步骤：调用 write_file 写代码 → 调用 compile 编译。若 compile 返回 compiled=false，
读 output 里的错误，调 write_file 写修复版，再 compile。compile 返回 compiled=true 后停止调工具，回复"完成"。`

	p := planner.NewLLMPlanner(provider, modelName, goal, reg)
	p.MaxRounds = 8 // FR6：planner 端上限

	// 4) Kernel（不变）
	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).
		WithMaxSteps(40) // FR6：kernel 兜底硬上限

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, p, st)
	if err != nil {
		t.Fatalf("loop run error: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("expected Completed, got status=%d", res.Status)
	}

	// 验证：state 里至少存在一个 compile 节点且 compiled=true（loop 收敛）
	var sawSuccessfulCompile bool
	for _, name := range st.Nodes() {
		if c, ok := st.Output(name)["compiled"].(bool); ok && c {
			sawSuccessfulCompile = true
			break
		}
	}
	if !sawSuccessfulCompile {
		t.Fatalf("never reached a successful compile in %d nodes", len(st.Nodes()))
	}

	// 真实落地了 main.go
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Fatalf("main.go 未落地: %v", err)
	}
	t.Logf("✅ M1 live LLM loop 成功；写出节点数=%d", len(st.Nodes()))
	reportRounds(t, st)
}

// TestM1_LLMPlanner_ForceIteration 用一个**大概率第一次写不对**的目标，
// 真正逼出 control loop 的反馈环（FR6 / M1 命题核心）。
//
// 用 Go 泛型——小模型经常把方法接收者上的类型参数写错
// （`func (s *Stack[T]) Push(v T)` vs 漏写 `[T]`、把 `any` 写成 `interface{}` 等组合），
// 大概率一次过不了 `go build`。这才是"看报错 → 改"路径的真实演练。
//
// 注：这是 STRESS 测试，对模型能力敏感；deepseek-chat 可能一次写对（说明它强）；
// 用较弱的模型更容易看到 2+ 轮迭代。无论几轮，reportRounds 都会清楚显示发生了什么。
func TestM1_LLMPlanner_ForceIteration(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY 未设置：跳过 live LLM 测试")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		t.Fatal("LLM_BASE_URL 必填，例如 https://api.deepseek.com/v1")
	}
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module m\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))

	provider := model.NewOpenAICompatibleProvider(baseURL, apiKey)
	goal := `用 Go 泛型实现一个完整可编译的 main.go，要求：
1) 定义类型 Stack[T any]，带 Push(v T)、Pop() (T, bool)、Len() int 三个指针接收者方法；
2) 在 main 中演示：构造 Stack[int]，依次 Push 1,2,3，然后循环 Pop 直到空，每次 Pop 都 fmt.Println 弹出的值；
3) 整个 main.go 是单一文件，无外部依赖。

工作流程：write_file 写代码 → compile 编译 → 若 compiled=false，仔细阅读 output 报错并写修复版 → compile。
compiled=true 后停止调用任何工具。`

	p := planner.NewLLMPlanner(provider, modelName, goal, reg)
	p.MaxRounds = 10

	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).
		WithMaxSteps(50)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, p, st)
	if err != nil {
		t.Logf("loop run error（也算结果）: %v", err)
	}

	reportRounds(t, st)

	// 唯一的硬断言：最终编译通过。是否迭代用日志看（FR6 = 不卡死即可）。
	var sawSuccessfulCompile bool
	for _, n := range st.Nodes() {
		if c, ok := st.Output(n)["compiled"].(bool); ok && c {
			sawSuccessfulCompile = true
			break
		}
	}
	if !sawSuccessfulCompile {
		t.Fatalf("MaxRounds=%d 内未通过编译 status=%d", p.MaxRounds, res.Status)
	}
	if res.Status != runtime.StatusCompleted {
		t.Logf("⚠️  编译过了但 Scheduler 状态非 Completed: %d（可能 LLM 没干净停止）", res.Status)
	}
}

// TestM1_LLMPlanner_SeededBug 用**确定性**方式验证反馈环：
// 预置一个已知编译失败的 main.go，LLM 不知道里面是什么 → 必须先 compile 看报错 → 修。
//
// 与前两个测试的区别：那两个是"看 LLM 概率上会不会自己出错"，本测试是"物理上无法绕过反馈"。
// 唯一能绕过的方式是 LLM 不调 compile 而直接 write_file 写覆盖——那本身就是诊断信息。
func TestM1_LLMPlanner_SeededBug(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY 未设置：跳过 live LLM 测试")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		t.Fatal("LLM_BASE_URL 必填")
	}
	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module m\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 预置已知编译失败的代码：x、y 两个变量都声明未使用（双错误，更明显）。
	seeded := `package main

import "fmt"

func main() {
	x := 1
	y := 2
	fmt.Println("hello")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	reg.Register(builtin.NewWriteFileTool(dir))
	reg.Register(builtin.NewCompileTool(dir))

	provider := model.NewOpenAICompatibleProvider(baseURL, apiKey)
	goal := `工作目录里已经存在一个 main.go，但它编译失败。你不知道它的内容。

要求：
1) 先调用 compile 看具体报错；
2) 根据报错，调用 write_file 写一个修复版的 main.go；保留 import "fmt" 和 fmt.Println("hello") 这个调用；
3) 再调用 compile 验证；
4) compile 返回 compiled=true 后停止调任何工具。

不要在没看到 compile 报错之前调用 write_file。`

	p := planner.NewLLMPlanner(provider, modelName, goal, reg)
	p.MaxRounds = 8

	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).
		WithMaxSteps(40)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	st := runtime.NewMemState(nil)
	res, err := sched.Run(ctx, p, st)
	if err != nil {
		t.Logf("loop run error: %v", err)
	}

	reportRounds(t, st)

	// ── 硬证据断言 ──

	// 1) 最终编译必须通过（否则修复失败）。
	var lastCompileOK bool
	var compileCount, failedCount int
	var firstCallWasWrite bool
	var seenAnyTool bool
	for _, n := range st.Nodes() {
		if !seenAnyTool && strings.Contains(n, "write_file") {
			firstCallWasWrite = true
		}
		if strings.Contains(n, "compile") {
			seenAnyTool = true
			compileCount++
			c, _ := st.Output(n)["compiled"].(bool)
			if !c {
				failedCount++
			}
			lastCompileOK = c
		} else if strings.Contains(n, "write_file") {
			seenAnyTool = true
		}
	}
	if !lastCompileOK {
		t.Fatalf("终态未编译通过 status=%d", res.Status)
	}
	// 2) 必须 ≥1 次失败的编译（这就是"看到反馈"的硬证据）。
	if failedCount < 1 {
		if firstCallWasWrite {
			t.Fatalf("⚠️  LLM 违反指令：没先 compile 就 write_file 覆盖了。反馈环未被触发——但这是 LLM 不听话，不是机制问题。compileCount=%d", compileCount)
		}
		t.Fatalf("反馈环未被触发：%d 次编译全部成功（预置的 main.go 应当首次失败），seeded 可能被意外覆盖", compileCount)
	}
	t.Logf("✅✅ 反馈环硬证据：%d 次编译中 %d 次失败（首次失败的报错被 LLM 用于决定下一步动作），最终通过", compileCount, failedCount)
}
