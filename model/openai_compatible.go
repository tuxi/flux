package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatibleProvider 调用任何 OpenAI-Compatible /chat/completions 端点
// （DeepSeek / Moonshot / 通义 / 自托管 vLLM 等）。M1 只用 Complete（非流式）。
type OpenAICompatibleProvider struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func NewOpenAICompatibleProvider(baseURL, apiKey string) *OpenAICompatibleProvider {
	return &OpenAICompatibleProvider{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

type chatCompletionRequest struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		TotalTokens          int `json:"total_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"` // deepseek
		PromptTokensDetails  struct {
			CachedTokens int `json:"cached_tokens"` // openai-style
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func (p *OpenAICompatibleProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if p.APIKey == "" {
		return Response{}, fmt.Errorf("missing api key")
	}
	if p.BaseURL == "" {
		return Response{}, fmt.Errorf("missing base url")
	}
	if req.Model == "" {
		return Response{}, fmt.Errorf("missing model")
	}

	body := chatCompletionRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.HTTPClient.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}

	// 先按 status 分类：5xx 常返回非 JSON（HTML 错误页），不能被 decode 失败掩盖。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
		var decoded chatCompletionResponse
		if json.Unmarshal(raw, &decoded) == nil && decoded.Error != nil {
			apiErr.Type = decoded.Error.Type
			apiErr.Message = decoded.Error.Message
		}
		return Response{}, apiErr
	}

	var decoded chatCompletionResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode response: %w; raw=%s", err, string(raw))
	}
	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("model api returned no choices: raw=%s", string(raw))
	}

	cached := decoded.Usage.PromptCacheHitTokens
	if cached == 0 {
		cached = decoded.Usage.PromptTokensDetails.CachedTokens
	}

	choice := decoded.Choices[0]
	return Response{
		Content:      strings.TrimSpace(choice.Message.Content),
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:       decoded.Usage.PromptTokens,
			CompletionTokens:   decoded.Usage.CompletionTokens,
			TotalTokens:        decoded.Usage.TotalTokens,
			CachedPromptTokens: cached,
		},
		Raw: raw,
	}, nil
}
