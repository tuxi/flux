package nodes

import (
	"fmt"

	"flux/definition"
	"flux/tool"
)

// NodeTypeSchema 描述节点类型
type NodeTypeSchema struct {

	// 节点类型
	Type definition.NodeType

	// 描述
	Description string

	// 配置Schema
	ConfigSchema tool.DataSchema

	// 表达式字段，定义哪些字段是表达式字段，运行时输入构造时，expression config 要 Eval
	ExprConfigFields map[string]bool
}

// Validate 校验 NodeDefinition Config
func (s *NodeTypeSchema) Validate(
	def *definition.NodeDefinition,
) error {

	for name, field := range s.ConfigSchema.Fields {

		val, exists := def.Config[name]

		if field.Required && !exists {
			return fmt.Errorf(
				"node %s missing config field: %s",
				def.Name,
				name,
			)
		}

		if exists {

			err := validateFieldType(field.Type, val)

			if err != nil {
				return fmt.Errorf(
					"node %s config field %s type error: %w",
					def.Name,
					name,
					err,
				)
			}

		}
	}

	return nil
}

func validateFieldType(expected string, val any) error {
	switch expected {

	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("expect string")
		}

	case "number":
		switch val.(type) {
		case float64, int:
		default:
			return fmt.Errorf("expect number")
		}

	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("expect object")
		}

	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Errorf("expect array")
		}

	}

	return nil
}
