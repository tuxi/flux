// flux-mcp-server 将 Flux Workflow Engine 暴露为 MCP (Model Context Protocol) 服务。
//
// 任何支持 MCP 的 Agent（Claude Code、Codex、code-agent 等）都可以通过标准 MCP 协议
// 调用 plan_workflow 工具来生成并执行多步 DAG。
//
// 用法：
//
//	flux-mcp-server
//
// 通过 stdin/stdout 使用 JSON-RPC 2.0 与 MCP client 通信。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/tuxi/flux/model"
	"github.com/tuxi/flux/planner"
	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "flux-mcp-server: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	llmProvider := &model.OpenAICompatibleProvider{
		BaseURL:    envDefault("LLM_BASE_URL", "https://api.deepseek.com/v1"),
		APIKey:     os.Getenv("LLM_API_KEY"),
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	}
	modelName := envDefault("LLM_MODEL", "deepseek-chat")

	reg := tool.NewRegistry()
	// 注册 DAG 节点可用的工具：shell 执行命令，merge_result 合并结果。
	wd, _ := os.Getwd()
	reg.Register(builtin.NewShellTool(wd))
	reg.Register(builtin.NewMergeResultTool())
	// 商品视频生成工具 — Agent 可自主编排 DAG
	registerGoodsTools(reg)

	s := &server{
		reader:    bufio.NewReader(os.Stdin),
		writer:    os.Stdout,
		tools:     reg,
		llm:       llmProvider,
		modelName: modelName,
	}
	return s.serve(ctx)
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ── MCP JSON-RPC types ──

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── MCP protocol types ──

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Tools *struct{} `json:"tools,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type listToolsResult struct {
	Tools []toolDef `json:"tools"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ── server ──

type server struct {
	reader      *bufio.Reader
	writer      io.Writer
	tools       *tool.Registry
	llm         *model.OpenAICompatibleProvider
	modelName   string
	initialized bool
}

func (s *server) serve(ctx context.Context) error {
	dec := json.NewDecoder(s.reader)
	enc := json.NewEncoder(s.writer)

	for {
		var req jsonrpcRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("decode: %w", err)
		}

		// JSON-RPC notification（无 id）→ 不应回复
		if req.ID == nil {
			continue
		}

		resp := s.handle(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("encode: %w", err)
		}
	}
}

func (s *server) handle(ctx context.Context, req jsonrpcRequest) jsonrpcResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("unknown method: %s", req.Method)},
		}
	}
}

func (s *server) handleInitialize(req jsonrpcRequest) jsonrpcResponse {
	s.initialized = true
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: capabilities{
				Tools: &struct{}{},
			},
			ServerInfo: serverInfo{
				Name:    "flux-workflow-engine",
				Version: "3.0.0",
			},
		},
	}
}

func (s *server) handleToolsList(req jsonrpcRequest) jsonrpcResponse {
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: listToolsResult{
			Tools: []toolDef{
				{
					Name:        "plan_workflow",
					Description: "给定目标和可用工具目录，生成并执行一个多步 Workflow DAG。Flux Engine 会将目标编译为一张有向无环图（DAG），进行依赖求解和并行执行，并返回最终结果。",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"goal": map[string]any{
								"type":        "string",
								"description": "要完成的目标描述",
							},
						},
						"required": []string{"goal"},
					},
				},
			},
		},
	}
}

func (s *server) handleToolsCall(ctx context.Context, req jsonrpcRequest) jsonrpcResponse {
	var params callToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonrpcResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)},
		}
	}

	if params.Name != "plan_workflow" {
		return jsonrpcResponse{
			JSONRPC: "2.0", ID: req.ID,
			Result: callToolResult{
				Content: []contentItem{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", params.Name)}},
				IsError: true,
			},
		}
	}

	goal, _ := params.Arguments["goal"].(string)
	if goal == "" {
		return jsonrpcResponse{
			JSONRPC: "2.0", ID: req.ID,
			Result: callToolResult{
				Content: []contentItem{{Type: "text", Text: "missing required argument: goal"}},
				IsError: true,
			},
		}
	}

	// 使用 DAGPlanner + Scheduler 执行
	result := s.executePlan(ctx, goal)

	return jsonrpcResponse{
		JSONRPC: "2.0", ID: req.ID,
		Result: callToolResult{
			Content: []contentItem{{Type: "text", Text: result}},
		},
	}
}

// executePlan 使用 DAGPlanner 生成 DAG 并通过 Scheduler 执行。
// 当前实现使用内置工具集。未来可通过 MCP tools/list → tools/call 间接调用宿主 Agent 的工具。
func (s *server) executePlan(ctx context.Context, goal string) string {
	// 注意：当前使用空 Registry。plan_workflow 的能力取决于注册了哪些工具。
	// 在完整部署中，工具由 flux 的 tool.Registry（Provider 工具：图片/视频/TTS 等）提供。
	p := planner.NewDAGPlanner(s.llm, s.modelName, goal, s.tools)
	plan, err := p.Generate(ctx)
	if err != nil {
		return fmt.Sprintf("DAG 生成失败: %v", err)
	}

	state := runtime.NewMemState(map[string]any{"goal": goal})
	sched := runtime.NewScheduler(
		planner.NewToolInvoker(s.tools),
		planner.NopAwait{},
		planner.NopStore{},
		planner.NopEmitter{},
	)

	res, err := sched.Run(ctx, runtime.NewStaticSource(plan), state)
	if err != nil {
		return fmt.Sprintf("DAG 执行失败: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		return fmt.Sprintf("DAG 未完成: status=%d", res.Status)
	}

	var output string
	for _, name := range state.Nodes() {
		if out := state.Output(name); out != nil {
			b, _ := json.MarshalIndent(out, "", "  ")
			output += fmt.Sprintf("Node %s:\n%s\n", name, string(b))
		}
	}
	if output == "" {
		output = "DAG 执行完成（无输出节点）"
	}
	return output
}
