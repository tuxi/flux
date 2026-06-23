package cost

import (
	"testing"

	"flux/tool"
)

func TestValidateUsageFacts(t *testing.T) {
	schema := tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"resource_type":       {Type: "string", Required: true},
			"provider":            {Type: "string", Required: true},
			"usage_quantity":      {Type: "number", Required: true},
			"usage_unit":          {Type: "string", Required: true},
			"billable":            {Type: "bool", Required: true},
			"billable_stage":      {Type: "string", Required: false},
			"usage_breakdown":     {Type: "object", Required: false},
			"provider_request_id": {Type: "string", Required: false},
		},
	}

	err := ValidateUsageFacts([]map[string]any{{
		"resource_type":  "tts",
		"provider":       "edge",
		"usage_quantity": 12,
		"usage_unit":     "chars",
		"billable":       true,
		"billable_stage": "completed",
	}}, schema)
	if err != nil {
		t.Fatalf("expected valid usage facts, got %v", err)
	}
}

func TestParseUsageFacts_OnlyKeepsCompletedBillableFacts(t *testing.T) {
	facts, err := ParseUsageFacts([]map[string]any{
		{
			"resource_type":  "video_generation",
			"provider":       "volcengine",
			"usage_quantity": 1,
			"usage_unit":     "jobs",
			"billable":       true,
			"billable_stage": "submit",
		},
		{
			"resource_type":       "video_generation",
			"provider":            "volcengine",
			"provider_request_id": "task-1",
			"usage_quantity":      1,
			"usage_unit":          "jobs",
			"billable":            true,
			"billable_stage":      "completed",
			"usage_breakdown": map[string]any{
				"duration_seconds": 3.2,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 completed fact, got %#v", facts)
	}
	if facts[0].ProviderRequestID != "task-1" {
		t.Fatalf("unexpected parsed fact: %#v", facts[0])
	}
}
