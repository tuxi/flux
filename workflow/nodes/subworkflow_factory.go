package nodes

import (
	"flux/definition"
	"fmt"
)

type SubWorkflowNodeFactory struct{}

func NewSubWorkflowNodeFactory() *SubWorkflowNodeFactory {
	return &SubWorkflowNodeFactory{}
}

func (f *SubWorkflowNodeFactory) Type() string {
	return definition.NodeSubWorkflow
}

func (f *SubWorkflowNodeFactory) Create(def definition.NodeDefinition) (Step, error) {

	wf, ok := def.Config["workflow"].(string)
	if !ok {
		return nil, fmt.Errorf("subworkflow missing workflow")
	}

	return NewSubWorkflowStep(wf), nil
}
