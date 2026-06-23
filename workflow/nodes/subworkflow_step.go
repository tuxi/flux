package nodes

import (
	"flux/tool"
)

type SubWorkflowStep struct {
	WorkflowName string
}

func NewSubWorkflowStep(workflowName string) *SubWorkflowStep {
	return &SubWorkflowStep{WorkflowName: workflowName}
}

func (s SubWorkflowStep) Name() string {
	return "subworkflow"
}

func (s *SubWorkflowStep) Run(ctx *NodeExecContext) error {
	// SubWorkflow 不在 Step 内执行
	// Engine executeNode 会接管
	return nil
}

func (s *SubWorkflowStep) RetryPolicy() RetryPolicy {
	return RetryPolicy{}
}

func (s *SubWorkflowStep) InputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (s *SubWorkflowStep) OutputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"result": {
				Type: "object",
			},
		},
	}
}

func (s *SubWorkflowStep) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}
