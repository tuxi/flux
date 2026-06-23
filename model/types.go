// Package model 提供 OpenAI-Compatible 模型调用（DeepSeek / 月之暗面 / 通义 / 自托管 等）。
//
// 范围：M1 只需要 Complete（非流式 + tool_calls）。后续如需流式，可平移用户既有的
// CompleteStream 实现（同一组类型）。
package model

import "fmt"

// Message 一条对话消息。role: system / user / assistant / tool。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // assistant 消息携带模型决定要调的工具
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool 消息：对应哪一次工具调用
	Name       string     `json:"name,omitempty"`
}

// ToolCall 模型一次工具调用（assistant 消息里的 tool_calls 元素）。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // 目前固定 "function"
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字符串
}

// ToolDefinition 我们告诉模型可用的工具。
type ToolDefinition struct {
	Type     string         `json:"type"` // "function"
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema（type=object + properties + required）
}

type Request struct {
	Model       string           `json:"-"`
	Messages    []Message        `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
}

type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	Raw          []byte // 调试用；不必常驻
}

type Usage struct {
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	CachedPromptTokens int
}

// APIError 携带 HTTP 状态码 + 上游 body —— 调用方据此决定重试/退避策略。
type APIError struct {
	StatusCode int
	Type       string
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("api error %d (%s): %s", e.StatusCode, e.Type, e.Message)
	}
	return fmt.Sprintf("api error %d: %s", e.StatusCode, truncate(e.Body, 500))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
