package runtimekeys

import "testing"

type sampleImageRole struct {
	Role           string   `json:"role"`
	Reason         string   `json:"reason"`
	ImageURL       string   `json:"image_url"`
	IsPrimary      bool     `json:"is_primary"`
	RoleCandidates []string `json:"role_candidates"`
}

type sampleAnalyzeInput struct {
	Index              int               `json:"index"`
	ImageItem          string            `json:"image_item"`
	ImageRoles         []sampleImageRole `json:"image_roles"`
	ProductName        any               `json:"product_name"`
	PrimaryImage       string            `json:"primary_image"`
	ProductImages      []string          `json:"product_images"`
	MapItemHash        string            `json:"__map_item_hash"`
	ProductDescription any               `json:"product_description"`
}

func TestBuildSubWorkflowKey_NormalizesEquivalentInputs(t *testing.T) {
	structInput := map[string]any{
		"index": 0,
		"image_item": "https://example.com/833c0b.jpg",
		"image_roles": []sampleImageRole{
			{
				Role:           "hero",
				Reason:         "primary image defaults to hero",
				ImageURL:       "https://example.com/630622.jpg",
				IsPrimary:      true,
				RoleCandidates: []string{"hero", "detail"},
			},
			{
				Role:           "other",
				Reason:         "no strong keyword matched, fallback to other",
				ImageURL:       "https://example.com/833c0b.jpg",
				IsPrimary:      false,
				RoleCandidates: []string{"other", "detail"},
			},
		},
		"product_name":        nil,
		"primary_image":       "https://example.com/630622.jpg",
		"product_images":      []string{"https://example.com/630622.jpg", "https://example.com/833c0b.jpg"},
		"__map_item_hash":     "fd9525baf4278d4e6870746a37925ff550e56a7df977ce541354799b273cd6bf",
		"product_description": nil,
	}

	mapInput := map[string]any{
		"index": 0,
		"image_item": "https://example.com/833c0b.jpg",
		"image_roles": []any{
			map[string]any{
				"role":            "hero",
				"reason":          "primary image defaults to hero",
				"image_url":       "https://example.com/630622.jpg",
				"is_primary":      true,
				"role_candidates": []any{"hero", "detail"},
			},
			map[string]any{
				"role":            "other",
				"reason":          "no strong keyword matched, fallback to other",
				"image_url":       "https://example.com/833c0b.jpg",
				"is_primary":      false,
				"role_candidates": []any{"other", "detail"},
			},
		},
		"product_name":        nil,
		"primary_image":       "https://example.com/630622.jpg",
		"product_images":      []any{"https://example.com/630622.jpg", "https://example.com/833c0b.jpg"},
		"__map_item_hash":     "fd9525baf4278d4e6870746a37925ff550e56a7df977ce541354799b273cd6bf",
		"product_description": nil,
	}

	keyA := BuildSubWorkflowKey(2048682749497196544, "analyze_product_images_multi", "analyze_product_image_flow", structInput)
	keyB := BuildSubWorkflowKey(2048682749497196544, "analyze_product_images_multi", "analyze_product_image_flow", mapInput)

	if keyA != keyB {
		t.Fatalf("expected equivalent inputs to generate same sub key\nA=%s\nB=%s", keyA, keyB)
	}
}

