// Package mcp 是 Flux 的 MCP（Model Context Protocol）接入层。
//
// 主线二 阶段 A（consume）：Flux 作为 MCP client，连上 MCP server，把它的工具
// 接进 flux/tool.Registry，让 planner 像调本地工具一样调远端工具。
//
// 范围（M1 阶段，故意收窄）：
//   - 仅 stdio transport（子进程 + 换行分隔 JSON-RPC）；HTTP/SSE 只预留 Transport 接口位。
//   - 仅 tools 切面：initialize / tools/list / tools/call。
//     不做 resources / prompts / sampling / roots / progress / cancellation。
package mcp

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 帧。MCP over stdio = 每行一条 JSON-RPC 消息。

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"` // 指针：通知/无 id 帧 → nil，便于过滤
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorBody   `json:"error,omitempty"`
}

type rpcErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// RPCError 是上游 server 返回的 JSON-RPC error，暴露给调用方。
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}
