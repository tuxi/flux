package memory

import (
	"context"
	"testing"

	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/store"
)

func TestWorkflowStore_CRUD(t *testing.T) {
	s := NewWorkflowStore()
	ctx := context.Background()

	// Create
	run, err := s.CreateRun(ctx, store.RunMeta{ID: "run-1", Goal: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "run-1" {
		t.Fatalf("expected run-1, got %s", run.ID)
	}

	// Load
	loaded, err := s.LoadRun(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Goal != "test" {
		t.Fatalf("expected 'test', got %s", loaded.Goal)
	}

	// Update status
	if err := s.UpdateRunStatus(ctx, "run-1", "completed"); err != nil {
		t.Fatal(err)
	}

	// Task
	task, err := s.CreateTask(ctx, "run-1", store.TaskMeta{ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "task-1" {
		t.Fatalf("expected task-1, got %s", task.ID)
	}

	// Node
	if err := s.PersistNode(ctx, "task-1", "node-a", runtime.NodeSuccess, map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.LoadNodeStates(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].NodeName != "node-a" || nodes[0].State != runtime.NodeSuccess {
		t.Fatalf("unexpected node record: %+v", nodes[0])
	}

	// Plan
	plan := &runtime.Plan{Nodes: map[string]*runtime.PlanNode{
		"n1": {Name: "n1", ToolName: "echo"},
	}}
	if err := s.SavePlan(ctx, "task-1", plan); err != nil {
		t.Fatal(err)
	}
	loadedPlan, err := s.LoadPlan(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedPlan.Nodes) != 1 {
		t.Fatalf("expected 1 plan node, got %d", len(loadedPlan.Nodes))
	}
}

func TestAwaitStore_CreateResolve(t *testing.T) {
	s := NewAwaitStore()
	ctx := context.Background()

	b := store.AwaitBinding{
		BindingID:      "b-1",
		TaskID:         "task-1",
		NodeName:       "async-node",
		ProviderTaskID: "provider-123",
		Status:         store.AwaitStatusAwaiting,
	}
	if err := s.CreateBinding(ctx, b); err != nil {
		t.Fatal(err)
	}

	// Find by provider task ID
	found, err := s.FindByProviderTaskID(ctx, "provider-123")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected binding, got nil")
	}
	if found.BindingID != "b-1" {
		t.Fatalf("expected b-1, got %s", found.BindingID)
	}

	// Resolve — first time should succeed
	claimed, err := s.ResolveBinding(ctx, "b-1")
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected claim=true on first resolve")
	}

	// Resolve — second time should be idempotent
	claimed, err = s.ResolveBinding(ctx, "b-1")
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("expected claim=false on second resolve (idempotent)")
	}

	// List pending — should be empty now
	pending, err := s.ListPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending, got %d", len(pending))
	}
}

func TestTraceStore_AppendReplay(t *testing.T) {
	s := NewTraceStore()
	ctx := context.Background()

	events := []runtime.TraceEvent{
		{RunID: "run-1", Seq: 1, Class: runtime.ClassExecution, Node: "n1", Type: runtime.TraceNodeStart},
		{RunID: "run-1", Seq: 2, Class: runtime.ClassExecution, Node: "n1", Type: runtime.TraceOutput, Payload: map[string]any{"result": "ok"}},
	}
	if err := s.AppendTrace(ctx, "task-1", events); err != nil {
		t.Fatal(err)
	}

	// Replay from seq 0
	all, err := s.ReplayTrace(ctx, "task-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 events, got %d", len(all))
	}

	// Replay from seq 1
	after, err := s.ReplayTrace(ctx, "task-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 event after seq 1, got %d", len(after))
	}
}
