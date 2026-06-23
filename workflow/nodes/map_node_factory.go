package nodes

import (
	"flux/definition"
	"fmt"
)

type MapNodeFactory struct{}

func NewMapNodeFactory() *MapNodeFactory {
	return &MapNodeFactory{}
}

func (f *MapNodeFactory) Type() string {
	return definition.NodeMap
}

func (f *MapNodeFactory) Create(def definition.NodeDefinition) (Step, error) {

	items, ok := def.Config["items"].(string)
	if !ok {
		return nil, fmt.Errorf("map missing items")
	}

	iterator, ok := def.Config["iterator"].(string)
	if !ok {
		return nil, fmt.Errorf("map missing iterator")
	}

	subWorkflow, ok := def.Config["workflow"].(string)
	if !ok {
		return nil, fmt.Errorf("map missing workflow")
	}

	parallel := 4

	if p, ok := def.Config["parallel"].(float64); ok {
		parallel = int(p)
	}

	step := NewMapStep(items, iterator, subWorkflow, parallel)

	// --- optional failure / fallback policy ---
	if fp, ok := def.Config["failure_policy"].(string); ok {
		step.WithFailurePolicy(fp)
	}
	// 兼容 JSON unmarshal 的 float64 和 Go DSL 直接写的 int
	switch v := def.Config["max_child_retries"].(type) {
	case float64:
		step.WithMaxChildRetries(int(v))
	case int:
		step.WithMaxChildRetries(v)
	case int64:
		step.WithMaxChildRetries(int(v))
	}
	if fs, ok := def.Config["fallback_source"].(string); ok {
		step.WithFallbackSource(fs)
	}
	// max_fallback_ratio: Phase 1 仅解析存储，不根据它 fail 任务
	switch v := def.Config["max_fallback_ratio"].(type) {
	case float64:
		step.WithMaxFallbackRatio(v)
	case int:
		step.WithMaxFallbackRatio(float64(v))
	case int64:
		step.WithMaxFallbackRatio(float64(v))
	}

	return step, nil
}
