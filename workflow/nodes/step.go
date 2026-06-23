package nodes

import (
	"flux/tool"
	"time"
)

// RetryPolicy 重试机制
type RetryPolicy struct {
	MaxRetries int
	Interval   time.Duration
}

// Step 节点执行
type Step interface {
	Name() string
	Run(ctx *NodeExecContext) error
	RetryPolicy() RetryPolicy

	InputSchema() tool.DataSchema
	OutputSchema() tool.DataSchema

	Mode() tool.ExecutionMode
}

// UsageAwareStep 是节点 step 的可选能力接口。
// 只有有成本语义的 step/tool 才需要实现它。
type UsageAwareStep interface {
	UsageSchema() tool.DataSchema
	BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error)
}
