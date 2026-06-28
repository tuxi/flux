package tool

import (
	"context"
	"fmt"
)

// WorkflowAsTool 将一个已有的 Workflow 包装为 tool.Tool，使 Agent 可以像调用普通工具一样
// 选择已有的预制工作流（如 text_to_image、goods_video_pro_v3）。
//
// 预制工作流是经过生产验证的手写 DSL，Agent 可以选择直接使用它们，
// 而不是每次都从零生成 DAG。
type WorkflowAsTool struct {
	name        string
	description string
	schema      DataSchema
	// Execute 委托给外部（由宿主注入 v1 engine 调用或直接执行）
	executor func(ctx context.Context, input map[string]any, emitter ToolEmitter) (*Result, error)
}

// NewWorkflowAsTool 创建一个预制工作流工具。
// executor: 宿主提供的执行函数，通常委托给 v1 engine。
func NewWorkflowAsTool(name, description string, schema DataSchema, executor func(ctx context.Context, input map[string]any, emitter ToolEmitter) (*Result, error)) *WorkflowAsTool {
	return &WorkflowAsTool{
		name:        name,
		description: description,
		schema:      schema,
		executor:    executor,
	}
}

func (t *WorkflowAsTool) Name() string             { return t.name }
func (t *WorkflowAsTool) Description() string      { return t.description }
func (t *WorkflowAsTool) InputSchema() DataSchema   { return t.schema }
func (t *WorkflowAsTool) OutputSchema() DataSchema  { return DataSchema{} }
func (t *WorkflowAsTool) Mode() ExecutionMode       { return SyncExecution }

// SetExecutor 设置工作流的执行函数（用于延迟注入，如 engine 创建后）。
func (t *WorkflowAsTool) SetExecutor(fn func(ctx context.Context, input map[string]any, emitter ToolEmitter) (*Result, error)) {
	t.executor = fn
}

func (t *WorkflowAsTool) Execute(ctx context.Context, input map[string]any, emitter ToolEmitter) (*Result, error) {
	if t.executor == nil {
		return Fail(fmt.Errorf("workflow %s: no executor configured", t.name)), nil
	}
	return t.executor(ctx, input, emitter)
}
