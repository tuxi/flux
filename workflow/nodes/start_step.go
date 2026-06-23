package nodes

import (
	"flux/definition"
	"flux/tool"
)

// StartStep 是Start 节点，职责只有一个：
// 把 TaskContext.Input 传递给 DAG，例如：用户输入：
//
//		{
//		 "topic": "AI创业"
//		}
//
//	 Start 输出：
//
//		{
//		 "topic": "AI创业"
//		}
type StartStep struct{}

func (s *StartStep) Name() string {
	return "start"
}

func (s *StartStep) RetryPolicy() RetryPolicy {
	return RetryPolicy{}
}

func (s *StartStep) InputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (s *StartStep) OutputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (s *StartStep) Run(ctx *NodeExecContext) error {
	ctx.EmitNodeEvent(NodeEvent{
		Type:    "started",
		Message: "Workflow started",
	})

	//for k, v := range ctx.TaskContext.Input {
	//	ctx.SetOutput(k, v)
	//}

	//ctx.EmitNodeEvent(NodeEvent{
	//	Type:     "completed",
	//	Message:  "Input injected",
	//	Progress: 1,
	//})

	return nil
}

// StartNodeFactory 是 StartStep的工程结构
type StartNodeFactory struct{}

func (f *StartNodeFactory) Type() string {
	return "start"
}

func (f *StartNodeFactory) Create(def definition.NodeDefinition) (Step, error) {

	return &StartStep{}, nil

}

func NewStartNodeFactory() *StartNodeFactory {
	return &StartNodeFactory{}
}

func (v *StartStep) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}
