package engine

import (
	"flux/workflow"
	"flux/workflow/nodes"
)

func (e *Engine) markPatchedNodesDirty(
	plan *DirtyPlan,
	ctx *nodes.Context,
) {
	if plan == nil || ctx == nil {
		return
	}
	for nodeName := range ctx.PatchedNodes {
		plan.DirtyNodes[nodeName] = DirtyReasonPatchedState
	}
}

func (e *Engine) markResumeBoundaryDirty(
	plan *DirtyPlan,
	wf workflow.Workflow,
	resumeFrom string,
) {
	if plan == nil || resumeFrom == "" {
		return
	}
	if _, ok := wf.Nodes()[resumeFrom]; !ok {
		return
	}
	plan.DirtyNodes[resumeFrom] = DirtyReasonResumeBoundary
}
