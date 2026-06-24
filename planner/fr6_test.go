package planner_test

// FR6 hermetic 证明（无 LLM）：用假的 model.Completer 脚本化 LLM 回复，验证
//   1. give_up 退出阀：LLM 判定不可达 → GaveUpError（带原因）；
//   2. 无进展安全网：同一动作重复到上限 → GaveUpError（卡死兜底）。
//
// 这正是上一次空仓库实跑暴露的缺口（agent 空转 20 轮）被钉死的地方。

import (
	"context"
	"errors"
	"testing"

	"flux/model"
	"flux/planner"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

// fakeCompleter 按脚本依次返回预设回复；用完后返回空回复（= LLM 不再调工具 = 完成）。
type fakeCompleter struct {
	responses []model.Response
	i         int
}

func (f *fakeCompleter) Complete(context.Context, model.Request) (model.Response, error) {
	if f.i >= len(f.responses) {
		return model.Response{}, nil
	}
	r := f.responses[f.i]
	f.i++
	return r, nil
}

func callResp(name, args string) model.Response {
	return model.Response{ToolCalls: []model.ToolCall{
		{ID: "c", Type: "function", Function: model.FunctionCall{Name: name, Arguments: args}},
	}}
}

func runPlanner(p *planner.LLMPlanner, reg *tool.Registry) (runtime.Result, error) {
	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).WithMaxSteps(50)
	return sched.Run(context.Background(), p, runtime.NewMemState(nil))
}

func TestFR6_GiveUp(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())

	// LLM 第一轮就 give_up。
	fc := &fakeCompleter{responses: []model.Response{
		callResp("give_up", `{"reason":"目录里没有失败的测试，前提不成立"}`),
	}}
	p := planner.NewLLMPlanner(fc, "m", "修复失败的测试", reg)

	_, err := runPlanner(p, reg)
	var gu *planner.GaveUpError
	if !errors.As(err, &gu) {
		t.Fatalf("应返回 GaveUpError，得 %v", err)
	}
	if gu.Reason == "" || !contains(gu.Reason, "前提不成立") {
		t.Fatalf("应带原因，得 %q", gu.Reason)
	}
	t.Logf("✅ give_up 退出阀：%s", gu.Reason)
}

func TestFR6_NoProgress(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())

	// LLM 一直重复同一个动作（模拟卡死打转），从不 give_up。
	same := callResp("merge_result", `{"x":1}`)
	fc := &fakeCompleter{responses: []model.Response{same, same, same, same, same, same, same}}
	p := planner.NewLLMPlanner(fc, "m", "goal", reg)
	p.NoProgressLimit = 4 // 重复 4 次即判定无进展

	_, err := runPlanner(p, reg)
	var gu *planner.GaveUpError
	if !errors.As(err, &gu) {
		t.Fatalf("应因无进展返回 GaveUpError，得 %v", err)
	}
	if !contains(gu.Reason, "无进展") {
		t.Fatalf("原因应提示无进展，得 %q", gu.Reason)
	}
	t.Logf("✅ 无进展安全网：%s", gu.Reason)
}

func TestFR6_NormalCompletionUnaffected(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())

	// 一次工具调用后 LLM 停手（空回复）→ 正常完成，不应误触发 FR6。
	fc := &fakeCompleter{responses: []model.Response{
		callResp("merge_result", `{"x":1}`),
	}}
	p := planner.NewLLMPlanner(fc, "m", "goal", reg)

	res, err := runPlanner(p, reg)
	if err != nil {
		t.Fatalf("正常完成不应有错误: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("应正常 Completed，得 %d", res.Status)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
