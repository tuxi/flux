package vlm

import "testing"

func TestBuildUsageFacts(t *testing.T) {
	facts := BuildUsageFacts(map[string]any{
		"vlm_provider":      "volcengine",
		"vlm_model":         "doubao-1-5-vision-pro-32k-250115",
		"vlm_request_id":    "req-1",
		"prompt_tokens":     123,
		"completion_tokens": 45,
		"total_tokens":      168,
	})
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %#v", facts)
	}
	if facts[0]["resource_type"] != "vlm" {
		t.Fatalf("unexpected resource type: %#v", facts[0])
	}
	if facts[0]["usage_quantity"] != float64(168) {
		t.Fatalf("unexpected usage quantity: %#v", facts[0])
	}
}
