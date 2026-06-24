package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// protocolVersion：发给 server 的协议版本。选用广泛兼容的稳定版；
// server 会在 initialize 响应里回它实际采用的版本，我们记录但不强校验。
const protocolVersion = "2024-11-05"

// Client 是一个 MCP server 的会话（tools 切面）。
type Client struct {
	t Transport

	ServerName      string
	ServerVersion   string
	ProtocolVersion string
}

// ToolInfo 是 server 暴露的一个工具的元数据。
// InputSchema 原样保留 JSON Schema —— 这正是要喂给 LLM 的形状（MCP 自带，无需翻译）。
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// CallToolResult 是 tools/call 的返回。
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

// ContentBlock：MCP 的内容块（text/image/resource...）。M1 只重点用 text；
// 其余类型保留 Raw，等主线二 C 阶段做完整 content 映射（诚实标注的有损点）。
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Text 把所有 text 块拼起来 —— 适配 tool.Result.Data 时用。
func (r *CallToolResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// NewStdioClient 启动一个 stdio MCP server 子进程并完成 initialize 握手。
//
//	c, err := mcp.NewStdioClient(ctx, "npx",
//	    []string{"-y", "@modelcontextprotocol/server-filesystem", dir}, nil)
func NewStdioClient(ctx context.Context, command string, args, env []string) (*Client, error) {
	t, err := newStdioTransport(command, args, env)
	if err != nil {
		return nil, err
	}
	c := &Client{t: t}
	if err := c.initialize(ctx); err != nil {
		_ = t.Close()
		return nil, err
	}
	return c, nil
}

// NewClient 用一个已有 Transport 构造 Client（便于将来接 HTTP/SSE，或测试注入假 transport）。
func NewClient(ctx context.Context, t Transport) (*Client, error) {
	c := &Client{t: t}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

func (c *Client) initialize(ctx context.Context) error {
	res, err := c.t.Call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "flux", "version": "0.1.0"},
	})
	if err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}
	var ir initializeResult
	if err := json.Unmarshal(res, &ir); err != nil {
		return fmt.Errorf("mcp initialize decode: %w", err)
	}
	c.ServerName = ir.ServerInfo.Name
	c.ServerVersion = ir.ServerInfo.Version
	c.ProtocolVersion = ir.ProtocolVersion

	// 必须发 initialized 通知，server 才认为握手完成。
	if err := c.t.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("mcp initialized notify: %w", err)
	}
	return nil
}

type listToolsResult struct {
	Tools []ToolInfo `json:"tools"`
}

// ListTools 返回 server 暴露的工具（M1 不处理分页 cursor —— filesystem 等小 server 一页够）。
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	res, err := c.t.Call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var lr listToolsResult
	if err := json.Unmarshal(res, &lr); err != nil {
		return nil, fmt.Errorf("mcp tools/list decode: %w", err)
	}
	return lr.Tools, nil
}

// CallTool 调用一个工具。注意：工具内部失败由 CallToolResult.IsError 表达，
// 不是这里返回的 error —— error 仅代表传输/协议层失败。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	res, err := c.t.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}
	var ctr CallToolResult
	if err := json.Unmarshal(res, &ctr); err != nil {
		return nil, fmt.Errorf("mcp tools/call decode: %w", err)
	}
	return &ctr, nil
}

func (c *Client) Close() error { return c.t.Close() }
