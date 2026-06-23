package engine

import (
	"testing"

	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
	"flux/workflow/nodes"
)

// fanInDef 构造一个典型的 fan-in 拓扑：
//
//	┌────────────┐
//	│ left_branch│──┐
//	└────────────┘  │
//	                ▼
//	           ┌────────┐
//	           │ target │
//	           └────────┘
//	                ▲
//	┌─────────────┐ │
//	│right_branch │─┘
//	└─────────────┘
//
// 复刻用户报告里 build_blueprint_segments 的两条入边场景。
func fanInDef() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		Name: "fan_in",
		Nodes: []definition.NodeDefinition{
			{Name: "left_branch", Type: definition.NodeTool},
			{Name: "right_branch", Type: definition.NodeTool},
			{Name: "target", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "left_branch", To: "target", Type: definition.EdgeNormal},
			{From: "right_branch", To: "target", Type: definition.EdgeNormal},
		},
	}
}

func newFanInFixture(t *testing.T) (*nodes.Context, *graph.Graph) {
	t.Helper()
	def := fanInDef()
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph build: %v", err)
	}
	ctx := &nodes.Context{
		Workflow:       def,
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		Output:         map[string]any{},
	}
	return ctx, g
}

// TestDepsMet_WaitsForAllParents_RegardlessOfStaleEdgeFlag 直接覆盖用户报告的 bug。
//
// 场景：left_branch 已成功（edge=true），right_branch 还 pending；但 ctx.ActivatedEdges
// 里残留了一条过期的 right_branch->target=false（来自之前的失败 closure / skipSubtree）。
// 旧实现会因为 edge=false 走 continue，错误地认为依赖已满足。
// 修复后必须返回 false：right_branch 还没 terminal，绝不允许触发下游。
func TestDepsMet_WaitsForAllParents_RegardlessOfStaleEdgeFlag(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeSuccess}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodePending}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	// left_branch 正常完成，自然写入 true
	ctx.ActivatedEdges["left_branch->target"] = true
	// 过期 false edge —— 历史污染源
	ctx.ActivatedEdges["right_branch->target"] = false

	if e.depsMet(ctx, "target", g) {
		t.Fatal("depsMet should be false: right_branch is still pending; stale false edge must not satisfy fan-in")
	}
}

// TestDepsMet_NoPanicOnUndecidedEdge 旧实现遇到未决定边会 panic 直接打挂 worker。
// 修复后改为 return false 安全兜底。
func TestDepsMet_NoPanicOnUndecidedEdge(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	// left_branch 已 terminal，但 ActivatedEdges 里完全没记录这条边（异常状态）
	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeSuccess}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("depsMet panicked on undecided edge: %v", r)
		}
	}()

	if e.depsMet(ctx, "target", g) {
		t.Fatal("depsMet should be false: edges are not yet decided")
	}
}

// TestDepsMet_AllParentsSuccessActivated 正常 join 通过路径。
func TestDepsMet_AllParentsSuccessActivated(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeSuccess}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	ctx.ActivatedEdges["left_branch->target"] = true
	ctx.ActivatedEdges["right_branch->target"] = true

	if !e.depsMet(ctx, "target", g) {
		t.Fatal("depsMet should be true: all parents Success and edges activated")
	}
}

// TestDepsMet_InactiveConditionalBranchIsSkipped 条件分支被关闭（边=false）且 parent 已 Success 时，
// 这条边正确地不参与 join。如 parent 是 Skipped/Failed（非 Success），则为 blocked。
func TestDepsMet_InactiveConditionalBranchIsSkipped(t *testing.T) {
	// 使用条件边 fixture：条件未命中 → inactive → 从 join 剔除。
	def := &definition.WorkflowDefinition{
		Name: "cond_branch",
		Nodes: []definition.NodeDefinition{
			{Name: "cond_parent", Type: definition.NodeTool},
			{Name: "normal_parent", Type: definition.NodeTool},
			{Name: "target", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "cond_parent", To: "target", Type: definition.EdgeCondition, Condition: "cond_parent.take == true"},
			{From: "normal_parent", To: "target", Type: definition.EdgeNormal},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph build: %v", err)
	}
	ctx := &nodes.Context{
		Workflow:       def,
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		Output:         map[string]any{},
	}
	e := &Engine{}

	// cond_parent 成功但条件不满足 → edge=false（inactive，正确从 join 剔除）
	ctx.Runtime["cond_parent"] = &domain.NodeRuntime{Name: "cond_parent", State: domain.NodeSuccess}
	ctx.Runtime["normal_parent"] = &domain.NodeRuntime{Name: "normal_parent", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	ctx.ActivatedEdges["cond_parent->target"] = false  // inactive: 条件未命中
	ctx.ActivatedEdges["normal_parent->target"] = true // active

	if !e.depsMet(ctx, "target", g) {
		t.Fatal("depsMet should be true: only the active branch needs to be Success, inactive condition edge is correctly excluded")
	}

	// P0 修复：EdgeNormal parent Skipped 是 blocked，不允许 join 通过。
	// 旧 bug：会把 blocked 误判为 inactive。
	t.Run("EdgeNormal parent skipped blocks join", func(t *testing.T) {
		ctx2, g2 := newFanInFixture(t)
		ctx2.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeSkipped}
		ctx2.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
		ctx2.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}
		// P0 语义：EdgeNormal + parent Skipped → activated=true（必选路径，但父死）
		ctx2.ActivatedEdges["left_branch->target"] = true
		ctx2.ActivatedEdges["right_branch->target"] = true
		if e.depsMet(ctx2, "target", g2) {
			t.Fatal("depsMet must be false: EdgeNormal parent Skipped = blocked, join must not pass")
		}
	})
}

// TestDepsMet_ActivatedParentNotSuccess parent 是 Failed / Skipped 等非 Success 终态时，
// 不允许触发下游（即使 edge=true）。
func TestDepsMet_ActivatedParentNotSuccess(t *testing.T) {
	cases := []struct {
		name  string
		state domain.NodeState
	}{
		{"failed", domain.NodeFailed},
		{"skipped", domain.NodeSkipped},
		{"canceled", domain.NodeCanceled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, g := newFanInFixture(t)
			e := &Engine{}

			ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: tc.state}
			ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
			ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

			ctx.ActivatedEdges["left_branch->target"] = true
			ctx.ActivatedEdges["right_branch->target"] = true

			if e.depsMet(ctx, "target", g) {
				t.Fatalf("depsMet should be false: parent state=%s with activated edge must block join", tc.state)
			}
		})
	}
}

// TestDepsMet_RunningParentBlocksFanInEvenIfEdgeAlreadyTrue 重执行场景：parent 之前成功过、edge=true 已写入，
// 但 parent 这次被 reset 回 Running/Pending（fork、重试），下游必须重新等待。
func TestDepsMet_RunningParentBlocksFanInEvenIfEdgeAlreadyTrue(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeRunning}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	ctx.ActivatedEdges["left_branch->target"] = true
	ctx.ActivatedEdges["right_branch->target"] = true

	if e.depsMet(ctx, "target", g) {
		t.Fatal("depsMet should be false: a non-terminal parent must block downstream even if its old edge is still true")
	}
}

// TestClearOutgoingActivatedEdges_RemovesAllOutgoing
// Fix 2 验证：节点 reset 时，所有 outgoing edge 从 ctx 中被清掉，其它边不受影响。
func TestClearOutgoingActivatedEdges_RemovesAllOutgoing(t *testing.T) {
	ctx, _ := newFanInFixture(t)
	e := &Engine{}

	ctx.ActivatedEdges["left_branch->target"] = true
	ctx.ActivatedEdges["right_branch->target"] = false
	ctx.ActivatedEdges["unrelated->other"] = true

	e.clearOutgoingActivatedEdges(ctx, "right_branch")

	if _, ok := ctx.ActivatedEdges["right_branch->target"]; ok {
		t.Fatal("right_branch->target should have been removed")
	}
	if v, ok := ctx.ActivatedEdges["left_branch->target"]; !ok || v != true {
		t.Fatal("left_branch->target must remain intact")
	}
	if v, ok := ctx.ActivatedEdges["unrelated->other"]; !ok || v != true {
		t.Fatal("unrelated->other must remain intact")
	}
}

// TestRebuildActivatedEdges_DropsStaleFalseEdgeWhenSourceNotTerminal
// Fix 3 验证：rebuild 阶段，来源节点当前不是 terminal 时，false edge 视为过期并丢弃。
func TestRebuildActivatedEdges_DropsStaleFalseEdgeWhenSourceNotTerminal(t *testing.T) {
	ctx, _ := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{
		Name:           "left_branch",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"left_branch->target": true},
	}
	// right_branch 当前是 pending（被 reset 重跑），但 DB 里残留了一条 false edge
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{
		Name:           "right_branch",
		State:          domain.NodePending,
		ActivatedEdges: map[string]bool{"right_branch->target": false},
	}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	e.rebuildActivatedEdges(ctx)

	if _, ok := ctx.ActivatedEdges["right_branch->target"]; ok {
		t.Fatal("stale false edge from non-terminal source must be dropped during rebuild")
	}
	if v, ok := ctx.ActivatedEdges["left_branch->target"]; !ok || v != true {
		t.Fatal("true edge from terminal source must be preserved")
	}
}

// TestRebuildActivatedEdges_KeepsFalseEdgeFromTerminalSource
// Fix 3 边界：合法的 false（条件分支判为 false，源节点真的 terminal）必须保留。
func TestRebuildActivatedEdges_KeepsFalseEdgeFromTerminalSource(t *testing.T) {
	ctx, _ := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{
		Name:           "left_branch",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"left_branch->target": false}, // 条件分支显式 false
	}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodePending}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	e.rebuildActivatedEdges(ctx)

	v, ok := ctx.ActivatedEdges["left_branch->target"]
	if !ok {
		t.Fatal("legitimate false edge from terminal source must survive rebuild")
	}
	if v != false {
		t.Fatal("value should remain false")
	}
}

// TestFanInJoin_StaleFalseEdgeFromReExecutedParent end-to-end 集成式回归：
// 完整复刻用户报告的 reset+rebuild+depsMet 链路。
//
//  1. right_branch 第一次执行失败 → finalizeNode 把 right_branch->target 持久化为 false
//  2. 任务被重试，right_branch 通过 prepareDirtyRuntime 被 reset 回 pending
//  3. 重启/恢复时 rebuildActivatedEdges 读取 DB
//  4. left_branch 完成，写入 left_branch->target=true
//  5. 检查 depsMet：必须 false（right_branch 还在 pending，不能让 target 提前跑）
func TestFanInJoin_StaleFalseEdgeFromReExecutedParent(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	// 模拟 reset 后的 right_branch：state=Pending，ActivatedEdges 在 prepareDirtyRuntime 里已被清空
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{
		Name:           "right_branch",
		State:          domain.NodePending,
		ActivatedEdges: map[string]bool{},
	}
	// left_branch 在新一轮成功
	ctx.Runtime["left_branch"] = &domain.NodeRuntime{
		Name:           "left_branch",
		State:          domain.NodeSuccess,
		ActivatedEdges: map[string]bool{"left_branch->target": true},
	}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	// 模拟：上一轮 failClosure 写到 ctx 里的过期 false 边
	// （应该被 prepareDirtyRuntime → clearOutgoingActivatedEdges 清掉；这里手动塞入，
	//  额外验证 depsMet 自身也能挡住这种过期记录）
	ctx.ActivatedEdges["right_branch->target"] = false

	// rebuild 从 runtime 读
	e.rebuildActivatedEdges(ctx)

	// 注意：rebuild 之后 ctx 里 right_branch->target 还可能保留之前手塞的 false，
	// 这正是为什么 depsMet 自身必须做 parent-terminal 兜底
	if e.depsMet(ctx, "target", g) {
		t.Fatal("target must not be triggered: right_branch still pending; this is the exact bug from task 2059106682667024384")
	}

	// 模拟：right_branch 这一次执行真正完成
	ctx.Runtime["right_branch"].State = domain.NodeSuccess
	ctx.ActivatedEdges["right_branch->target"] = true

	if !e.depsMet(ctx, "target", g) {
		t.Fatal("target should be triggered now: both parents Success and edges true")
	}
}
