package engine

import (
	"flux/domain"
	"flux/engine/graph"
	"flux/workflow"
	"fmt"
	"strings"
)

func (e *Engine) ValidateResumeSpecForExternal(
	wf workflow.Workflow,
	resumeSpec *domain.ResumeSpec,
) error {
	return e.validateResumeSpec(wf, resumeSpec)
}

func (e *Engine) validateResumeSpec(
	wf workflow.Workflow,
	spec *domain.ResumeSpec,
) error {
	if spec == nil {
		return nil
	}

	if err := e.validateResumeBoundary(wf, spec.ResumeFrom); err != nil {
		return err
	}

	for i, patch := range spec.Patches {
		if err := e.validateRuntimePatch(wf, patch); err != nil {
			return fmt.Errorf("patch[%d] invalid: %w", i, err)
		}
		if err := e.validatePatchResumeRelation(wf, patch, spec.ResumeFrom); err != nil {
			return fmt.Errorf("patch[%d] resume relation invalid: %w", i, err)
		}
	}

	return nil
}

func (e *Engine) validateRuntimePatch(
	wf workflow.Workflow,
	patch domain.RuntimePatch,
) error {
	if patch.Node == "" {
		return fmt.Errorf("patch node is empty")
	}
	if _, ok := wf.Nodes()[patch.Node]; !ok {
		return fmt.Errorf("patch node not found: %s", patch.Node)
	}

	switch patch.Target {
	case domain.PatchTargetNodeOutput, domain.PatchTargetNodeCheckpoint:
	default:
		return fmt.Errorf("unsupported patch target: %s", patch.Target)
	}

	switch patch.Op {
	case domain.PatchOpSet, domain.PatchOpDelete, domain.PatchOpMerge:
	default:
		return fmt.Errorf("unsupported patch op: %s", patch.Op)
	}

	trimmedPath := strings.TrimSpace(strings.Trim(patch.Path, "."))
	if patch.Op != domain.PatchOpMerge || patch.Path != "" {
		// merge 允许 path="" 表示 merge root object
		if trimmedPath == "" && patch.Path != "" {
			return fmt.Errorf("invalid patch path")
		}
		if patch.Op != domain.PatchOpMerge && trimmedPath == "" {
			return fmt.Errorf("patch path is empty")
		}
	}

	if patch.Op == domain.PatchOpMerge {
		if _, ok := patch.Value.(map[string]any); !ok {
			return fmt.Errorf("merge patch value must be map[string]any, got %T", patch.Value)
		}
	}

	return nil
}

// validatePatchResumeRelation
//
// 规则：
// 1. resume_from 可为空，为空表示只 patch，不指定消费边界（不推荐，但允许）
// 2. 如果指定了 resume_from：
//   - 可以等于 patch.Node（表示 patch 当前节点状态，然后从该节点重跑）
//   - 或必须位于 patch.Node 的下游
//
// 3. 不能从 patch.Node 的上游 resume
func (e *Engine) validatePatchResumeRelation(
	wf workflow.Workflow,
	patch domain.RuntimePatch,
	resumeFrom string,
) error {
	if resumeFrom == "" {
		return nil
	}

	if patch.Node == resumeFrom {
		return nil
	}

	g := wf.Graph()
	if g == nil {
		return fmt.Errorf("workflow graph is nil")
	}

	if !isReachable(g, patch.Node, resumeFrom) {
		return fmt.Errorf("resume_from=%s is not downstream of patch node=%s", resumeFrom, patch.Node)
	}

	return nil
}

func isReachable(g *graph.Graph, from, to string) bool {
	if g == nil {
		return false
	}
	if from == to {
		return true
	}

	visited := map[string]bool{}
	queue := []string{from}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if visited[cur] {
			continue
		}
		visited[cur] = true

		for _, child := range g.Children[cur] {
			if child == to {
				return true
			}
			queue = append(queue, child)
		}
	}

	return false
}
