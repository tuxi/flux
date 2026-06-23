package engine

import (
	"flux/engine/graph"
	"flux/workflow"
	"fmt"
)

func (e *Engine) validateResumeBoundary(
	wf workflow.Workflow,
	resumeFrom string,
) error {
	if resumeFrom == "" {
		return nil
	}
	if _, ok := wf.Nodes()[resumeFrom]; !ok {
		return fmt.Errorf("resume_from node not found: %s", resumeFrom)
	}
	return nil
}

func (e *Engine) applyPatchedNodePriorityRules(
	plan *DirtyPlan,
	patchedNodes map[string]bool,
) {
	if plan == nil {
		return
	}

	for nodeName := range patchedNodes {
		if nodeName == "" {
			continue
		}
		plan.PatchedNodes[nodeName] = true

		// patched 节点本身不是普通 reuse，也不是默认 dirty execute node
		delete(plan.ReuseNodes, nodeName)
		delete(plan.DirtyNodes, nodeName)
	}
}

func (e *Engine) applyResumeBoundaryRules(
	plan *DirtyPlan,
	g *graph.Graph,
	resumeFrom string,
) {
	if plan == nil || g == nil || resumeFrom == "" {
		return
	}

	// resume boundary 本身必须重跑
	plan.DirtyNodes[resumeFrom] = DirtyReasonResumeBoundary
	delete(plan.ReuseNodes, resumeFrom)
	delete(plan.PatchedNodes, resumeFrom)

	// downstream 全部 upstream_dirty
	queue := []string{resumeFrom}
	visited := map[string]bool{}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if visited[cur] {
			continue
		}
		visited[cur] = true

		for _, child := range g.Children[cur] {
			if _, exists := plan.DirtyNodes[child]; !exists {
				plan.DirtyNodes[child] = DirtyReasonUpstreamDirty
			}
			delete(plan.ReuseNodes, child)
			delete(plan.PatchedNodes, child)
			queue = append(queue, child)
		}
	}
}
