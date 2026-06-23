package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
	"flux/utils"
	"flux/workflow"
	"flux/workflow/nodes"
)

func (e *Engine) BuildRunPlan(
	runCtx *nodes.Context,
	wf workflow.Workflow,
	newInput map[string]any,
) (*RunPlan, error) {
	if runCtx == nil {
		return nil, fmt.Errorf("runCtx is nil")
	}

	mode := RunPlanModeInitial
	if runCtx.Task != nil && runCtx.Task.ForkedFrom != nil {
		mode = RunPlanModeFork
	}

	runPlan := &RunPlan{
		TaskID:     runCtx.Task.ID,
		Mode:       mode,
		ResumeFrom: runCtx.ResumeFrom,
		Nodes:      map[string]*NodePlan{},
		TopoOrder:  wf.Order(),
	}

	if runCtx.Task != nil && runCtx.Task.ForkedFrom != nil {
		runPlan.ParentTaskID = runCtx.Task.ForkedFrom
	}

	// 非 fork：全部直接 execute，planning 不做复用判定
	if runCtx.ParentSnapshot == nil {
		nodeMap := wf.Nodes()
		for _, nodeName := range wf.Order() {
			label := ""
			var nodeType definition.NodeType
			if n, ok := nodeMap[nodeName]; ok {
				label = n.Label
				nodeType = n.Type
			}
			runPlan.Nodes[nodeName] = &NodePlan{
				Name:      nodeName,
				Label:     label,
				NodeType:  nodeType,
				Action:    PlanActionExecute,
				Reason:    ExecutionReasonNone,
				ReuseKind: domain.ReuseNone,
			}
		}
		return runPlan, nil
	}

	if err := e.validateResumeBoundary(wf, runCtx.ResumeFrom); err != nil {
		return nil, err
	}

	// 状态闭合校验：fork/redo 场景下，父任务状态必须闭合
	if vr := ValidateParentStateClosure(runCtx.ParentSnapshot.Nodes, wf.Graph(), ClosureModeFork); !vr.Valid {
		// 将第一个 blocking issue 转为 error 返回
		for _, issue := range vr.Issues {
			if issue.Level == ClosureLevelBlock {
				return nil, fmt.Errorf("parent state not closed: %s", issue.Message)
			}
		}
	}

	planCtx, err := e.newPlanningContext(runCtx, wf, newInput)
	if err != nil {
		return nil, err
	}

	g := wf.Graph()
	nodeMap := wf.Nodes()

	for _, nodeName := range wf.Order() {
		nodePlan, err := e.buildSingleNodePlan(runCtx, planCtx, wf, g, nodeMap, runPlan, nodeName)
		if err != nil {
			return nil, err
		}
		runPlan.Nodes[nodeName] = nodePlan
	}

	if err := e.fillRunPlanMapItemReuse(runPlan, planCtx, wf); err != nil {
		return nil, err
	}

	return runPlan, nil
}

func (e *Engine) newPlanningContext(
	runCtx *nodes.Context,
	wf workflow.Workflow,
	newInput map[string]any,
) (*nodes.Context, error) {
	parentSnapshot := runCtx.ParentSnapshot
	if parentSnapshot == nil {
		return nil, fmt.Errorf("parent snapshot is nil")
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

	// 1. 建最小 runtime
	for _, nodeName := range wf.Order() {
		planCtx.Runtime[nodeName] = &domain.NodeRuntime{
			TaskID: runCtx.Task.ID,
			Name:   nodeName,
			State:  domain.NodePending,
		}
	}

	// 2. seed 父快照 success 节点。
	// 但不注入 resumeFrom 及其下游节点的 output：这些节点原执行时尚未产出，
	// 若上游节点的 input_mapping 引用了它们，提前注入会导致 hash 误判 input_changed。
	g := wf.Graph()
	for _, nodeName := range wf.Order() {
		parentNode, ok := parentSnapshot.Nodes[nodeName]
		if !ok || parentNode == nil {
			continue
		}
		if parentNode.State != domain.NodeSuccess {
			continue
		}
		if runCtx.ResumeFrom != "" && isReachable(g, runCtx.ResumeFrom, nodeName) {
			continue
		}
		if err := e.seedPlanningNode(planCtx, wf, nodeName, parentNode); err != nil {
			return nil, err
		}
	}

	// 3. planning 上应用 patch
	if err := e.applyPatchesToPlanningContext(planCtx, wf); err != nil {
		return nil, err
	}

	return planCtx, nil
}

func (e *Engine) buildSingleNodePlan(
	runCtx *nodes.Context,
	planCtx *nodes.Context,
	wf workflow.Workflow,
	g *graph.Graph,
	nodeMap map[string]nodes.Node,
	runPlan *RunPlan,
	nodeName string,
) (*NodePlan, error) {
	nodePlan := &NodePlan{
		Name:      nodeName,
		Label:     getNodeLabel(nodeMap, nodeName),
		NodeType:  getNodeType(nodeMap, nodeName),
		Action:    PlanActionExecute,
		Reason:    ExecutionReasonNone,
		ReuseKind: domain.ReuseNone,
	}

	parentSnapshot := runCtx.ParentSnapshot
	if parentSnapshot != nil {
		if parentNode, ok := parentSnapshot.Nodes[nodeName]; ok && parentNode != nil {
			nodePlan.ParentTaskID = &parentNode.TaskID
			reusedNode := parentNode.Name
			nodePlan.ParentNode = &reusedNode
		}
	}

	node, ok := nodeMap[nodeName]
	if !ok {
		return nil, fmt.Errorf("node def not found: %s", nodeName)
	}

	parentNode, ok := parentSnapshot.Nodes[nodeName]
	if !ok || parentNode == nil {
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonMissingParent
		return nodePlan, nil
	}

	// 1. resume boundary 自己，优先级最高
	if runCtx.ResumeFrom != "" && nodeName == runCtx.ResumeFrom {
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonResumeBoundary
		return nodePlan, nil
	}

	// 2. patch 命中当前节点
	if planCtx.PatchedNodes[nodeName] {
		nodePlan.Action = PlanActionPatch
		nodePlan.Reason = ExecutionReasonPatchedNode
		nodePlan.ReuseKind = domain.ReuseNone
		nodePlan.Patches = collectNodePatches(runCtx.Patches, nodeName)
		return nodePlan, nil
	}

	// Inactive conditional branches are terminal in the parent run. Reuse that
	// skipped state instead of turning it into a dirty executable branch.
	if parentNode.State == domain.NodeSkipped {
		nodePlan.Action = PlanActionReuse
		nodePlan.Reason = ExecutionReasonReuseNode
		nodePlan.ReuseKind = domain.ReuseNode
		return nodePlan, nil
	}

	// 3. 父节点状态非 success，无条件 execute
	if parentNode.State != domain.NodeSuccess {
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonParentNotReady
		return nodePlan, nil
	}

	// persist_output=false 的终端聚合节点 output 未落盘，Fork 时必须重新执行
	if len(parentNode.Output) == 0 && !shouldPersistOutput(node) {
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonParentNotReady
		return nodePlan, nil
	}

	// 4. 如果是 resumeFrom 下游，必须 execute
	if runCtx.ResumeFrom != "" && isReachable(g, runCtx.ResumeFrom, nodeName) {
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonUpstreamDirty
		return nodePlan, nil
	}

	// 5. 如果任一父节点已经决定 execute，则当前必须 execute
	if e.hasExecuteParent(nodeName, g, runPlan, parentSnapshot) {
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonUpstreamDirty
		return nodePlan, nil
	}

	// 6. hash 比较
	inputs, err := e.buildNodeInputForPlan(planCtx, node, g)
	if err != nil {
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonInputResolveFail
		return nodePlan, nil
	}

	newHash := planCtx.CalculateInputHash(
		fmt.Sprintf("%d-%s", runCtx.Task.WorkflowVersionID, node.Name),
		inputs,
	)

	if newHash != parentNode.InputHash {
		// 🔍 诊断日志：输出规范化后的 JSON 首尾片段，便于定位差异根因。
		normalized := utils.NormalizeMap(inputs)
		inputJSON, _ := json.Marshal(normalized)
		preview := formatJSONPreview(inputJSON)
		log.Printf("[plan preview] hash mismatch node=%s old=%s new=%s workflowVer=%d input_size=%d input_preview=%s",
			nodeName, parentNode.InputHash, newHash, runCtx.Task.WorkflowVersionID, len(inputJSON), preview)
		nodePlan.Action = PlanActionExecute
		nodePlan.Reason = ExecutionReasonInputChanged
		return nodePlan, nil
	}

	// 7. 否则 reuse
	nodePlan.Action = PlanActionReuse
	nodePlan.Reason = ExecutionReasonReuseNode
	nodePlan.ReuseKind = domain.ReuseNode

	return nodePlan, nil
}

func (e *Engine) hasExecuteParent(
	nodeName string,
	g *graph.Graph,
	runPlan *RunPlan,
	parentSnapshot *nodes.ReuseSnapshot,
) bool {
	if g == nil || runPlan == nil {
		return false
	}
	for _, p := range g.Parents[nodeName] {
		if !parentEdgeWasActivated(parentSnapshot, p, nodeName) {
			continue
		}
		parentPlan := runPlan.Nodes[p]
		if parentPlan == nil {
			continue
		}
		if parentPlan.Action == PlanActionExecute {
			return true
		}
	}
	return false
}

func parentEdgeWasActivated(
	parentSnapshot *nodes.ReuseSnapshot,
	from string,
	to string,
) bool {
	if parentSnapshot == nil {
		return true
	}
	parentNode := parentSnapshot.Nodes[from]
	if parentNode == nil || parentNode.ActivatedEdges == nil {
		return true
	}
	edgeKey := fmt.Sprintf("%s->%s", from, to)
	active, ok := parentNode.ActivatedEdges[edgeKey]
	if !ok {
		return true
	}
	return active
}

func (e *Engine) fillRunPlanMapItemReuse(
	runPlan *RunPlan,
	planCtx *nodes.Context,
	wf workflow.Workflow,
) error {
	parentSnapshot := planCtx.ParentSnapshot
	if parentSnapshot == nil {
		return nil
	}

	for _, nodeName := range wf.Order() {
		nodePlan := runPlan.Nodes[nodeName]
		if nodePlan == nil {
			continue
		}
		if nodePlan.Action != PlanActionExecute {
			continue
		}

		node, ok := wf.Nodes()[nodeName]
		if !ok {
			continue
		}
		if node.Type != definition.NodeMap {
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

		reuse := map[int]bool{}
		for i, item := range items {
			newHash := nodes.CalculateMapItemHash(item)
			oldHash, _ := itemHashesRaw[strconv.Itoa(i)].(string)
			if oldHash != "" && oldHash == newHash {
				reuse[i] = true
			}
		}

		if len(reuse) > 0 {
			nodePlan.MapItemReuse = reuse
			nodePlan.ReuseKind = domain.ReuseMapItems
		}
	}

	return nil
}

func collectNodePatches(
	patches []domain.RuntimePatch,
	nodeName string,
) []domain.RuntimePatch {
	if len(patches) == 0 {
		return nil
	}

	out := make([]domain.RuntimePatch, 0, len(patches))
	for _, p := range patches {
		if p.Node == nodeName {
			out = append(out, p)
		}
	}
	return out
}

func cloneIntBoolMap(in map[int]bool) map[int]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func getNodeLabel(nodeMap map[string]nodes.Node, nodeName string) string {
	if n, ok := nodeMap[nodeName]; ok {
		return n.Label
	}
	return ""
}

func getNodeType(nodeMap map[string]nodes.Node, nodeName string) definition.NodeType {
	if n, ok := nodeMap[nodeName]; ok {
		return n.Type
	}
	return ""
}

// formatJSONPreview 返回 JSON 字节的人类可读预览，用于日志诊断。
// 小型 JSON（<= 600 字节）直接返回；大型 JSON 截取前 500 + 后 100 字节。
func formatJSONPreview(data []byte) string {
	if len(data) <= 600 {
		return string(data)
	}
	head := 500
	tail := 100
	if head > len(data) {
		head = len(data)
	}
	if head+tail >= len(data) {
		return string(data)
	}
	return string(data[:head]) + "…[TRUNCATED]…" + string(data[len(data)-tail:])
}
