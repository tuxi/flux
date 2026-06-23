package nodes

import (
	"context"
	"flux/tool"
	"time"
)

// ToolStepAdapter 桥接 Tool → Step 的适配器
type ToolStepAdapter struct {
	tool    tool.Tool
	config  map[string]any
	version string
}

func NewToolStepAdapter(
	t tool.Tool,
	config map[string]any,
	version string,
) Step {
	return &ToolStepAdapter{
		tool:    t,
		config:  config,
		version: version,
	}
}

func (t *ToolStepAdapter) Name() string {
	return t.tool.Name()
}

func (t *ToolStepAdapter) RetryPolicy() RetryPolicy {
	maxRetries := 2
	if raw, ok := t.config["retry_count"]; ok {
		switch v := raw.(type) {
		case int:
			maxRetries = v
		case int32:
			maxRetries = int(v)
		case int64:
			maxRetries = int(v)
		case float32:
			maxRetries = int(v)
		case float64:
			maxRetries = int(v)
		}
	}
	interval := 2 * time.Second
	if raw, ok := t.config["retry_interval_ms"]; ok {
		switch v := raw.(type) {
		case int:
			interval = time.Duration(v) * time.Millisecond
		case int32:
			interval = time.Duration(v) * time.Millisecond
		case int64:
			interval = time.Duration(v) * time.Millisecond
		case float32:
			interval = time.Duration(v) * time.Millisecond
		case float64:
			interval = time.Duration(v) * time.Millisecond
		}
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	if interval < 0 {
		interval = 0
	}
	return RetryPolicy{
		MaxRetries: maxRetries,
		Interval:   interval,
	}
}

func (t *ToolStepAdapter) InputSchema() tool.DataSchema {
	return t.tool.InputSchema()
}

func (t *ToolStepAdapter) OutputSchema() tool.DataSchema {
	return t.tool.OutputSchema()
}

func (t *ToolStepAdapter) Run(ctx *NodeExecContext) error {
	execCtx := context.Background()
	if ctx != nil && ctx.TaskContext != nil && ctx.TaskContext.Task != nil && ctx.NodeDef != nil {
		execCtx = tool.ContextWithExecutionMeta(execCtx, tool.ExecutionMeta{
			UserID:   ctx.TaskContext.Task.UserID,
			TaskID:   ctx.TaskContext.Task.ID,
			RootID:   ctx.TaskContext.Task.RootID,
			NodeName: ctx.NodeDef.Name,
		})
	}

	result, err := t.tool.Execute(
		execCtx,
		ctx.Input,
		ctx,
	)
	if err != nil {
		return err
	}

	// 直接覆盖 Output
	for k, v := range result.Data {
		ctx.Output[k] = v
	}
	return nil
}

func (t *ToolStepAdapter) Mode() tool.ExecutionMode {
	return t.tool.Mode()
}

func (t *ToolStepAdapter) UsageSchema() tool.DataSchema {
	if aware, ok := t.tool.(tool.UsageAware); ok {
		return aware.UsageSchema()
	}
	return tool.DataSchema{}
}

func (t *ToolStepAdapter) BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error) {
	aware, ok := t.tool.(tool.UsageAware)
	if !ok {
		return nil, nil
	}
	return aware.BuildUsageFacts(input, output)
}
