package nodes

import (
	"flux/domain"
	"flux/tool"
)

// AwaitStep 本身不在 Step.Run 中执行真正的等待逻辑。
// 与 subworkflow 类似，Engine.executeNode 会接管该节点：
// 1. 创建 AwaitBinding
// 2. 将节点置为 awaiting
// 3. 挂起任务
type AwaitStep struct {
	AwaitType string
	Source    string
}

func NewAwaitStep(awaitType, source string) *AwaitStep {
	return &AwaitStep{
		AwaitType: awaitType,
		Source:    source,
	}
}

func (s *AwaitStep) Name() string {
	return "await"
}

func (s *AwaitStep) Run(ctx *NodeExecContext) error {
	return &domain.WorkflowSuspendedError{
		Reason: domain.SuspendAsyncNode,
	}
}

func (s *AwaitStep) RetryPolicy() RetryPolicy {
	return RetryPolicy{}
}

func (s *AwaitStep) InputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"callback_token": {Type: "string", Required: false},
		},
	}
}

func (s *AwaitStep) OutputSchema() tool.DataSchema {
	// Await 节点的最终输出来自外部事件完成时的回填，因此这里返回开放 schema。
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{},
	}
}

func (s *AwaitStep) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}
