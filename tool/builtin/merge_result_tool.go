package builtin

import (
	"context"
	"flux/tool"
)

type MergeResultTool struct {
}

func NewMergeResultTool() *MergeResultTool {
	return &MergeResultTool{}
}

func (m MergeResultTool) Name() string {
	return "merge_result"
}

func (m MergeResultTool) Description() string {
	return "用来合并结果的通用工具"
}

func (m MergeResultTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (m MergeResultTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (m MergeResultTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	return &tool.Result{
		Success: true,
		Data:    input, // 原路返回
	}, nil
}

func (m MergeResultTool) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}
