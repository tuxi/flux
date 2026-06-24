package tool_test

// 阶段 C 单元测试（hermetic，无网络/无 LLM）：验证统一定义出口 tool.DefinitionOf——
//   - 本地工具（只有 DataSchema）→ 合成出合法 JSON Schema；
//   - DefinedTool（原生持有 JSON Schema）→ 原样直供，不被合成覆盖。

import (
	"context"
	"encoding/json"
	"testing"

	"flux/tool"
)

// dataSchemaTool 只用 DataSchema 描述自己（代表本地工具）。
type dataSchemaTool struct{}

func (dataSchemaTool) Name() string        { return "writer" }
func (dataSchemaTool) Description() string  { return "writes" }
func (dataSchemaTool) Mode() tool.ExecutionMode { return tool.AsyncExecution }
func (dataSchemaTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"path":    {Type: "string", Required: true, Desc: "file path"},
		"retries": {Type: "integer", Required: false, Desc: "n"},
	}}
}
func (dataSchemaTool) OutputSchema() tool.DataSchema { return tool.DataSchema{} }
func (dataSchemaTool) Execute(context.Context, map[string]any, tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(nil), nil
}

// nativeTool 原生持有 JSON Schema（代表 MCP 适配器）。
type nativeTool struct{}

func (nativeTool) Name() string        { return "native" }
func (nativeTool) Description() string  { return "native" }
func (nativeTool) Mode() tool.ExecutionMode      { return tool.SyncExecution }
func (nativeTool) InputSchema() tool.DataSchema  { return tool.DataSchema{} }
func (nativeTool) OutputSchema() tool.DataSchema { return tool.DataSchema{} }
func (nativeTool) Execute(context.Context, map[string]any, tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(nil), nil
}

const nativeSchema = `{"type":"object","properties":{"q":{"type":"string","enum":["a","b"]}},"required":["q"]}`

func (nativeTool) Definition() tool.ToolDefinition {
	return tool.ToolDefinition{
		Name:        "native",
		Description: "native",
		InputSchema: json.RawMessage(nativeSchema),
		Annotations: tool.Annotations{Execution: tool.SyncExecution},
	}
}

func TestDefinitionOf_SynthesizesFromDataSchema(t *testing.T) {
	d := tool.DefinitionOf(dataSchemaTool{})

	if d.Name != "writer" || d.Description != "writes" {
		t.Fatalf("name/desc 透传错误: %+v", d)
	}
	if d.Annotations.Execution != tool.AsyncExecution {
		t.Fatalf("Execution 应从 Mode() 派生为 async，得 %q", d.Annotations.Execution)
	}

	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
		t.Fatalf("合成的 InputSchema 非法 JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("顶层应为 object，得 %q", schema.Type)
	}
	if schema.Properties["path"].Type != "string" {
		t.Fatalf("path 应为 string，得 %q", schema.Properties["path"].Type)
	}
	if schema.Properties["retries"].Type != "integer" {
		t.Fatalf("retries 应为 integer，得 %q", schema.Properties["retries"].Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "path" {
		t.Fatalf("required 应为 [path]，得 %v", schema.Required)
	}
}

func TestDefinitionOf_PrefersNativeDefinition(t *testing.T) {
	d := tool.DefinitionOf(nativeTool{})

	// 原生定义应被原样采用 —— 关键是合成路径不能把 enum 这类无法用 DataSchema 表达的约束抹掉。
	var got, want map[string]any
	if err := json.Unmarshal(d.InputSchema, &got); err != nil {
		t.Fatalf("native InputSchema 非法: %v", err)
	}
	_ = json.Unmarshal([]byte(nativeSchema), &want)

	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("DefinedTool 的原生 schema 未被原样采用:\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}
