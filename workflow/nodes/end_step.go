package nodes

import (
	"flux/definition"
	"flux/tool"
)

type EndStep struct{}

func (e *EndStep) Name() string {
	return "end"
}

func (e *EndStep) RetryPolicy() RetryPolicy {
	return RetryPolicy{}
}

func (e *EndStep) InputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (e *EndStep) OutputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (e *EndStep) Run(ctx *NodeExecContext) error {

	ctx.EmitNodeEvent(NodeEvent{
		Type:     "completed",
		Message:  "Workflow完成",
		Progress: 1,
	})
	return nil
}

func (v *EndStep) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}

type EndNodeFactory struct{}

func (f *EndNodeFactory) Type() string {
	return "end"
}

func (f *EndNodeFactory) Create(def definition.NodeDefinition) (Step, error) {
	return &EndStep{}, nil
}

func NewEndNodeFactory() *EndNodeFactory {
	return &EndNodeFactory{}
}
