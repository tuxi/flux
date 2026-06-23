package runtimekeys

import (
	"crypto/md5"
	"encoding/json"
	"fmt"

	"flux/utils"
)

// BuildSubWorkflowKey 构建 SubKey（幂等）
func BuildSubWorkflowKey(
	parentTaskID int64,
	nodeName string,
	workflowName string,
	input map[string]any,
) string {
	normalized := normalizeSubWorkflowKeyInput(input)
	b, _ := json.Marshal(normalized)

	return fmt.Sprintf(
		"%d-%s-%s-%x",
		parentTaskID,
		nodeName,
		workflowName,
		md5.Sum(b),
	)
}

func normalizeSubWorkflowKeyInput(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	return normalizeMapAny(input)
}

func normalizeMapAny(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = normalizeValue(v)
	}
	return out
}

func normalizeValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return normalizeMapAny(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeValue(x[i])
		}
		return out
	default:
		if utils.IsObject(v) {
			asMap, err := utils.ObjectToMap(v)
			if err == nil {
				return normalizeMapAny(asMap)
			}
		}
		if utils.IsSlice(v) {
			items, ok := utils.ToAnySlice(v)
			if ok {
				out := make([]any, len(items))
				for i := range items {
					out[i] = normalizeValue(items[i])
				}
				return out
			}
		}
		return utils.NormalizeAny(v)
	}
}
