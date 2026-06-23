package nodes

import (
	"flux/definition"
	"fmt"
)

type LoopNodeFactory struct{}

func NewLoopNodeFactory() *LoopNodeFactory {
	return &LoopNodeFactory{}
}

func (l *LoopNodeFactory) Type() string {
	return definition.NodeLoop
}

func (l *LoopNodeFactory) Create(def definition.NodeDefinition) (Step, error) {
	items, ok := def.Config["items"].(string)
	if !ok || items == "" {
		return nil, fmt.Errorf("loop missing items")
	}

	iterator, ok := def.Config["iterator"].(string)
	if !ok || iterator == "" {
		return nil, fmt.Errorf("loop missing iterator")
	}

	subWorkflow, ok := def.Config["workflow"].(string)
	if !ok || subWorkflow == "" {
		return nil, fmt.Errorf("loop missing workflow")
	}

	carry := map[string]string{}
	if raw, ok := def.Config["carry"].(map[string]any); ok {
		for k, v := range raw {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("loop carry[%s] must be string", k)
			}
			carry[k] = s
		}
	}

	initial := map[string]any{}
	if raw, ok := def.Config["initial"].(map[string]any); ok {
		for k, v := range raw {
			initial[k] = v
		}
	}

	return NewLoopStep(
		items,
		iterator,
		subWorkflow,
		carry,
		initial,
	), nil
}
