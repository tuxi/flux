package nodes

import (
	"flux/definition"
	"flux/tool"
	"fmt"
)

type ToolFactory struct {
	ToolRegistry *tool.Registry
}

func NewToolFactory(toolRegistry *tool.Registry) *ToolFactory {
	return &ToolFactory{ToolRegistry: toolRegistry}
}

func (f *ToolFactory) Type() string {
	return definition.NodeTool
}

func (f *ToolFactory) Create(def definition.NodeDefinition) (Step, error) {

	toolName, ok := def.Config["tool"].(string)
	if !ok {
		return nil, fmt.Errorf("tool node missing config.tool")
	}

	t, exist := f.ToolRegistry.Get(toolName)
	if !exist {
		return nil, fmt.Errorf("tool not found: %s", toolName)
	}

	return NewToolStepAdapter(t, def.Config, def.Version), nil
}
