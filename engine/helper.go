package engine

import (
	"encoding/json"

	"flux/domain"
	"flux/workflow/nodes"
)

func findNode(nodes map[string]nodes.Node, name string) *nodes.Node {

	for _, n := range nodes {
		if n.Name == name {
			return &n
		}
	}

	return nil
}

func parseTaskInput(data []byte) map[string]any {
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

func parseTaskOutput(data []byte) map[string]any {
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (e *Engine) GenTaskID() int64 {
	return e.iSrv.GenSnowID()
}

func deepCloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCloneAny(v)
	}
	return dst
}

func deepCloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCloneMap(x)

	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = deepCloneAny(x[i])
		}
		return out

	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out

	case []int:
		out := make([]int, len(x))
		copy(out, x)
		return out

	case []int64:
		out := make([]int64, len(x))
		copy(out, x)
		return out

	case []float64:
		out := make([]float64, len(x))
		copy(out, x)
		return out

	case []bool:
		out := make([]bool, len(x))
		copy(out, x)
		return out

	case map[string]string:
		out := make(map[string]string, len(x))
		for k, v := range x {
			out[k] = v
		}
		return out

	case map[string]int:
		out := make(map[string]int, len(x))
		for k, v := range x {
			out[k] = v
		}
		return out

	case map[string]bool:
		out := make(map[string]bool, len(x))
		for k, v := range x {
			out[k] = v
		}
		return out

	default:
		return x
	}
}

func hasAnyReusedMapItems(cp map[string]any) bool {
	if cp == nil {
		return false
	}
	raw, _ := cp["reused_items"].(map[string]any)
	if raw == nil {
		return false
	}
	for _, v := range raw {
		if b, ok := v.(bool); ok && b {
			return true
		}
	}
	return false
}

func clearNodeReuseMetadata(runtime *domain.NodeRuntime) {
	if runtime == nil {
		return
	}
	runtime.IsInjected = false
	runtime.ReusedFromTaskID = nil
	runtime.ReusedFromNode = nil

	if runtime.ReuseKind == domain.ReuseNode {
		runtime.ReuseKind = domain.ReuseNone
	}
}

func parseTaskPatches(data []byte) ([]domain.RuntimePatch, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var patches []domain.RuntimePatch
	err := json.Unmarshal(data, &patches)
	return patches, err
}
