package planner

// LLMPlanner 是真实的 IncrementalPlanSource：
//   - 把 tool.Registry 暴露给模型作为可调工具（OpenAI tool_calls）；
//   - 每个 Scheduler 迭代调一次 LLM：先把上一轮节点的产出作为 tool 消息回喂，
//     再让模型决定下一步要调哪些工具或结束；
//   - 返回的每个 tool_call → 一个 PlanNode。kernel 执行后产出回喂下一轮。
//
// 与 FR3 一致：和 scriptedSource 实现同一个 PlanSource.Next 形状——只是把"剧本"
// 换成了"模型决定"。Scheduler / Invoker / ExecState / async / trace 全部不动。

import (
	"context"
	"encoding/json"
	"fmt"

	"flux/model"
	"flux/runtime"
	"flux/tool"
)

// LLMPlanner 实现 runtime.PlanSource。
//
// 用法：
//
//	p := planner.NewLLMPlanner(provider, "deepseek-chat", "你的目标...", reg)
//	p.MaxRounds = 8 // FR6：planner 自带的迭代上限；kernel 的 WithMaxSteps 是兜底
//	sched.Run(ctx, p, state)
type LLMPlanner struct {
	Provider  model.Completer
	Model     string
	Goal      string
	System    string // 可选；为空时用 defaultSystemPrompt
	MaxRounds int    // FR6：planner 自带的回合上限（默认 10）

	// NoProgressLimit FR6：同一个 (tool, args) 动作重复达到此次数即判定无进展并终止（默认 4）。
	// 这是"卡在原地打转"的硬安全网，独立于 LLM 主动 give_up。
	NoProgressLimit int

	// OnAssistant 可选：每轮拿到 LLM 回复后回调其文本（推理/说明）。
	// 用于 observability（CLI 实时显示"thinking"）；nil 即无操作。
	OnAssistant func(content string)

	registry *tool.Registry
	tools    []model.ToolDefinition

	messages     []model.Message
	pending      []pendingCall // 上一轮返回的节点，待回喂结果
	round        int
	done         bool
	actionCounts map[string]int // FR6：(tool,args) 签名 → 出现次数
}

type pendingCall struct {
	ToolCallID string
	NodeName   string
}

// History 返回当前完整对话历史（供会话持久化导出）。
func (p *LLMPlanner) History() []model.Message { return p.messages }

// SetHistory 载入已保存的对话历史（续接会话）。需在 Run 之前调用。
func (p *LLMPlanner) SetHistory(h []model.Message) { p.messages = h }

// GaveUpError 表示 agent **主动判定目标不可达/前提不成立**而终止（FR6）。
// 它不是执行失败，而是"识别到无法完成并报告原因"——调用方（CLI）应区别于 error 对待。
type GaveUpError struct{ Reason string }

func (e *GaveUpError) Error() string { return "agent gave up: " + e.Reason }

// NewLLMPlanner 用 Registry 里**所有工具**作为模型菜单（M1：工具少，先全量喂；
// FR7：将来工具多了再做按目标筛选）。额外附带一个 give_up 元工具（FR6 退出阀）。
func NewLLMPlanner(provider model.Completer, modelName, goal string, reg *tool.Registry) *LLMPlanner {
	return &LLMPlanner{
		Provider:        provider,
		Model:           modelName,
		Goal:            goal,
		MaxRounds:       10,
		NoProgressLimit: 4,
		registry:        reg,
		tools:           append(buildToolDefinitions(reg), giveUpTool),
		actionCounts:    map[string]int{},
	}
}

const defaultSystemPrompt = `You are an autonomous agent that completes a goal by calling tools.

Iterate: examine prior tool results, call the next tool you need, observe its output, and continue.
When the goal is satisfied, STOP CALLING TOOLS — reply with a brief final message instead.
Never explain plans in prose while there are tool calls to make: emit the tool calls.

IMPORTANT — do not invent work. If, after exploring, the goal's premise does not hold
(e.g. it says "fix the failing test" but there is no failing test, or the referenced file
does not exist), call the give_up tool with a clear reason. Do NOT fabricate files or tasks
to make the goal seem satisfiable.

The user message is the goal.`

// giveUpTool 是 FR6 的退出阀：让 LLM 在目标不可达时主动、带原因地终止。
var giveUpTool = model.ToolDefinition{
	Type: "function",
	Function: model.FunctionSchema{
		Name:        "give_up",
		Description: "当目标无法完成时调用（例如要修的失败测试根本不存在、引用的文件缺失、前提不成立）。给出 reason 说明原因，而不是凭空造任务。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{"type": "string", "description": "为什么无法完成"},
			},
			"required": []string{"reason"},
		},
	},
}

// Next 是 runtime.PlanSource 的实现。
func (p *LLMPlanner) Next(ctx context.Context, state runtime.ExecState) ([]*runtime.PlanNode, bool, error) {
	if p.done {
		return nil, true, nil
	}

	// FR6：planner 自带回合上限。kernel 的 WithMaxSteps 是独立的兜底。
	if p.round >= p.MaxRounds {
		p.done = true
		return nil, true, fmt.Errorf("planner: max rounds (%d) exceeded", p.MaxRounds)
	}
	p.round++

	// 1) 把上一轮返回节点的产出作为 tool 消息回喂（必须紧跟那条 assistant 消息之后）。
	for _, pc := range p.pending {
		out := state.Output(pc.NodeName)
		body, _ := json.Marshal(out)
		p.messages = append(p.messages, model.Message{
			Role:       "tool",
			ToolCallID: pc.ToolCallID,
			Content:    string(body),
		})
	}
	p.pending = nil

	// 2) 首轮注入。续接会话时 messages 已载入历史 → 不重发 system，只把新目标追加为 user。
	if p.round == 1 {
		if len(p.messages) == 0 {
			sys := p.System
			if sys == "" {
				sys = defaultSystemPrompt
			}
			p.messages = append(p.messages, model.Message{Role: "system", Content: sys})
		}
		p.messages = append(p.messages, model.Message{Role: "user", Content: p.Goal})
	}

	// 3) 调 LLM。
	resp, err := p.Provider.Complete(ctx, model.Request{
		Model:      p.Model,
		Messages:   p.messages,
		Tools:      p.tools,
		ToolChoice: "auto",
	})
	if err != nil {
		return nil, false, fmt.Errorf("planner llm call: %w", err)
	}

	// 4) 记下 assistant 消息（必须在 tool 消息之前，否则上下文非法）。
	p.messages = append(p.messages, model.Message{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	})
	if p.OnAssistant != nil && resp.Content != "" {
		p.OnAssistant(resp.Content)
	}

	// 5) 没调工具 ⇒ LLM 决定完成。
	if len(resp.ToolCalls) == 0 {
		p.done = true
		return nil, true, nil
	}

	// 5.1) FR6 退出阀：LLM 主动 give_up ⇒ 带原因终止（不可达，不是执行失败）。
	for _, tc := range resp.ToolCalls {
		if tc.Function.Name == "give_up" {
			p.done = true
			return nil, true, &GaveUpError{Reason: giveUpReason(tc.Function.Arguments)}
		}
	}

	// 5.2) FR6 无进展安全网：同一 (tool,args) 动作重复达到上限 ⇒ 判定卡死并终止。
	for _, tc := range resp.ToolCalls {
		sig := tc.Function.Name + "|" + tc.Function.Arguments
		p.actionCounts[sig]++
		if p.NoProgressLimit > 0 && p.actionCounts[sig] >= p.NoProgressLimit {
			p.done = true
			return nil, true, &GaveUpError{Reason: fmt.Sprintf(
				"无进展：重复执行 %s 达 %d 次仍未推进，可能卡死或目标不可达",
				tc.Function.Name, p.actionCounts[sig])}
		}
	}

	// 6) 翻译 tool_calls → PlanNode。
	added := make([]*runtime.PlanNode, 0, len(resp.ToolCalls))
	for i, tc := range resp.ToolCalls {
		toolName := tc.Function.Name
		if _, ok := p.registry.Get(toolName); !ok {
			// LLM 调了不存在的工具：把错误回喂（FR5 同款 generate-validate-repair）。
			p.messages = append(p.messages, model.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    fmt.Sprintf(`{"error":"tool %q does not exist"}`, toolName),
			})
			continue
		}

		var input map[string]any
		if args := tc.Function.Arguments; args != "" {
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				p.messages = append(p.messages, model.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    fmt.Sprintf(`{"error":"invalid JSON arguments: %s"}`, err.Error()),
				})
				continue
			}
		}
		if input == nil {
			input = map[string]any{}
		}

		nodeName := fmt.Sprintf("r%d_%s_%d", p.round, toolName, i)
		captured := input
		added = append(added, &runtime.PlanNode{
			Name:     nodeName,
			ToolName: toolName,
			Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
				return captured, nil
			},
		})
		p.pending = append(p.pending, pendingCall{ToolCallID: tc.ID, NodeName: nodeName})
	}

	// 全部 tool_call 都无效（不存在 / args 坏）：让下一轮 LLM 看错误自己修正，
	// 但 Scheduler 这一轮会因为 runnable=0 触发"停滞"。退化处理：返回一个 no-op
	// 复活下一轮——这里直接 done 让用户看到错误更直白。
	if len(added) == 0 {
		p.done = true
		return nil, true, fmt.Errorf("planner: all tool_calls invalid in round %d", p.round)
	}

	return added, false, nil
}

func giveUpReason(args string) string {
	var p struct {
		Reason string `json:"reason"`
	}
	if args != "" {
		_ = json.Unmarshal([]byte(args), &p)
	}
	if p.Reason == "" {
		return "（未给原因）"
	}
	return p.Reason
}

// buildToolDefinitions 把 tool.Registry 转成 OpenAI tool_calls 兼容的 schema。
//
// 阶段 C：统一经 tool.DefinitionOf 取定义——本地工具（DataSchema 合成）与 MCP 工具
// （原生 JSON Schema，DefinedTool）一视同仁，不再有 planner 端的旁路断言。
func buildToolDefinitions(reg *tool.Registry) []model.ToolDefinition {
	tools := reg.List()
	defs := make([]model.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		d := tool.DefinitionOf(t)
		var params map[string]any
		if err := json.Unmarshal(d.InputSchema, &params); err != nil || params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		defs = append(defs, model.ToolDefinition{
			Type: "function",
			Function: model.FunctionSchema{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  params,
			},
		})
	}
	return defs
}
