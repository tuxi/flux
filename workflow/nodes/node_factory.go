package nodes

import (
	"flux/definition"
)

// NodeFactory 用于节点Step的构建
type NodeFactory interface {
	// Type 节点类型
	Type() string
	// Create 创建Step
	//例如：LLMFactory
	// ToolFactory
	//HTTPFactory
	//ScriptFactory
	Create(def definition.NodeDefinition) (Step, error)
}
