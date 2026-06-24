package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"

	"flux/tool"
)

const (
	serverName    = "flux"
	serverVersion = "0.1.0"
)

// Server 把一个 flux/tool.Registry 暴露成 MCP server —— stage A(consume) 的对称面。
// 让 Flux 的工具能被 Claude Code / Codex 等 MCP 客户端调用。
//
// 范围同 consume：仅 stdio、仅 tools 切面（initialize / tools/list / tools/call）。
// 请求串行处理（一个 Execute 跑完再读下一条）—— M1 够用，不引入并发。
type Server struct {
	reg     *tool.Registry
	writeMu sync.Mutex // 串行化 out 写
}

func NewServer(reg *tool.Registry) *Server { return &Server{reg: reg} }

// ServeStdio = Serve(ctx, os.Stdin, os.Stdout)，给真实子进程入口（cmd/flux-mcp）用。
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

// Serve 在 in/out 上跑 JSON-RPC 循环，直到 in 关闭（EOF）或 ctx 取消。
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			s.handle(ctx, trimmed, out)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

type rpcIncoming struct {
	ID     json.RawMessage `json:"id"` // 缺省=通知；原样回显（可能是 number 或 string）
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (s *Server) handle(ctx context.Context, line []byte, out io.Writer) {
	var req rpcIncoming
	if json.Unmarshal(line, &req) != nil {
		return // 无法解析且 id 未知，忽略
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	switch req.Method {
	case "initialize":
		s.reply(out, req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		})
	case "notifications/initialized":
		// 通知，无响应
	case "tools/list":
		s.reply(out, req.ID, map[string]any{"tools": s.listTools()})
	case "tools/call":
		s.reply(out, req.ID, s.callTool(ctx, req.Params))
	default:
		if !isNotification {
			s.replyError(out, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

// listTools 用 tool.DefinitionOf 产出每个工具的 JSON Schema —— 这正是 stage C 的回报：
// 本地工具（DataSchema 合成）与未来嵌套的工具都经同一出口，对外暴露统一的 MCP 定义。
func (s *Server) listTools() []ToolInfo {
	tools := s.reg.List()
	out := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		d := tool.DefinitionOf(t)
		schema := d.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, ToolInfo{Name: d.Name, Description: d.Description, InputSchema: schema})
	}
	return out
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) CallToolResult {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if json.Unmarshal(params, &p) != nil {
		return errorResult("invalid tools/call params")
	}
	t, ok := s.reg.Get(p.Name)
	if !ok {
		return errorResult("tool not found: " + p.Name)
	}
	res, err := t.Execute(ctx, p.Arguments, nil)
	if err != nil {
		// 工具执行错误按 MCP 约定走 isError 结果，不是 JSON-RPC 协议错误。
		return errorResult(err.Error())
	}
	if res == nil {
		return CallToolResult{Content: []ContentBlock{{Type: "text", Text: ""}}}
	}
	if !res.Success {
		return errorResult(res.Error)
	}
	body, _ := json.Marshal(res.Data)
	return CallToolResult{Content: []ContentBlock{{Type: "text", Text: string(body)}}}
}

func errorResult(msg string) CallToolResult {
	return CallToolResult{IsError: true, Content: []ContentBlock{{Type: "text", Text: msg}}}
}

func (s *Server) reply(out io.Writer, id json.RawMessage, result any) {
	raw, _ := json.Marshal(result)
	s.write(out, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": json.RawMessage(raw)})
}

func (s *Server) replyError(out io.Writer, id json.RawMessage, code int, msg string) {
	s.write(out, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": map[string]any{"code": code, "message": msg}})
}

func rawID(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return id
}

func (s *Server) write(out io.Writer, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	_, _ = out.Write(data)
	s.writeMu.Unlock()
}
