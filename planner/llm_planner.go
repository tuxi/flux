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
	Provider  *model.OpenAICompatibleProvider
	Model     string
	Goal      string
	System    string // 可选；为空时用 defaultSystemPrompt
	MaxRounds int    // FR6：planner 自带的回合上限（默认 10）

	registry *tool.Registry
	tools    []model.ToolDefinition

	messages []model.Message
	pending  []pendingCall // 上一轮返回的节点，待回喂结果
	round    int
	done     bool
}

type pendingCall struct {
	ToolCallID string
	NodeName   string
}

// NewLLMPlanner 用 Registry 里**所有工具**作为模型菜单（M1：工具少，先全量喂；
// FR7：将来工具多了再做按目标筛选）。
func NewLLMPlanner(provider *model.OpenAICompatibleProvider, modelName, goal string, reg *tool.Registry) *LLMPlanner {
	return &LLMPlanner{
		Provider:  provider,
		Model:     modelName,
		Goal:      goal,
		MaxRounds: 10,
		registry:  reg,
		tools:     buildToolDefinitions(reg),
	}
}

const defaultSystemPrompt = `You are an autonomous agent that completes a goal by calling tools.

Iterate: examine prior tool results, call the next tool you need, observe its output, and continue.
When the goal is satisfied, STOP CALLING TOOLS — reply with a brief final message instead.
Never explain plans in prose while there are tool calls to make: emit the tool calls.
The user message is the goal.`

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

	// 2) 首轮注入 system + user。
	if p.round == 1 {
		sys := p.System
		if sys == "" {
			sys = defaultSystemPrompt
		}
		p.messages = append(p.messages,
			model.Message{Role: "system", Content: sys},
			model.Message{Role: "user", Content: p.Goal},
		)
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

	// 5) 没调工具 ⇒ LLM 决定完成。
	if len(resp.ToolCalls) == 0 {
		p.done = true
		return nil, true, nil
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

// buildToolDefinitions 把 tool.Registry 转成 OpenAI tool_calls 兼容的 schema。
// M1：用现有 DataSchema 直接映射；M3 升级到 MCP/JSON Schema 后这里换实现即可，
// LLMPlanner 其它部分不动（FR7）。
func buildToolDefinitions(reg *tool.Registry) []model.ToolDefinition {
	tools := reg.List()
	defs := make([]model.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, model.ToolDefinition{
			Type: "function",
			Function: model.FunctionSchema{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  toolParameters(t),
			},
		})
	}
	return defs
}

// toolParameters 优先用工具的原生 JSON Schema（MCP 工具通过 RawInputSchema 直供，
// 见 mcp.ToolAdapter）——避免 JSON Schema → DataSchema → JSON Schema 的有损往返。
// 没有原生 schema 的本地工具退回到 DataSchema 映射。
func toolParameters(t tool.Tool) map[string]any {
	if rs, ok := t.(interface{ RawInputSchema() json.RawMessage }); ok {
		if raw := rs.RawInputSchema(); len(raw) > 0 {
			var params map[string]any
			if json.Unmarshal(raw, &params) == nil && len(params) > 0 {
				return params
			}
		}
	}
	return dataSchemaToJSONSchema(t.InputSchema())
}

func dataSchemaToJSONSchema(ds tool.DataSchema) map[string]any {
	props := map[string]any{}
	var required []string
	for name, f := range ds.Fields {
		props[name] = map[string]any{
			"type":        normalizeJSONType(f.Type),
			"description": f.Desc,
		}
		if f.Required {
			required = append(required, name)
		}
	}
	out := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func normalizeJSONType(t string) string {
	switch t {
	case "bool", "boolean":
		return "boolean"
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "array":
		return "array"
	case "object":
		return "object"
	default:
		return "string"
	}
}
