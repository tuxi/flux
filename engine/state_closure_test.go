package engine

import (
	"testing"

	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
)

// testDAG creates a simple A->B->C DAG for validation tests.
func testDAG() *graph.Graph {
	def := &definition.WorkflowDefinition{
		Name: "test",
		Nodes: []definition.NodeDefinition{
			{Name: "A", Type: definition.NodeTool},
			{Name: "B", Type: definition.NodeTool},
			{Name: "C", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "A", To: "B", Type: definition.EdgeNormal},
			{From: "B", To: "C", Type: definition.EdgeNormal},
		},
	}
	g, _ := graph.Build(def)
	return g
}

// branchDAG creates A->B, A->C (branching from A).
func branchDAG() *graph.Graph {
	def := &definition.WorkflowDefinition{
		Name: "branch",
		Nodes: []definition.NodeDefinition{
			{Name: "A", Type: definition.NodeTool},
			{Name: "B", Type: definition.NodeTool},
			{Name: "C", Type: definition.NodeTool},
		},
		Edges: []definition.EdgeDefinition{
			{From: "A", To: "B", Type: definition.EdgeNormal},
			{From: "A", To: "C", Type: definition.EdgeNormal},
		},
	}
	g, _ := graph.Build(def)
	return g
}

func TestValidateParentStateClosure_AllSuccessCompleteEdges_Fork(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"B->C": true}},
		"C": {Name: "C", State: domain.NodeSuccess},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid, got issues: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_TerminalNodeMissingEdge_Fork(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeSuccess}, // missing B->C edge
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid, got valid")
	}
	if len(result.Issues) != 2 {
		t.Fatalf("expected 2 issues, got %d: %+v", len(result.Issues), result.Issues)
	}
	// First issue: B missing edge
	if result.Issues[0].NodeName != "B" {
		t.Fatalf("expected issue on node B, got %s", result.Issues[0].NodeName)
	}
	if result.Issues[0].EdgeKey != "B->C" {
		t.Fatalf("expected edge B->C, got %s", result.Issues[0].EdgeKey)
	}
	// Second issue: C pending with undecided parent edge
	if result.Issues[1].NodeName != "C" {
		t.Fatalf("expected issue on node C, got %s", result.Issues[1].NodeName)
	}
}

func TestValidateParentStateClosure_RunningNode_Fork(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeRunning},
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid for running node")
	}
	foundUnstable := false
	for _, issue := range result.Issues {
		if issue.NodeName == "B" && issue.FieldName == "state" {
			foundUnstable = true
			break
		}
	}
	if !foundUnstable {
		t.Fatalf("expected unstable issue for running node B, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_RunningNode_Resume(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeRunning},
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeResume)
	if result.Valid {
		t.Fatal("expected invalid even in resume mode for running node")
	}
}

func TestValidateParentStateClosure_AwaitingNode_Fork(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeAwaiting},
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid for awaiting node in fork mode")
	}
	foundAwaiting := false
	for _, issue := range result.Issues {
		if issue.NodeName == "B" && issue.FieldName == "state" {
			foundAwaiting = true
			break
		}
	}
	if !foundAwaiting {
		t.Fatalf("expected issue for awaiting node B in fork mode, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_AwaitingNode_Resume_Allowed(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeAwaiting},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeResume)
	if !result.Valid {
		t.Fatalf("expected valid for awaiting node in resume mode, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_RetryingNode_Blocked(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeRetrying},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid for retrying node")
	}
}

func TestValidateParentStateClosure_ReadyNode_Blocked(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeReady},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid for ready node")
	}
}

func TestValidateParentStateClosure_FailedNodeMissingEdge(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeFailed}, // failed node should still have edge decisions
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid for failed node missing edge")
	}
	if result.Issues[0].NodeName != "B" {
		t.Fatalf("expected issue on node B, got %s", result.Issues[0].NodeName)
	}
}

func TestValidateParentStateClosure_PendingWithUndecidedParentEdge(t *testing.T) {
	branch := branchDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}}, // missing A->C
		"B": {Name: "B", State: domain.NodeSuccess},
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, branch, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid for pending node with undecided parent edge")
	}
	hasEdgeIssue := false
	for _, issue := range result.Issues {
		if issue.NodeName == "C" && issue.EdgeKey == "A->C" {
			hasEdgeIssue = true
			break
		}
	}
	if !hasEdgeIssue {
		t.Fatalf("expected issue for C missing edge A->C, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_SuccessPendingEdges_Valid(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeSuccessPendingEdges, ActivatedEdges: map[string]bool{"B->C": true}},
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid for success_pending_edges with complete edges, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_FailedPendingEdges_ValidWithEdges(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeFailedPendingEdges, ActivatedEdges: map[string]bool{"B->C": false}},
		"C": {Name: "C", State: domain.NodeSkipped},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid for failed_pending_edges with edges decided, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_BranchWithPartialEdgeClosure(t *testing.T) {
	branch := branchDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true, "A->C": false}},
		"B": {Name: "B", State: domain.NodeSuccess},
		"C": {Name: "C", State: domain.NodeSkipped},
	}

	result := ValidateParentStateClosure(nodes, branch, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid for branch with all edges decided, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_AllSuccess_Resume(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"B->C": true}},
		"C": {Name: "C", State: domain.NodeSuccess},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeResume)
	if !result.Valid {
		t.Fatalf("expected valid for all-success in resume mode, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_EmptyNodes(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid for empty nodes, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_NilNodeSkipped(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": nil,
		"C": {Name: "C", State: domain.NodePending},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid when nil nodes are skipped, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_NoOutgoingEdges(t *testing.T) {
	// C is a leaf node with no outgoing edges — it doesn't need ActivatedEdges
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"B->C": true}},
		"C": {Name: "C", State: domain.NodeSuccess},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid for leaf node without edges, got: %+v", result.Issues)
	}
}

func TestValidateParentStateClosure_MultipleBlockingIssues(t *testing.T) {
	// A->B->C DAG. A is running (unstable), B is success but missing edge to C.
	// C is pending with undecided terminal parent B.
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeRunning}, // issue: unstable
		"B": {Name: "B", State: domain.NodeSuccess}, // issue: missing B->C edge
		"C": {Name: "C", State: domain.NodePending}, // issue: pending, terminal parent B hasn't decided edge
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid with multiple issues")
	}
	if len(result.Issues) < 2 {
		t.Fatalf("expected at least 2 issues, got %d: %+v", len(result.Issues), result.Issues)
	}
	// Verify all issues are block level
	for _, issue := range result.Issues {
		if issue.Level != ClosureLevelBlock {
			t.Fatalf("expected all block-level issues, got %s for %s", issue.Level, issue.Message)
		}
	}
}

func TestValidateParentStateClosure_SkippedNodeWithOutgoingEdges(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeSkipped}, // skipped but has outgoing edge B->C not decided
		"C": {Name: "C", State: domain.NodeSkipped},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if result.Valid {
		t.Fatal("expected invalid for skipped node missing edge decision")
	}
	if result.Issues[0].NodeName != "B" {
		t.Fatalf("expected issue on node B, got %s", result.Issues[0].NodeName)
	}
}

func TestValidateParentStateClosure_CanceledNodeWithEdgesDecided(t *testing.T) {
	dag := testDAG()
	nodes := map[string]*domain.NodeRuntime{
		"A": {Name: "A", State: domain.NodeSuccess, ActivatedEdges: map[string]bool{"A->B": true}},
		"B": {Name: "B", State: domain.NodeCanceled, ActivatedEdges: map[string]bool{"B->C": false}},
		"C": {Name: "C", State: domain.NodeSkipped},
	}

	result := ValidateParentStateClosure(nodes, dag, ClosureModeFork)
	if !result.Valid {
		t.Fatalf("expected valid for canceled node with edges decided, got: %+v", result.Issues)
	}
}
