package mcp

import (
	"context"
	"encoding/json"

	"flux/tool"
)

// ToolAdapter 把一个 MCP 工具包装成 flux/tool.Tool，使其能注册进 tool.Registry，
// 让 planner 像调本地工具一样调它。
//
// 名字双轨：name 是注册/展示给 LLM 的名字（可带前缀防撞），serverName 是调 server 用的原名。
type ToolAdapter struct {
	client     *Client
	serverName string // 调 server 用
	name       string // 注册 + 给 LLM 看（可带前缀）
	info       ToolInfo
}

// 编译期确认实现 tool.Tool。
var _ tool.Tool = (*ToolAdapter)(nil)

func (a *ToolAdapter) Name() string        { return a.name }
func (a *ToolAdapter) Description() string  { return a.info.Description }
func (a *ToolAdapter) Mode() tool.ExecutionMode { return tool.SyncExecution }

// InputSchema 是把 MCP 的 JSON Schema 有损压成 DataSchema（满足 tool.Tool 接口）。
// planner 实际上会优先用 RawInputSchema() 拿原生 JSON Schema，不走这条有损路径。
func (a *ToolAdapter) InputSchema() tool.DataSchema {
	return jsonSchemaToDataSchema(a.info.InputSchema)
}

// OutputSchema：MCP tools/list 不带 output schema，返回空。
func (a *ToolAdapter) OutputSchema() tool.DataSchema { return tool.DataSchema{} }

// RawInputSchema 直供 MCP 原生 JSON Schema（可选接口；planner 优先用它，避免有损往返）。
// 这是主线二 C 阶段"定义层向 MCP 看齐"的接缝。
func (a *ToolAdapter) RawInputSchema() json.RawMessage { return a.info.InputSchema }

// Execute 转译到 MCP tools/call。
//
// 关键语义（与本地 compile 工具一致）：MCP 工具内部错误（CallToolResult.IsError）
// 是给 planner 看的**反馈**，不是 run 终结错误——所以 Success 仍为 true，把 content+isError
// 放进 Data。只有传输/协议层失败才返回 Go error（真基础设施故障）。
//
// TODO(主线二 C)：MCP content 是 text/image/resource 块数组，这里压成 {content:text,isError}
// 是有损的；多模态/资源块需要在 C 阶段做完整 content 映射。
func (a *ToolAdapter) Execute(ctx context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	res, err := a.client.CallTool(ctx, a.serverName, input)
	if err != nil {
		return nil, err
	}
	return tool.Success(map[string]any{
		"content": res.Text(),
		"isError": res.IsError,
	}), nil
}

// RegisterAll 列出 server 的所有工具并注册进 registry（带可选前缀防与本地工具撞名，
// 如 filesystem 的 write_file 会撞本地 write_file）。返回注册后的名字列表。
func RegisterAll(ctx context.Context, client *Client, reg *tool.Registry, prefix string) ([]string, error) {
	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	registered := make([]string, 0, len(tools))
	for _, info := range tools {
		a := &ToolAdapter{
			client:     client,
			serverName: info.Name,
			name:       prefix + info.Name,
			info:       info,
		}
		reg.Register(a)
		registered = append(registered, a.name)
	}
	return registered, nil
}

// jsonSchemaToDataSchema 把 MCP JSON Schema 顶层 properties 有损压成 DataSchema。
func jsonSchemaToDataSchema(raw json.RawMessage) tool.DataSchema {
	if len(raw) == 0 {
		return tool.DataSchema{}
	}
	var s struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return tool.DataSchema{}
	}
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	fields := make(map[string]tool.FieldSchema, len(s.Properties))
	for name, p := range s.Properties {
		fields[name] = tool.FieldSchema{Type: p.Type, Required: req[name], Desc: p.Description}
	}
	return tool.DataSchema{Fields: fields}
}
