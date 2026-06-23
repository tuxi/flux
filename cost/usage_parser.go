package cost

import (
	"fmt"

	"flux/tool"
	"flux/utils"
	"flux/workflow/nodes"
)

func ValidateUsageFacts(rawFacts []map[string]any, schema tool.DataSchema) error {
	if len(rawFacts) == 0 {
		return nil
	}

	for idx, raw := range rawFacts {
		if raw == nil {
			return fmt.Errorf("usage_fact[%d] is nil", idx)
		}
		for fieldName, fs := range schema.Fields {
			val, exists := raw[fieldName]
			if fs.Required && !exists {
				return fmt.Errorf("usage_fact[%d] missing required field: %s", idx, fieldName)
			}
			if exists {
				if err := nodes.ValidateFieldTypeStrict(fs, val); err != nil {
					return fmt.Errorf("usage_fact[%d] invalid field %s: %w", idx, fieldName, err)
				}
			}
		}
	}

	return nil
}

func ParseUsageFacts(rawFacts []map[string]any) ([]UsageFact, error) {
	facts := make([]UsageFact, 0, len(rawFacts))
	for idx, raw := range rawFacts {
		if raw == nil {
			continue
		}
		fact, ok, err := ParseUsageFact(raw)
		if err != nil {
			return nil, fmt.Errorf("parse usage_fact[%d]: %w", idx, err)
		}
		if !ok || fact == nil {
			continue
		}
		facts = append(facts, *fact)
	}
	return facts, nil
}

func ParseUsageFact(raw map[string]any) (*UsageFact, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}

	resourceType := ResourceType(asString(raw["resource_type"]))
	provider := asString(raw["provider"])
	usageUnit := asString(raw["usage_unit"])
	usageQuantity := utils.ToFloat64(raw["usage_quantity"])
	billableStage := asString(raw["billable_stage"])
	billable := asBool(raw["billable"])

	if resourceType == "" || provider == "" || usageUnit == "" || usageQuantity <= 0 {
		return nil, false, nil
	}
	if billableStage != "" && billableStage != BillableStageCompleted {
		return nil, false, nil
	}
	if !billable {
		return nil, false, nil
	}

	fact := &UsageFact{
		ResourceType:      resourceType,
		Provider:          provider,
		Model:             asString(raw["model"]),
		ProviderRequestID: asString(raw["provider_request_id"]),
		UsageQuantity:     usageQuantity,
		UsageUnit:         usageUnit,
		UsageBreakdown:    asMap(raw["usage_breakdown"]),
		EstimatedCost:     utils.ToFloat64(raw["estimated_cost"]),
		Billable:          billable,
	}
	return fact, true, nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
