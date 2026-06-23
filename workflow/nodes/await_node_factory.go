package nodes

import (
	"fmt"

	"flux/definition"
)

type AwaitNodeFactory struct{}

func NewAwaitNodeFactory() *AwaitNodeFactory {
	return &AwaitNodeFactory{}
}

func (f *AwaitNodeFactory) Type() string {
	return definition.NodeAwait
}

func (f *AwaitNodeFactory) Create(def definition.NodeDefinition) (Step, error) {
	awaitType, ok := def.Config["await_type"].(string)
	if !ok || awaitType == "" {
		return nil, fmt.Errorf("await node missing await_type")
	}

	source, ok := def.Config["source"].(string)
	if !ok || source == "" {
		return nil, fmt.Errorf("await node missing source")
	}

	return NewAwaitStep(awaitType, source), nil
}
