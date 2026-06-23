package service

import (
	"testing"

	"flux/domain"
	"flux/tool"
	"github.com/stretchr/testify/require"
)

func TestSynthesizeReplayPayload_KeepsDoubaoProvider(t *testing.T) {
	binding := &domain.AwaitBinding{
		Provider:       stringPtr("doubao"),
		ProviderTaskID: stringPtr("task-123"),
		APITaskID:      stringPtr("task-123"),
	}

	payload, eventErr, terminal, err := synthesizeReplayPayload("doubao", binding, &tool.Result{
		Success: true,
		Data: map[string]any{
			"video_url":    "https://example.com/video.mp4",
			"api_task_id":  "task-123",
			"api_provider": "doubao",
		},
	})
	require.NoError(t, err)
	require.True(t, terminal)
	require.Empty(t, eventErr)
	require.Equal(t, "success", getNestedValue(payload, "data", "status"))
	require.Equal(t, "doubao", getNestedValue(payload, "data", "api_provider"))
}

func TestNormalizeProviderWebhookPayload_DoubaoPreservesProvider(t *testing.T) {
	binding := &domain.AwaitBinding{
		Provider:       stringPtr("doubao"),
		ProviderTaskID: stringPtr("task-123"),
		APITaskID:      stringPtr("task-123"),
	}

	output, eventErr, terminal, err := normalizeProviderWebhookPayload("doubao", binding, map[string]any{
		"task_id": "task-123",
		"data": map[string]any{
			"status":       "success",
			"video_url":    "https://example.com/video.mp4",
			"api_provider": "doubao",
		},
	})
	require.NoError(t, err)
	require.True(t, terminal)
	require.Empty(t, eventErr)
	require.Equal(t, "https://example.com/video.mp4", output["video_url"])
	require.Equal(t, "doubao", output["api_provider"])
}

func TestNormalizeProviderWebhookPayload_DoubaoSupportsQueryResponseShape(t *testing.T) {
	binding := &domain.AwaitBinding{
		Provider:       stringPtr("doubao"),
		ProviderTaskID: stringPtr("task-456"),
		APITaskID:      stringPtr("task-456"),
	}

	output, eventErr, terminal, err := normalizeProviderWebhookPayload("doubao", binding, map[string]any{
		"id":     "task-456",
		"status": "succeeded",
		"content": map[string]any{
			"video_url":      "https://example.com/official-shape-video.mp4",
			"last_frame_url": "https://example.com/official-shape-cover.png",
		},
	})
	require.NoError(t, err)
	require.True(t, terminal)
	require.Empty(t, eventErr)
	require.Equal(t, "https://example.com/official-shape-video.mp4", output["video_url"])
	require.Equal(t, "https://example.com/official-shape-cover.png", output["cover_url"])
	require.Equal(t, "doubao", output["api_provider"])
}

func TestNormalizeProviderWebhookPayload_DoubaoSupportsTopLevelErrorObject(t *testing.T) {
	binding := &domain.AwaitBinding{
		Provider:       stringPtr("doubao"),
		ProviderTaskID: stringPtr("task-789"),
		APITaskID:      stringPtr("task-789"),
	}

	output, eventErr, terminal, err := normalizeProviderWebhookPayload("doubao", binding, map[string]any{
		"id":     "task-789",
		"status": "failed",
		"error": map[string]any{
			"message": "generation failed in official shape",
		},
	})
	require.NoError(t, err)
	require.True(t, terminal)
	require.Equal(t, "generation failed in official shape", eventErr)
	require.Equal(t, "doubao", output["api_provider"])
}

func stringPtr(v string) *string {
	return &v
}
