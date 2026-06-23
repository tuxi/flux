package tool

// UsageAware 为有成本语义的工具提供可选 usage facts 能力。
// 它不会改变 Tool 主接口，只让需要记账的工具显式声明用量结构与产出方式。
type UsageAware interface {
	UsageSchema() DataSchema
	BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error)
}
