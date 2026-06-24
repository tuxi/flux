package tool

import "encoding/json"

// ToolDefinition 是工具的"定义层"——MCP 形状、可序列化、与执行解耦。
// 它是把工具描述给 LLM / MCP 客户端的统一货币（JSON Schema），取代弱的 DataSchema 旁路。
//
// 主线二 阶段 C：之前 MCP 适配器靠临时的 RawInputSchema() 直供原生 schema，planner 内联
// 断言取用。这里把它"转正"——任何工具都可经 DefinedTool 暴露原生定义，本地工具则由
// DefinitionOf 从 DataSchema 合成。本地工具与 MCP 工具的 schema 自此走同一出口。
//
// 注意范围：本阶段只统一"定义"，不动 tool.Tool 接口签名、不动 Execute、不动 workflow 引擎。
type ToolDefinition struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`            // JSON Schema（MCP: inputSchema）
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"` // JSON Schema（MCP: outputSchema）
	Annotations  Annotations     `json:"annotations,omitempty"`
}

// Annotations 携带 MCP 标准提示 + Flux 扩展。本阶段只填 Execution（从 Mode 派生）；
// MCP 的 readOnly/destructive 等提示预留，已知时再填。
type Annotations struct {
	Execution   ExecutionMode `json:"execution,omitempty"`     // Flux 扩展：sync/async
	ReadOnly    bool          `json:"readOnlyHint,omitempty"`  // MCP hint（预留）
	Destructive bool          `json:"destructiveHint,omitempty"`
	Idempotent  bool          `json:"idempotentHint,omitempty"`
	OpenWorld   bool          `json:"openWorldHint,omitempty"`
}

// DefinedTool 是可选接口：原生就持有 JSON Schema 定义的工具实现它（如 MCP 适配器）。
// 实现了它的工具，DefinitionOf 会直接采用其原生定义，不走 DataSchema 合成。
type DefinedTool interface {
	Definition() ToolDefinition
}

// DefinitionOf 是"给我任意工具的 JSON-Schema 定义"的唯一入口（统一出口）：
//   - 工具实现了 DefinedTool（如 MCP 适配器）→ 直接用其原生定义；
//   - 否则 → 从 Name/Description/InputSchema(DataSchema)/Mode 合成一个等价定义。
//
// planner / 未来的 MCP-expose 都只调它，从而本地工具与 MCP 工具被一视同仁。
func DefinitionOf(t Tool) ToolDefinition {
	if dt, ok := t.(DefinedTool); ok {
		return dt.Definition()
	}
	return ToolDefinition{
		Name:         t.Name(),
		Description:  t.Description(),
		InputSchema:  DataSchemaToJSONSchema(t.InputSchema()),
		OutputSchema: DataSchemaToJSONSchema(t.OutputSchema()),
		Annotations:  Annotations{Execution: t.Mode()},
	}
}

// DataSchemaToJSONSchema 把 DataSchema 合成为标准 JSON Schema（object）。
// 这是从 planner 上移到 tool 包的"权威转换"——所有合成走这一处。
// 注意：DataSchema 只一层、无嵌套/枚举/约束，故合成结果也是浅层；这是 DataSchema 的固有局限，
// 原生持有 JSON Schema 的工具（DefinedTool）不受此限。
func DataSchemaToJSONSchema(ds DataSchema) json.RawMessage {
	props := make(map[string]any, len(ds.Fields))
	var required []string
	for name, f := range ds.Fields {
		props[name] = map[string]any{
			"type":        normalizeJSONType(f.Type),
			"description": f.Desc,
		}
		if f.Required {
			required = append(required, name)
		}
	}
	obj := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	b, _ := json.Marshal(obj)
	return b
}

func normalizeJSONType(t string) string {
	switch t {
	case "bool", "boolean":
		return "boolean"
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "array":
		return "array"
	case "object":
		return "object"
	default:
		return "string"
	}
}
