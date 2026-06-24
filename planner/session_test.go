package planner_test

// 会话持久化的 hermetic 证明（无 LLM）：续接时已载入的历史被保留，新目标作为新 user
// 追加，且不重发 system；History() 能完整导出供存盘。

import (
	"context"
	"testing"

	"flux/model"
	"flux/planner"
	"flux/runtime"
	"flux/tool"
	"flux/tool/builtin"
)

func TestSession_ResumePreservesHistoryAndAppendsGoal(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())

	// 模拟"上一次会话"留下的历史。
	prior := []model.Message{
		{Role: "system", Content: "SYS"},
		{Role: "user", Content: "第一次的目标"},
		{Role: "assistant", Content: "第一次的回答"},
	}

	// 这一轮 LLM 直接给最终答案（不调工具）→ 正常完成。
	fc := &fakeCompleter{responses: []model.Response{{Content: "续接后的回答"}}}
	p := planner.NewLLMPlanner(fc, "m", "第二次的目标", reg)
	p.SetHistory(prior)

	sched := runtime.NewScheduler(planner.NewToolInvoker(reg), planner.NopAwait{},
		planner.NopStore{}, planner.NopEmitter{}).WithMaxSteps(10)
	res, err := sched.Run(context.Background(), p, runtime.NewMemState(nil))
	if err != nil || res.Status != runtime.StatusCompleted {
		t.Fatalf("run: status=%d err=%v", res.Status, err)
	}

	h := p.History()
	// 旧历史原样在前
	if len(h) < 4 || h[0].Content != "SYS" || h[1].Content != "第一次的目标" || h[2].Content != "第一次的回答" {
		t.Fatalf("旧历史未被保留: %+v", h)
	}
	// 只应有一条 system（没重发）
	sysCount := 0
	var sawNewGoal bool
	for _, m := range h {
		if m.Role == "system" {
			sysCount++
		}
		if m.Role == "user" && m.Content == "第二次的目标" {
			sawNewGoal = true
		}
	}
	if sysCount != 1 {
		t.Fatalf("续接不应重发 system，得 %d 条", sysCount)
	}
	if !sawNewGoal {
		t.Fatalf("新目标应作为新 user 消息追加")
	}
	t.Logf("✅ 续接：旧历史保留 + 新目标追加 + system 不重发（共 %d 条消息）", len(h))
}
