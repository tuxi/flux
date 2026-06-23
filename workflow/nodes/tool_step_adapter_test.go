package nodes

import (
	"context"
	"testing"

	"flux/tool"
)

type fakeUsageTool struct{}

func (f *fakeUsageTool) Name() string                  { return "fake_usage_tool" }
func (f *fakeUsageTool) Description() string           { return "fake usage tool" }
func (f *fakeUsageTool) InputSchema() tool.DataSchema  { return tool.DataSchema{} }
func (f *fakeUsageTool) OutputSchema() tool.DataSchema { return tool.DataSchema{} }
func (f *fakeUsageTool) Mode() tool.ExecutionMode      { return tool.SyncExecution }
func (f *fakeUsageTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	_ = ctx
	_ = emitter
	return &tool.Result{Data: map[string]any{"ok": true}}, nil
}
func (f *fakeUsageTool) UsageSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"resource_type":  {Type: "string", Required: true},
			"provider":       {Type: "string", Required: true},
			"usage_quantity": {Type: "number", Required: true},
			"usage_unit":     {Type: "string", Required: true},
			"billable":       {Type: "bool", Required: true},
		},
	}
}
func (f *fakeUsageTool) BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error) {
	_ = input
	_ = output
	return []map[string]any{{
		"resource_type":  "tts",
		"provider":       "edge",
		"usage_quantity": 12,
		"usage_unit":     "chars",
		"billable":       true,
		"billable_stage": "completed",
	}}, nil
}

func TestToolStepAdapter_ImplementsUsageAwareStepWhenToolSupportsIt(t *testing.T) {
	step := NewToolStepAdapter(&fakeUsageTool{}, nil, "v1")

	aware, ok := step.(UsageAwareStep)
	if !ok {
		t.Fatalf("expected tool step adapter to implement UsageAwareStep")
	}
	facts, err := aware.BuildUsageFacts(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0]["provider"] != "edge" {
		t.Fatalf("unexpected facts: %#v", facts)
	}
}

func TestToolStepAdapter_RetryPolicyRespectsConfig(t *testing.T) {
	step := NewToolStepAdapter(&fakeUsageTool{}, map[string]any{
		"retry_count":       3,
		"retry_interval_ms": 1500,
	}, "v1")

	policy := step.RetryPolicy()
	if policy.MaxRetries != 3 {
		t.Fatalf("MaxRetries = %d, want 3", policy.MaxRetries)
	}
	if policy.Interval.Milliseconds() != 1500 {
		t.Fatalf("Interval = %dms, want 1500ms", policy.Interval.Milliseconds())
	}
}
