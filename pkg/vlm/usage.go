package vlm

import "flux/tool"

func UsageSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"resource_type":       {Type: "string", Required: true},
			"provider":            {Type: "string", Required: true},
			"model":               {Type: "string", Required: false},
			"provider_request_id": {Type: "string", Required: false},
			"usage_quantity":      {Type: "number", Required: true},
			"usage_unit":          {Type: "string", Required: true},
			"usage_breakdown":     {Type: "object", Required: false},
			"billable":            {Type: "bool", Required: true},
			"billable_stage":      {Type: "string", Required: true},
			"estimated_cost":      {Type: "number", Required: false},
		},
	}
}

func BuildUsageFacts(output map[string]any) []map[string]any {
	provider := stringValue(output["vlm_provider"])
	totalTokens := numberValue(output["total_tokens"])
	if provider == "" || totalTokens <= 0 {
		return nil
	}

	return []map[string]any{
		{
			"resource_type":       "vlm",
			"provider":            provider,
			"model":               firstNonEmptyString(stringValue(output["vlm_model"]), stringValue(output["model"])),
			"provider_request_id": stringValue(output["vlm_request_id"]),
			"usage_quantity":      totalTokens,
			"usage_unit":          "tokens",
			"billable":            true,
			"billable_stage":      "completed",
			"usage_breakdown": map[string]any{
				"prompt_tokens":     output["prompt_tokens"],
				"completion_tokens": output["completion_tokens"],
			},
		},
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func numberValue(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint8:
		return float64(n)
	case uint16:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	case float32:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}
