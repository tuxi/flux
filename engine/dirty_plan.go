package engine

import (
	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
	"flux/utils"
	"flux/workflow"
	"flux/workflow/nodes"
	"fmt"
	"strconv"
)

const (
	EditReplaceStartImage = "replace_start_image"
	EditReplaceEndImage   = "replace_end_image"
	EditUserPrompt        = "edit_user_prompt"
)

const (
	DirtyReasonInputChanged   = "input_changed"
	DirtyReasonUpstreamDirty  = "upstream_dirty"
	DirtyReasonMissingParent  = "missing_parent_snapshot"
	DirtyReasonInputResolve   = "input_resolve_failed"
	DirtyReasonParentNotReady = "parent_not_success"

	DirtyReasonPatchedState   = "patched_state"
	DirtyReasonResumeBoundary = "resume_boundary"
)

type DirtyPlan struct {
	DirtyNodes   map[string]string
	ReuseNodes   map[string]bool
	MapItemReuse map[string]map[int]bool
	PatchedNodes map[string]bool
}

// BuildDirtyPlan
//
// 规则：
// 1. parent snapshot success 全量 seed 到 planCtx
// 2. patch 应用到 planCtx
// 3. topo 顺序重新构造 input
// 4. 普通 hash 对比只决定“可否 reuse”
// 5. patched node 和 resume_from 再走优先级覆盖
func (e *Engine) BuildDirtyPlan(
	runCtx *nodes.Context,
	wf workflow.Workflow,
	newInput map[string]any,
) (*DirtyPlan, error) {
	plan := &DirtyPlan{
		DirtyNodes:   map[string]string{},
		ReuseNodes:   map[string]bool{},
		MapItemReuse: map[string]map[int]bool{},
		PatchedNodes: map[string]bool{},
	}

	parentSnapshot := runCtx.ParentSnapshot
	if parentSnapshot == nil {
		return plan, nil
	}

	if err := e.validateResumeBoundary(wf, runCtx.ResumeFrom); err != nil {
		return nil, err
	}

	planCtx := &nodes.Context{
		Ctx:            runCtx.Ctx,
		Task:           runCtx.Task,
		Workflow:       runCtx.Workflow,
		Input:          newInput,
		Output:         map[string]any{"input": newInput, "nodes": map[string]any{}},
		Runtime:        map[string]*domain.NodeRuntime{},
		ActivatedEdges: map[string]bool{},
		EventBus:       runCtx.EventBus,
		ParentSnapshot: parentSnapshot,

		Patches:      runCtx.Patches,
		ResumeFrom:   runCtx.ResumeFrom,
		PatchedNodes: map[string]bool{},
	}
	planCtx.EnsureOutputInitialized()

	g := wf.Graph()
	nodeMap := wf.Nodes()

	// 1) 建最小 runtime
	for _, nodeName := range wf.Order() {
		planCtx.Runtime[nodeName] = &domain.NodeRuntime{
			TaskID: runCtx.Task.ID,
			Name:   nodeName,
			State:  domain.NodePending,
		}
	}

	// 2) 把父快照成功节点全部 seed 进 planning ctx
	for _, nodeName := range wf.Order() {
		parentNode, ok := parentSnapshot.Nodes[nodeName]
		if !ok || parentNode == nil {
			continue
		}
		if parentNode.State != domain.NodeSuccess {
			continue
		}
		if err := e.seedPlanningNode(planCtx, wf, nodeName, parentNode); err != nil {
			return nil, err
		}
	}

	// 3) 应用 patch 到 planning ctx
	if err := e.applyPatchesToPlanningContext(planCtx, wf); err != nil {
		return nil, err
	}

	// 4) topo 顺序比较 hash
	for _, nodeName := range wf.Order() {
		node, ok := nodeMap[nodeName]
		if !ok {
			continue
		}

		parentNode, ok := parentSnapshot.Nodes[nodeName]
		if !ok || parentNode == nil {
			plan.DirtyNodes[nodeName] = DirtyReasonMissingParent
			continue
		}
		if parentNode.State != domain.NodeSuccess {
			plan.DirtyNodes[nodeName] = DirtyReasonParentNotReady
			continue
		}

		// patch 命中的节点先跳过，后面统一按 patch 优先级处理
		if planCtx.PatchedNodes[nodeName] {
			continue
		}

		// resume boundary 自己后面统一处理
		if runCtx.ResumeFrom != "" && nodeName == runCtx.ResumeFrom {
			continue
		}

		// 上游已 dirty，则当前直接 dirty
		if e.hasDirtyParent(nodeName, g, plan) {
			plan.DirtyNodes[nodeName] = DirtyReasonUpstreamDirty
			continue
		}

		inputs, err := e.buildNodeInputForPlan(planCtx, node, g)
		if err != nil {
			plan.DirtyNodes[nodeName] = DirtyReasonInputResolve
			continue
		}

		newHash := planCtx.CalculateInputHash(
			fmt.Sprintf("%d-%s", runCtx.Task.WorkflowVersionID, node.Name),
			inputs,
		)

		if newHash != parentNode.InputHash {
			plan.DirtyNodes[nodeName] = DirtyReasonInputChanged
			continue
		}

		plan.ReuseNodes[nodeName] = true
	}

	// 5) patched node 规则
	e.applyPatchedNodePriorityRules(plan, planCtx.PatchedNodes)

	// 6) resume boundary 规则，优先级最高
	e.applyResumeBoundaryRules(plan, g, runCtx.ResumeFrom)

	// 7) dirty 节点不能 reuse，也不能保留 patched-only 身份
	for nodeName := range plan.DirtyNodes {
		delete(plan.ReuseNodes, nodeName)
		delete(plan.PatchedNodes, nodeName)
	}

	// 8) map item reuse
	if err := e.fillMapItemReuse(plan, planCtx, wf); err != nil {
		return nil, err
	}

	return plan, nil
}

func (e *Engine) buildNodeInputForPlan(
	ctx *nodes.Context,
	node nodes.Node,
	g *graph.Graph,
) (map[string]any, error) {
	return e.buildNodeInput(ctx, node, g)
}

func (e *Engine) hasDirtyParent(
	nodeName string,
	g *graph.Graph,
	plan *DirtyPlan,
) bool {
	if g == nil || plan == nil {
		return false
	}
	for _, p := range g.Parents[nodeName] {
		if _, dirty := plan.DirtyNodes[p]; dirty {
			return true
		}
	}
	return false
}

func (e *Engine) seedPlanningNode(
	planCtx *nodes.Context,
	wf workflow.Workflow,
	nodeName string,
	parentNode *domain.NodeRuntime,
) error {
	if planCtx == nil || parentNode == nil {
		return nil
	}

	defNode := findNode(wf.Nodes(), nodeName)
	if defNode == nil {
		return fmt.Errorf("node def not found: %s", nodeName)
	}

	if parentNode.Output != nil {
		if err := planCtx.SetNodeOutput(
			nodeName,
			deepCloneMap(parentNode.Output),
			defNode.Step.OutputSchema(),
		); err != nil {
			return err
		}
	}

	planCtx.UpdateNodeStatus(nodeName, string(domain.NodeSuccess))
	planCtx.ActivatedEdgesMerge(cloneBoolMap(parentNode.ActivatedEdges))

	if rt, ok := planCtx.Runtime[nodeName]; ok && rt != nil {
		rt.State = domain.NodeSuccess
		rt.Output = deepCloneMap(parentNode.Output)
		rt.Checkpoint = deepCloneMap(parentNode.Checkpoint)
		rt.InputHash = parentNode.InputHash
		rt.ResolvedInput = deepCloneMap(parentNode.ResolvedInput)
		rt.OutputHash = parentNode.OutputHash
		rt.ActivatedEdges = cloneBoolMap(parentNode.ActivatedEdges)
	}

	return nil
}

func (e *Engine) fillMapItemReuse(
	plan *DirtyPlan,
	planCtx *nodes.Context,
	wf workflow.Workflow,
) error {
	parentSnapshot := planCtx.ParentSnapshot
	if parentSnapshot == nil {
		return nil
	}

	for _, nodeName := range wf.Order() {
		node, ok := wf.Nodes()[nodeName]
		if !ok {
			continue
		}
		if node.Type != definition.NodeMap {
			continue
		}
		if plan.ReuseNodes[nodeName] {
			continue
		}
		if _, dirty := plan.DirtyNodes[nodeName]; !dirty {
			continue
		}

		parentNode, ok := parentSnapshot.Nodes[nodeName]
		if !ok || parentNode == nil || parentNode.Checkpoint == nil {
			continue
		}

		itemsExpr, _ := node.Config["items"].(string)
		if itemsExpr == "" {
			continue
		}

		val, err := planCtx.EvalAny(itemsExpr)
		if err != nil {
			continue
		}

		items, ok := utils.ToAnySlice(val)
		if !ok || len(items) == 0 {
			continue
		}

		itemHashesRaw, _ := parentNode.Checkpoint["item_hashes"].(map[string]any)
		if itemHashesRaw == nil {
			continue
		}

		if plan.MapItemReuse[nodeName] == nil {
			plan.MapItemReuse[nodeName] = map[int]bool{}
		}

		for i, item := range items {
			newHash := nodes.CalculateMapItemHash(item)
			oldHash, _ := itemHashesRaw[strconv.Itoa(i)].(string)
			if oldHash != "" && oldHash == newHash {
				plan.MapItemReuse[nodeName][i] = true
			}
		}
	}

	return nil
}
