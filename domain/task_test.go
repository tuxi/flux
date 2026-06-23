package domain

import (
	"encoding/json"
	"testing"
)

func TestParseFinalLegacyImageOutput(t *testing.T) {
	output := map[string]any{
		"image_url": "https://example.com/result.png",
		"cover_url": "https://example.com/result-cover.png",
	}

	bs, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}

	got, err := ParseFinal(bs)
	if err != nil {
		t.Fatalf("ParseFinal returned error: %v", err)
	}
	if got == nil {
		t.Fatal("ParseFinal returned nil result")
	}
	if got.ResultType != "image" {
		t.Fatalf("expected image result type, got %q", got.ResultType)
	}
	if got.PrimaryFileUrl != "https://example.com/result.png" {
		t.Fatalf("unexpected primary file url: %q", got.PrimaryFileUrl)
	}
}

func TestParseFinalLegacyVideoOutput(t *testing.T) {
	output := map[string]any{
		"video_url": "https://example.com/result.mp4",
		"cover_url": "https://example.com/result-cover.jpg",
	}

	bs, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}

	got, err := ParseFinal(bs)
	if err != nil {
		t.Fatalf("ParseFinal returned error: %v", err)
	}
	if got == nil {
		t.Fatal("ParseFinal returned nil result")
	}
	if got.ResultType != "video" {
		t.Fatalf("expected video result type, got %q", got.ResultType)
	}
	if got.PrimaryFileUrl != "https://example.com/result.mp4" {
		t.Fatalf("unexpected primary file url: %q", got.PrimaryFileUrl)
	}
}
