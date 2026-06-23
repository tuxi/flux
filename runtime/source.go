package runtime

import "context"

// PlanSource 是底座的"计划来源"。这是让 workflow 与 planner 可互换的关键抽象：
//   - 静态 workflow：首次返回整张 DAG，done=true（退化情形）
//   - planner/agent：每次根据已完成结果返回下一步，done 由 LLM 判定（增量规划）
type PlanSource interface {
	// Next 返回新增（追加到计划中）的节点。底座负责依赖求解与调度，
	// 因此 Source 只需"产出节点"，不需要自己算就绪顺序。
	Next(ctx context.Context, state ExecState) (added []*PlanNode, done bool, err error)
}

// StaticSource 把一张完整的 Plan 包成"一次性给完"的 Source —— 这就是让
// 老 workflow 路径在新底座上原样跑通的垫片（shim）。
//
// 用法：plan := workflow.Compile(def);  scheduler.Run(ctx, runtime.NewStaticSource(plan), state)
type StaticSource struct {
	plan    *Plan
	emitted bool
}

func NewStaticSource(plan *Plan) *StaticSource { return &StaticSource{plan: plan} }

func (s *StaticSource) Next(_ context.Context, _ ExecState) ([]*PlanNode, bool, error) {
	if s.emitted {
		return nil, true, nil
	}
	s.emitted = true
	out := make([]*PlanNode, 0, len(s.plan.Nodes))
	for _, n := range s.plan.Nodes {
		out = append(out, n)
	}
	return out, true, nil // 整图一次性给出，done=true
}
