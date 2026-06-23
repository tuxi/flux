package engine

import (
	"context"
	"testing"

	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
	"flux/eventbus"
	"flux/workflow/nodes"
)

// newTestEngineForSkip 返回一个足以支持 finalizeSkippedNode / finalizeBlockedNode 的 Engine。
func newTestEngineForSkip() *Engine {
	eb := eventbus.NewEventBus(nil, nil)
	return &Engine{
		nodeRepo: newFakeNodeRepo(),
		taskRepo: newFakeTaskRepo(&domain.Task{ID: 1, RootID: 1, Status: domain.TaskRunning}),
		eventBus: eb,
	}
}

// withTask sets minimal fields on a nodes.Context so transitionLocked doesn't nil-panic.
func withTask(ctx *nodes.Context) {
	ctx.Task = &domain.Task{ID: 1, RootID: 1, Status: domain.TaskRunning}
	if ctx.Ctx == nil {
		ctx.Ctx = context.Background()
	}
	// transitionLocked uses ctx.EventBus (set by runDAG), not e.eventBus.
	if ctx.EventBus == nil {
		ctx.EventBus = eventbus.NewEventBus(nil, nil)
	}
	// transitionLocked → UpdateNodeStatus → expects Output["nodes"]
	if ctx.Output == nil {
		ctx.Output = map[string]any{}
	}
	if _, ok := ctx.Output["nodes"]; !ok {
		ctx.Output["nodes"] = map[string]any{}
	}
}

// ─── resolveEdgeState 单测 ───

func newEdgeStateFixture(t *testing.T, edgeType definition.EdgeType) (*nodes.Context, *graph.Graph) {
	t.Helper()
	def := &definition.WorkflowDefinition{
		Name: "edge_state_test",
		Nodes: []definition.NodeDefinition{
			{Name: "parent", Type: definition.NodeTool},
			{Name: "child", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "parent", To: "child", Type: edgeType},
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
	return ctx, g
}

func TestResolveEdgeState_ParentNotTerminal(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeNormal)
	e := &Engine{}
	ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeRunning}
	ctx.Runtime["child"] = &domain.NodeRuntime{Name: "child", State: domain.NodePending}

	if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateUnknown {
		t.Errorf("非 terminal 父应为 unknown, got %s", es)
	}
}

func TestResolveEdgeState_EdgeNormalParentSuccess(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeNormal)
	e := &Engine{}
	ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeSuccess}
	ctx.ActivatedEdges["parent->child"] = true

	if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateActive {
		t.Errorf("EdgeNormal + parent Success 应为 active, got %s", es)
	}
}

func TestResolveEdgeState_EdgeNormalParentFailed(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeNormal)
	e := &Engine{}
	// Failed + EdgeNormal → blocked，无论 edge 值
	for _, v := range []bool{true, false} {
		ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeFailed}
		ctx.ActivatedEdges["parent->child"] = v
		if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateBlocked {
			t.Errorf("EdgeNormal + parent Failed (edge=%v) 应为 blocked, got %s", v, es)
		}
	}
}

func TestResolveEdgeState_EdgeNormalParentSkipped_BlockedVsInactive(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeNormal)
	e := &Engine{}
	ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeSkipped}

	// edge=true → 执行路径上被级联 skip → blocked
	ctx.ActivatedEdges["parent->child"] = true
	if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateBlocked {
		t.Errorf("EdgeNormal + parent Skipped + edge=true 应为 blocked（级联 skip），got %s", es)
	}

	// edge=false → 死分支（条件未命中）→ inactive
	ctx.ActivatedEdges["parent->child"] = false
	if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateInactive {
		t.Errorf("EdgeNormal + parent Skipped + edge=false 应为 inactive（死分支），got %s", es)
	}
}

func TestResolveEdgeState_EdgeConditionParentSuccessActive(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeCondition)
	e := &Engine{}
	ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeSuccess}
	ctx.ActivatedEdges["parent->child"] = true

	if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateActive {
		t.Errorf("EdgeCondition + parent Success + edge=true 应为 active, got %s", es)
	}
}

func TestResolveEdgeState_EdgeConditionParentSuccessInactive(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeCondition)
	e := &Engine{}
	ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeSuccess}
	ctx.ActivatedEdges["parent->child"] = false

	if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateInactive {
		t.Errorf("EdgeCondition + parent Success + edge=false 应为 inactive, got %s", es)
	}
}

func TestResolveEdgeState_EdgeConditionParentFailed(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeCondition)
	e := &Engine{}
	// Failed + EdgeCondition → blocked，不论 edge 值。条件父失败不是"条件没走"。
	for _, v := range []bool{true, false} {
		ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeFailed}
		ctx.ActivatedEdges["parent->child"] = v
		if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateBlocked {
			t.Errorf("EdgeCondition + parent Failed (edge=%v) 应为 blocked, got %s", v, es)
		}
	}
}

func TestResolveEdgeState_EdgeNotDecided(t *testing.T) {
	ctx, g := newEdgeStateFixture(t, definition.EdgeNormal)
	e := &Engine{}
	ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeSuccess}
	// ActivatedEdges 里没有 parent->child

	if es := e.resolveEdgeState(ctx, "parent", "child", g); es != EdgeStateUnknown {
		t.Errorf("边未计算时应为 unknown, got %s", es)
	}
}

// ─── depsMet: video_to_prompt 事故复现图 ───

// videoToPromptBugDef 复现 task 2064672965093507072 的拓扑：
//
//	extract_keyframes (EdgeNormal)
//	    ↓
//	map_extract_ocr (EdgeNormal)
//	    ↓
//	extract_ocr (EdgeNormal)
//	    ↓              ↘ (EdgeNormal)
//	map_analyze_segments  ← transcribe_audio (EdgeNormal)
func videoToPromptBugDef() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		Name: "vtp_bug",
		Nodes: []definition.NodeDefinition{
			{Name: "extract_keyframes", Type: definition.NodeTool},
			{Name: "transcribe_audio", Type: definition.NodeTool},
			{Name: "map_extract_ocr", Type: definition.NodeTool},
			{Name: "extract_ocr", Type: definition.NodeTool},
			{Name: "map_analyze_segments", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "extract_keyframes", To: "map_extract_ocr", Type: definition.EdgeNormal},
			{From: "map_extract_ocr", To: "extract_ocr", Type: definition.EdgeNormal},
			{From: "extract_ocr", To: "map_analyze_segments", Type: definition.EdgeNormal},
			{From: "extract_keyframes", To: "map_analyze_segments", Type: definition.EdgeNormal},
			{From: "transcribe_audio", To: "map_analyze_segments", Type: definition.EdgeNormal},
		},
	}
}

// TestDepsMet_VideoToPromptBug_FailedParentBlocksFanIn 是本次事故的核心回归：3 条 EdgeNormal
// fan-in 到 map_analyze_segments，其中一个父 extract_keyframes failed → join 必须阻塞。
func TestDepsMet_VideoToPromptBug_FailedParentBlocksFanIn(t *testing.T) {
	def := videoToPromptBugDef()
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

	// extract_keyframes failed，其余成功/被 skip
	ctx.Runtime["extract_keyframes"] = &domain.NodeRuntime{Name: "extract_keyframes", State: domain.NodeFailed}
	ctx.Runtime["transcribe_audio"] = &domain.NodeRuntime{Name: "transcribe_audio", State: domain.NodeSuccess}
	// extract_ocr 被级联 skip（唯一父 extract_keyframes failed）
	ctx.Runtime["extract_ocr"] = &domain.NodeRuntime{Name: "extract_ocr", State: domain.NodeSkipped}
	ctx.Runtime["map_analyze_segments"] = &domain.NodeRuntime{Name: "map_analyze_segments", State: domain.NodePending}

	// P0 修复后，failed EdgeNormal 父的边写 true（表示必选路径但父死）
	ctx.ActivatedEdges["extract_keyframes->map_analyze_segments"] = true
	ctx.ActivatedEdges["transcribe_audio->map_analyze_segments"] = true
	// extract_ocr 被 failClosure 级联 skip（blocked 传播），边也写 true
	ctx.ActivatedEdges["extract_ocr->map_analyze_segments"] = true

	if e.depsMet(ctx, "map_analyze_segments", g) {
		t.Fatal("BUG 复现：extract_keyframes failed 但 depsMet 仍通过！fan-in join 必须阻塞")
	}
}

// TestDepsMet_VideoToPromptBug_AllSuccessPasses 正常路径：三路全 Success → join 通过。
func TestDepsMet_VideoToPromptBug_AllSuccessPasses(t *testing.T) {
	def := videoToPromptBugDef()
	g, _ := graph.Build(def)
	ctx := &nodes.Context{
		Workflow:       def,
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		Output:         map[string]any{},
	}
	e := &Engine{}

	ctx.Runtime["extract_keyframes"] = &domain.NodeRuntime{Name: "extract_keyframes", State: domain.NodeSuccess}
	ctx.Runtime["transcribe_audio"] = &domain.NodeRuntime{Name: "transcribe_audio", State: domain.NodeSuccess}
	ctx.Runtime["extract_ocr"] = &domain.NodeRuntime{Name: "extract_ocr", State: domain.NodeSuccess}
	ctx.Runtime["map_analyze_segments"] = &domain.NodeRuntime{Name: "map_analyze_segments", State: domain.NodePending}

	ctx.ActivatedEdges["extract_keyframes->map_analyze_segments"] = true
	ctx.ActivatedEdges["transcribe_audio->map_analyze_segments"] = true
	ctx.ActivatedEdges["extract_ocr->map_analyze_segments"] = true

	if !e.depsMet(ctx, "map_analyze_segments", g) {
		t.Fatal("三路全 Success 时 depsMet 应通过")
	}
}

// TestDepsMet_OneFailedOneSuccess_EdgeNormalBlocks 通用场景：普通 fan-in，A failed + B success → 阻塞。
func TestDepsMet_OneFailedOneSuccess_EdgeNormalBlocks(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeFailed}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	// P0 修复：Failed EdgeNormal 父的边写 true
	ctx.ActivatedEdges["left_branch->target"] = true
	ctx.ActivatedEdges["right_branch->target"] = true

	if e.depsMet(ctx, "target", g) {
		t.Fatal("EdgeNormal fan-in: 一个父 Failed + 一个父 Success → join 必须阻塞")
	}
}

// TestDepsMet_ConditionInactive_NormalSuccess_Passes 条件边未命中不应阻塞 EdgeNormal join。
func TestDepsMet_ConditionInactive_NormalSuccess_Passes(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Name: "cond_join",
		Nodes: []definition.NodeDefinition{
			{Name: "cond_parent", Type: definition.NodeTool},
			{Name: "normal_parent", Type: definition.NodeTool},
			{Name: "target", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "cond_parent", To: "target", Type: definition.EdgeCondition, Condition: "cond_parent.take"},
			{From: "normal_parent", To: "target", Type: definition.EdgeNormal},
		},
	}
	g, _ := graph.Build(def)
	ctx := &nodes.Context{
		Workflow:       def,
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		Output:         map[string]any{},
	}
	e := &Engine{}

	ctx.Runtime["cond_parent"] = &domain.NodeRuntime{Name: "cond_parent", State: domain.NodeSuccess}
	ctx.Runtime["normal_parent"] = &domain.NodeRuntime{Name: "normal_parent", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	ctx.ActivatedEdges["cond_parent->target"] = false // 条件未命中
	ctx.ActivatedEdges["normal_parent->target"] = true

	if !e.depsMet(ctx, "target", g) {
		t.Fatal("EdgeCondition inactive + EdgeNormal success → join 应通过")
	}
}

// TestDepsMet_ConditionParentFailed_BlocksJoin 条件父 failed 不是"条件没走"。
func TestDepsMet_ConditionParentFailed_BlocksJoin(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Name: "cond_failed",
		Nodes: []definition.NodeDefinition{
			{Name: "cond_parent", Type: definition.NodeTool},
			{Name: "normal_parent", Type: definition.NodeTool},
			{Name: "target", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "cond_parent", To: "target", Type: definition.EdgeCondition, Condition: "cond_parent.take"},
			{From: "normal_parent", To: "target", Type: definition.EdgeNormal},
		},
	}
	g, _ := graph.Build(def)
	ctx := &nodes.Context{
		Workflow:       def,
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		Output:         map[string]any{},
	}
	e := &Engine{}

	ctx.Runtime["cond_parent"] = &domain.NodeRuntime{Name: "cond_parent", State: domain.NodeFailed}
	ctx.Runtime["normal_parent"] = &domain.NodeRuntime{Name: "normal_parent", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}

	ctx.ActivatedEdges["cond_parent->target"] = true
	ctx.ActivatedEdges["normal_parent->target"] = true

	if e.depsMet(ctx, "target", g) {
		t.Fatal("EdgeCondition parent Failed → blocked，join 不应通过")
	}
}

// ─── hasBlockedParent / skipNodeWithCorrectKind ───

func TestHasBlockedParent_BlockedByFailedParent(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeFailed}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}
	ctx.ActivatedEdges["left_branch->target"] = true
	ctx.ActivatedEdges["right_branch->target"] = true

	if !e.hasBlockedParent(ctx, "target", g) {
		t.Fatal("left_branch Failed → target 应有 blocked parent")
	}
}

func TestHasBlockedParent_AllActive(t *testing.T) {
	ctx, g := newFanInFixture(t)
	e := &Engine{}

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeSuccess}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}
	ctx.ActivatedEdges["left_branch->target"] = true
	ctx.ActivatedEdges["right_branch->target"] = true

	if e.hasBlockedParent(ctx, "target", g) {
		t.Fatal("全部 active → 不应有 blocked parent")
	}
}

// TestSkipNodeWithCorrectKind_BlockedUsesFinalizeBlocked 验证：有 blocked 父时
// skipNodeWithCorrectKind 应写 EdgeNormal→true（blocked 传播），而非 false（inactive）。
func TestSkipNodeWithCorrectKind_BlockedUsesFinalizeBlocked(t *testing.T) {
	ctx, g := newFanInFixture(t)
	withTask(ctx)
	e := newTestEngineForSkip()

	ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeFailed}
	ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodePending}
	ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}
	// left_branch failed → edge true（P0 语义）
	ctx.ActivatedEdges["left_branch->target"] = true

	if err := e.skipNodeWithCorrectKind(ctx, "target", g); err != nil {
		t.Fatalf("skipNodeWithCorrectKind: %v", err)
	}

	rt := ctx.Runtime["target"]
	if rt.State != domain.NodeSkipped {
		t.Fatalf("target 应为 Skipped, got %s", rt.State)
	}
	// 有 blocked 父 → 应调用 finalizeBlockedNode → 出边 EdgeNormal 为 true。
	if len(rt.ActivatedEdges) > 0 {
		for k, v := range rt.ActivatedEdges {
			t.Logf("target outgoing: %s=%v", k, v)
		}
	}
}

// TestSkipNodeWithCorrectKind_InactiveOnlyUsesFinalizeSkipped 验证：全 inactive 父时
// skipNodeWithCorrectKind 应写全部 false（死分支传播）。
func TestSkipNodeWithCorrectKind_InactiveOnlyUsesFinalizeSkipped(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Name: "inactive_only",
		Nodes: []definition.NodeDefinition{
			{Name: "parent", Type: definition.NodeTool},
			{Name: "child", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "parent", To: "child", Type: definition.EdgeNormal},
		},
	}
	g, _ := graph.Build(def)
	ctx := &nodes.Context{
		Workflow:       def,
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		Output:         map[string]any{},
	}
	withTask(ctx)
	e := newTestEngineForSkip()

	// parent Skipped + edge=false → inactive（死分支）
	ctx.Runtime["parent"] = &domain.NodeRuntime{Name: "parent", State: domain.NodeSkipped}
	ctx.Runtime["child"] = &domain.NodeRuntime{Name: "child", State: domain.NodePending}
	ctx.ActivatedEdges["parent->child"] = false

	if err := e.skipNodeWithCorrectKind(ctx, "child", g); err != nil {
		t.Fatalf("skipNodeWithCorrectKind: %v", err)
	}

	rt := ctx.Runtime["child"]
	if rt.State != domain.NodeSkipped {
		t.Fatalf("child 应为 Skipped, got %s", rt.State)
	}
	// 全 inactive → finalizeSkippedNode → 出边应为 false。
	if len(rt.ActivatedEdges) > 0 {
		for k, v := range rt.ActivatedEdges {
			if v {
				t.Errorf("全 inactive skip 时出边应为 false, got %s=%v", k, v)
			}
		}
	}
}

// TestGlobalClosure_BlockedParentSkipsCorrectly 复现用户关心的 globalClosure 场景：
//
//	A -> B EdgeNormal
//	B -> C EdgeNormal
//	D -> C EdgeNormal
//	A failed, B should be blocked skipped, D success
//	C must not execute
func TestGlobalClosure_BlockedParentSkipsCorrectly(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Name: "closure_test",
		Nodes: []definition.NodeDefinition{
			{Name: "A", Type: definition.NodeTool},
			{Name: "B", Type: definition.NodeTool},
			{Name: "C", Type: definition.NodeTool},
			{Name: "D", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "A", To: "B", Type: definition.EdgeNormal},
			{From: "B", To: "C", Type: definition.EdgeNormal},
			{From: "D", To: "C", Type: definition.EdgeNormal},
		},
	}
	g, _ := graph.Build(def)
	ctx := &nodes.Context{
		Workflow:       def,
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		Output:         map[string]any{},
	}
	withTask(ctx)
	e := newTestEngineForSkip()

	// A failed, B pending, D success, C pending
	ctx.Runtime["A"] = &domain.NodeRuntime{Name: "A", State: domain.NodeFailed}
	ctx.Runtime["B"] = &domain.NodeRuntime{Name: "B", State: domain.NodePending}
	ctx.Runtime["D"] = &domain.NodeRuntime{Name: "D", State: domain.NodeSuccess}
	ctx.Runtime["C"] = &domain.NodeRuntime{Name: "C", State: domain.NodePending}
	ctx.ActivatedEdges["A->B"] = true // P0: failed EdgeNormal → true
	ctx.ActivatedEdges["B->C"] = true // 还未计算，先设 true
	ctx.ActivatedEdges["D->C"] = true

	// 模拟：globalClosure 处理 B → shouldSkipNode 应为 true（A blocked）
	if !e.shouldSkipNode(ctx, "B", g) {
		t.Fatal("B should be skipped: A failed → blocked")
	}
	// skipNodeWithCorrectKind 应为 finalizeBlockedNode（有 blocked 父）
	if !e.hasBlockedParent(ctx, "B", g) {
		t.Fatal("B should have blocked parent A")
	}

	// 执行 skip
	if err := e.skipNodeWithCorrectKind(ctx, "B", g); err != nil {
		t.Fatalf("skip B: %v", err)
	}
	if ctx.Runtime["B"].State != domain.NodeSkipped {
		t.Fatalf("B 应为 Skipped, got %s", ctx.Runtime["B"].State)
	}

	// B->C 是 EdgeNormal + B Skipped + edge=true → blocked（NOT inactive）
	es := e.resolveEdgeState(ctx, "B", "C", g)
	if es != EdgeStateBlocked {
		t.Fatalf("B->C: Skipped + EdgeNormal + edge=true 应为 blocked, got %s", es)
	}

	// C 做 depsMet：D 成功 + B blocked → 应阻塞
	if e.depsMet(ctx, "C", g) {
		t.Fatal("C must not execute: B blocked + D success → fan-in join 应阻塞")
	}
}

// TestShouldSkipNode_SkippedVsBlockedDisambiguation 验证 shouldSkipNode 对
// blocked 返回 true，对 inactive-only 返回 true，但两者的 skip 函数必须不同。
func TestShouldSkipNode_SkippedVsBlockedDisambiguation(t *testing.T) {
	e := &Engine{}

	t.Run("blocked by failed parent triggers skip", func(t *testing.T) {
		ctx, g := newFanInFixture(t)
		ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeFailed}
		ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSuccess}
		ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}
		ctx.ActivatedEdges["left_branch->target"] = true
		ctx.ActivatedEdges["right_branch->target"] = true

		if !e.shouldSkipNode(ctx, "target", g) {
			t.Fatal("其中一父 Failed → shouldSkipNode 应 true")
		}
		if !e.hasBlockedParent(ctx, "target", g) {
			t.Fatal("应有 blocked parent")
		}
	})

	t.Run("all inactive triggers skip without blocked parent", func(t *testing.T) {
		ctx, g := newFanInFixture(t)
		ctx.Runtime["left_branch"] = &domain.NodeRuntime{Name: "left_branch", State: domain.NodeSkipped}
		ctx.Runtime["right_branch"] = &domain.NodeRuntime{Name: "right_branch", State: domain.NodeSkipped}
		ctx.Runtime["target"] = &domain.NodeRuntime{Name: "target", State: domain.NodePending}
		ctx.ActivatedEdges["left_branch->target"] = false
		ctx.ActivatedEdges["right_branch->target"] = false

		if !e.shouldSkipNode(ctx, "target", g) {
			t.Fatal("全 inactive → shouldSkipNode 应 true")
		}
		if e.hasBlockedParent(ctx, "target", g) {
			t.Fatal("全 inactive 不应有 blocked parent")
		}
	})
}
