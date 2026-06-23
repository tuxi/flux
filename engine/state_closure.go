package engine

import (
	"fmt"

	"flux/domain"
	"flux/engine/graph"
)

// ClosureValidationLevel indicates severity.
type ClosureValidationLevel string

const (
	ClosureLevelBlock ClosureValidationLevel = "block"
)

// ClosureValidationMode controls strictness per entry point.
type ClosureValidationMode string

const (
	ClosureModeFork   ClosureValidationMode = "fork"   // strictest — no unstable or awaiting nodes
	ClosureModeResume ClosureValidationMode = "resume" // allow awaiting / suspended
)

// ClosureValidationIssue describes a single state closure problem.
type ClosureValidationIssue struct {
	Level     ClosureValidationLevel `json:"level"`
	NodeName  string                 `json:"node_name"`
	EdgeKey   string                 `json:"edge_key,omitempty"`
	FieldName string                 `json:"field_name,omitempty"`
	Message   string                 `json:"message"`
}

// ClosureValidationResult aggregates all validation issues.
type ClosureValidationResult struct {
	Valid  bool                     `json:"valid"`
	Issues []ClosureValidationIssue `json:"issues,omitempty"`
}

// ValidateParentStateClosure checks the parent task's node runtime states
// for DAG state closure consistency before fork/redo/resume operations.
func ValidateParentStateClosure(
	parentNodes map[string]*domain.NodeRuntime,
	dag *graph.Graph,
	mode ClosureValidationMode,
) *ClosureValidationResult {
	result := &ClosureValidationResult{Valid: true}

	// Check 1: Terminal nodes (success/failed) must have all outgoing edges
	// decided in ActivatedEdges.
	checkTerminalEdgeClosure(parentNodes, dag, result)

	// Check 2: No unstable transient nodes.
	checkUnstableNodes(parentNodes, result, mode)

	// Check 3: Pending nodes must have parent edge decisions for every
	// terminal parent. An undecided edge from a terminal parent means the
	// DAG was interrupted before edge computation completed.
	checkPendingNodeEdges(parentNodes, dag, result)

	result.Valid = len(result.Issues) == 0
	return result
}

// terminalStates are node states that represent completed execution.
var terminalNodeStates = map[domain.NodeState]bool{
	domain.NodeSuccess:             true,
	domain.NodeSuccessPendingEdges: true,
	domain.NodeFailed:              true,
	domain.NodeFailedPendingEdges:  true,
	domain.NodeSkipped:             true,
	domain.NodeCanceled:            true,
}

// checkTerminalEdgeClosure ensures every terminal node that has outgoing
// edges has all of them decided in its ActivatedEdges map.
func checkTerminalEdgeClosure(
	parentNodes map[string]*domain.NodeRuntime,
	dag *graph.Graph,
	result *ClosureValidationResult,
) {
	for name, rt := range parentNodes {
		if rt == nil {
			continue
		}
		if !terminalNodeStates[rt.State] {
			continue
		}

		edges := dag.Edges[name]
		if len(edges) == 0 {
			continue
		}

		for _, edge := range edges {
			edgeKey := name + "->" + edge.To
			if _, ok := rt.ActivatedEdges[edgeKey]; !ok {
				result.Issues = append(result.Issues, ClosureValidationIssue{
					Level:     ClosureLevelBlock,
					NodeName:  name,
					EdgeKey:   edgeKey,
					FieldName: "activated_edges",
					Message:   fmt.Sprintf("terminal node %s missing edge decision for %s", name, edgeKey),
				})
			}
		}
	}
}

// unstableStates are transient node states that indicate a task was
// interrupted mid-execution and should never appear in a snapshot
// used for fork/redo/resume.
var unstableStates = map[domain.NodeState]bool{
	domain.NodeRunning:  true,
	domain.NodeRetrying: true,
	domain.NodeReady:    true,
}

// checkUnstableNodes flags nodes in transient states. Fork mode additionally
// blocks awaiting nodes because fork cannot inherit external callbacks.
func checkUnstableNodes(
	parentNodes map[string]*domain.NodeRuntime,
	result *ClosureValidationResult,
	mode ClosureValidationMode,
) {
	for name, rt := range parentNodes {
		if rt == nil {
			continue
		}

		if unstableStates[rt.State] {
			result.Issues = append(result.Issues, ClosureValidationIssue{
				Level:     ClosureLevelBlock,
				NodeName:  name,
				FieldName: "state",
				Message:   fmt.Sprintf("unstable node %s is in transient state %s", name, rt.State),
			})
			continue
		}

		// Fork mode: awaiting nodes mean external callbacks are expected
		// that will never arrive for the fork.
		if mode == ClosureModeFork && rt.State == domain.NodeAwaiting {
			result.Issues = append(result.Issues, ClosureValidationIssue{
				Level:     ClosureLevelBlock,
				NodeName:  name,
				FieldName: "state",
				Message:   fmt.Sprintf("fork does not support awaiting node %s — external callback cannot transfer to new task", name),
			})
		}
	}
}

// checkPendingNodeEdges verifies that every pending node has edge decisions
// from all of its terminal parents. A missing edge decision means the parent
// completed but the edge to this child was never computed.
func checkPendingNodeEdges(
	parentNodes map[string]*domain.NodeRuntime,
	dag *graph.Graph,
	result *ClosureValidationResult,
) {
	for name, rt := range parentNodes {
		if rt == nil {
			continue
		}
		if rt.State != domain.NodePending {
			continue
		}

		for _, parentName := range dag.Parents[name] {
			parentNode := parentNodes[parentName]
			if parentNode == nil {
				continue
			}
			if !terminalNodeStates[parentNode.State] {
				continue
			}

			edgeKey := parentName + "->" + name
			if _, ok := parentNode.ActivatedEdges[edgeKey]; !ok {
				result.Issues = append(result.Issues, ClosureValidationIssue{
					Level:     ClosureLevelBlock,
					NodeName:  name,
					EdgeKey:   edgeKey,
					FieldName: "activated_edges",
					Message:   fmt.Sprintf("pending node %s missing edge decision from terminal parent %s (%s)", name, parentName, edgeKey),
				})
			}
		}
	}
}
