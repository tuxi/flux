package planner

// Hermetic 回归测试（无 LLM/无网络）：DAGPlanner.Generate 的对话历史必须始终满足
// OpenAI 语义——带 tool_calls 的 assistant 消息后，每个 tool_call_id 都要有对应的
// tool 响应消息。否则严格的 provider（deepseek 等）在 repair 轮会返回 400：
// "An assistant message with 'tool_calls' must be followed by tool messages ...".
//
// 触发场景（小/快规划模型常见）：模型第一轮调了 submit_plan 之外的工具。

import (
	"context"
	"testing"

	"github.com/tuxi/flux/model"
)

// scriptedCompleter 按预设顺序返回响应，并在**每次**收到请求时校验历史不变量。
type scriptedCompleter struct {
	t         *testing.T
	responses []model.Response
	n         int
}

func (s *scriptedCompleter) Complete(_ context.Context, req model.Request) (model.Response, error) {
	assertToolCallsAnswered(s.t, req.Messages)
	if s.n >= len(s.responses) {
		s.t.Fatalf("Complete called more than scripted (%d)", len(s.responses))
	}
	r := s.responses[s.n]
	s.n++
	return r, nil
}

// assertToolCallsAnswered 校验：每个 assistant 消息里的 tool_call.ID 都能在某条 tool
// 消息的 ToolCallID 中找到响应。
func assertToolCallsAnswered(t *testing.T, msgs []model.Message) {
	t.Helper()
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if !answered[tc.ID] {
				t.Fatalf("invalid history: assistant tool_call %q has no following tool response (would 400 on strict providers)", tc.ID)
			}
		}
	}
}

func wrongToolResp() model.Response {
	return model.Response{ToolCalls: []model.ToolCall{{
		ID: "call_wrong_1", Type: "function",
		Function: model.FunctionCall{Name: "search", Arguments: "{}"},
	}}}
}

func submitPlanResp() model.Response {
	return model.Response{ToolCalls: []model.ToolCall{{
		ID: "call_submit_1", Type: "function",
		Function: model.FunctionCall{
			Name:      "submit_plan",
			Arguments: `{"nodes":[{"id":"w","tool":"write_file","arguments":{"path":"a.txt","content":"hi"}}],"result_type":"generic"}`,
		},
	}}}
}

// 第一轮调错工具 → 第二轮正确 submit_plan：历史不变量必须始终成立，且最终生成有效计划。
func TestGenerate_WrongToolThenSubmit_HistoryValid(t *testing.T) {
	reg := dagTestReg(t.TempDir())
	fake := &scriptedCompleter{t: t, responses: []model.Response{wrongToolResp(), submitPlanResp()}}

	p := NewDAGPlanner(fake, "test-model", "write a file", reg)
	plan, err := p.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if plan == nil || len(plan.Nodes) != 1 {
		t.Fatalf("expected 1-node plan, got %+v", plan)
	}
	if fake.n != 2 {
		t.Fatalf("expected 2 LLM calls (1 repair), got %d", fake.n)
	}
}

// submit_plan 与另一个工具被同时调用：兄弟 tool_call 也必须被响应（否则下一轮 400）。
func TestGenerate_SubmitPlusSiblingToolCall_HistoryValid(t *testing.T) {
	reg := dagTestReg(t.TempDir())
	// 第一轮：submit_plan（参数非法 JSON，强制进入 repair）+ 一个兄弟工具调用。
	badSubmitPlusSibling := model.Response{ToolCalls: []model.ToolCall{
		{ID: "call_submit_bad", Type: "function", Function: model.FunctionCall{Name: "submit_plan", Arguments: "{not json"}},
		{ID: "call_sibling", Type: "function", Function: model.FunctionCall{Name: "search", Arguments: "{}"}},
	}}
	fake := &scriptedCompleter{t: t, responses: []model.Response{badSubmitPlusSibling, submitPlanResp()}}

	p := NewDAGPlanner(fake, "test-model", "write a file", reg)
	if _, err := p.Generate(context.Background()); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if fake.n != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", fake.n)
	}
}
