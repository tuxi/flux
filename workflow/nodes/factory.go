package nodes

import (
	"flux/definition"
	registry2 "flux/tool"
	"fmt"
)

type StepFactory interface {
	Create(node definition.NodeDefinition) (Step, error)
}

type DefaultFactory struct {
	ToolRegistry *registry2.Registry
}

func (f *DefaultFactory) Create(nd definition.NodeDefinition) (Step, error) {

	switch nd.Type {
	case definition.NodeStart:
		return &StartStep{}, nil

	case definition.NodeEnd:
		return &EndStep{}, nil
	case definition.NodeTool:
		toolName, exist := nd.Config["tool"].(string)
		if !exist {
			return nil, fmt.Errorf("tool node missing config.tool")
		}
		t, exist := f.ToolRegistry.Get(toolName)
		if !exist {
			return nil, fmt.Errorf("tool %s not found in registry", toolName)
		}
		return NewToolStepAdapter(t, nd.Config, nd.Version), nil
	case definition.NodeSubWorkflow:
		workflowName, exist := nd.Config["workflow"].(string)
		if !exist {
			return nil, fmt.Errorf("subworkflow node missing workflow")
		}
		return NewSubWorkflowStep(workflowName), nil
	default:
		return nil, fmt.Errorf("unknown step type: %s", nd.Type)
	}
}
