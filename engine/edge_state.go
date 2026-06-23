package engine

import (
	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
	"flux/workflow/nodes"
)

// EdgeRuntimeState 运行时边三态推导。
// ActivatedEdges 持久化仍为 bool（不存三态），但所有调度判断统一通过
// resolveEdgeState 从 edge.Type + parent.State + ActivatedEdges[key] 推导，
// 解决「false 同时表示条件未命中和必选父阻塞」的语义漏洞。
type EdgeRuntimeState string

const (
	// EdgeStateUnknown — 边状态尚未确定（父未 terminal 或 edge 未计算）。
	EdgeStateUnknown EdgeRuntimeState = "unknown"
	// EdgeStateActive — 边被激活且父节点成功，参与下游 join。
	EdgeStateActive EdgeRuntimeState = "active"
	// EdgeStateInactive — 条件边未命中（仅限父节点成功 + 条件不满足），
	// 该边从下游 join 剔除。
	EdgeStateInactive EdgeRuntimeState = "inactive"
	// EdgeStateBlocked — 父节点 terminal 但非 Success（failed/skipped/canceled）。
	// 无论 EdgeNormal 还是 EdgeCondition，父死即路径阻塞，不允许下游执行。
	EdgeStateBlocked EdgeRuntimeState = "blocked"
)

// findEdge 在 DAG 中根据 parent/child 查找 EdgeDefinition。
// dag.Parents[node] 只存 parent name，不存 edge 对象，因此需要此函数做类型判断。
func findEdge(dag *graph.Graph, parent, child string) *definition.EdgeDefinition {
	if dag == nil {
		return nil
	}
	for _, e := range dag.Edges[parent] {
		if e.To == child {
			return &e
		}
	}
	return nil
}

// resolveEdgeState 从持久化的 bool + 父运行时状态 + 边类型，推导三态。
//
// 核心规则：
//   - 父未 terminal → unknown（等待）
//   - 父 Failed/Canceled → 一律 blocked（不论边类型）
//   - 父 Skipped + EdgeNormal + edge=true → blocked（执行路径上的节点被级联 skip）
//   - 父 Skipped + edge=false → inactive（死分支，条件未命中传播）
//   - EdgeNormal + 父 Success → active
//   - EdgeCondition/CaseKey + 父 Success + activated → active
//   - EdgeCondition/CaseKey + 父 Success + !activated → inactive
func (e *Engine) resolveEdgeState(
	ctx *nodes.Context,
	parent string,
	child string,
	dag *graph.Graph,
) EdgeRuntimeState {
	rt := ctx.Runtime[parent]
	if rt == nil {
		return EdgeStateUnknown
	}

	if !isTerminal(rt.State) {
		return EdgeStateUnknown
	}

	// 父 Failed/Canceled → 一律 blocked，不可忽略。
	if rt.State == domain.NodeFailed || rt.State == domain.NodeCanceled {
		return EdgeStateBlocked
	}

	// 父 Success，看边类型。
	edge := findEdge(dag, parent, child)
	if edge == nil {
		return EdgeStateUnknown
	}

	// 所有边类型都需要先确认边是否已计算。
	key := parent + "->" + child
	if _, ok := ctx.ActivatedEdges[key]; !ok {
		return EdgeStateUnknown
	}

	activated := ctx.ActivatedEdges[key]

	if rt.State == domain.NodeSuccess {
		// EdgeNormal：无条件边，父成功即激活。
		if edge.Type == definition.EdgeNormal {
			return EdgeStateActive
		}
		// EdgeCondition / CaseKey。
		if activated {
			return EdgeStateActive
		}
		return EdgeStateInactive
	}

	// parent Skipped：根据边值和类型区分 inactive（死分支）与 blocked（级联 skip）。
	if edge.Type == definition.EdgeNormal && activated {
		// 执行路径上的 EdgeNormal 节点被级联 skip（上游失败）→ blocked。
		return EdgeStateBlocked
	}
	// Skipped + (EdgeCondition 或 activated=false) → 死分支，inactive。
	return EdgeStateInactive
}

// hasBlockedParent 检查节点的入边中是否有 blocked（上游失败/级联 skip）。
// 用于决定应调用 finalizeBlockedNode（阻塞传播）还是 finalizeSkippedNode（死分支）。
func (e *Engine) hasBlockedParent(ctx *nodes.Context, node string, dag *graph.Graph) bool {
	for _, p := range dag.Parents[node] {
		if e.resolveEdgeState(ctx, p, node, dag) == EdgeStateBlocked {
			return true
		}
	}
	return false
}

// skipNodeWithCorrectKind 根据父边状态选择正确的 skip 函数并执行：
//   - 有 blocked 父 → finalizeBlockedNode（阻塞传播，EdgeNormal→true）
//   - 全 inactive → finalizeSkippedNode（死分支，全部→false）
func (e *Engine) skipNodeWithCorrectKind(ctx *nodes.Context, nodeName string, dag *graph.Graph) error {
	if e.hasBlockedParent(ctx, nodeName, dag) {
		return e.finalizeBlockedNode(ctx, nodeName, dag)
	}
	return e.finalizeSkippedNode(ctx, nodeName, dag)
}
