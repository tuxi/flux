package nodes

import (
	"flux/definition"
	"flux/tool"
	"fmt"
	"sync"
)

// NodeRegistry 节点注册中心
// 注意：NodeRegistry不是 ToolRegistry，它负责NodeType -> NodeFactory
// 例如：start tool llm condition code
type NodeRegistry struct {
	mu        sync.RWMutex
	factories map[string]NodeFactory
	schemas   map[string]*NodeTypeSchema
}

func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{
		factories: make(map[string]NodeFactory),
		schemas:   make(map[string]*NodeTypeSchema),
	}
}

func (r *NodeRegistry) Register(factory NodeFactory, schema *NodeTypeSchema) {

	r.mu.Lock()
	defer r.mu.Unlock()

	t := factory.Type()

	if _, ok := r.factories[t]; ok {
		panic(fmt.Sprintf("node type already registered: %s", t))
	}

	r.factories[t] = factory
	r.schemas[t] = schema
}

func (r *NodeRegistry) Get(nodeType string) (NodeFactory, error) {

	r.mu.RLock()
	defer r.mu.RUnlock()

	f, ok := r.factories[nodeType]
	if !ok {
		return nil, fmt.Errorf("unknown node type: %s", nodeType)
	}

	return f, nil
}

func (r *NodeRegistry) GetSchema(nodeType definition.NodeType) (*NodeTypeSchema, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.schemas[string(nodeType)]
	if !ok {
		return nil, fmt.Errorf("schema not found for node type: %s", nodeType)
	}
	return s, nil
}

func (r *NodeRegistry) ValidateNode(def *definition.NodeDefinition) error {
	s, err := r.GetSchema(def.Type)
	if err != nil {
		return err
	}
	return s.Validate(def)
}

func (r *NodeRegistry) List() []string {

	r.mu.RLock()
	defer r.mu.RUnlock()

	var types []string

	for t := range r.factories {
		types = append(types, t)
	}

	return types
}

func (r *NodeRegistry) IsExprConfigField(nodeType definition.NodeType, field string) bool {
	schema, err := r.GetSchema(nodeType)
	if err != nil {
		return false
	}
	if schema.ExprConfigFields == nil {
		return false
	}
	val, ok := schema.ExprConfigFields[field]
	if !ok {
		return false
	}
	return val
}

var reg = NewNodeRegistry()

// InitNodeRegistry NodeRegistry初始化（系统启动）
func InitNodeRegistry(toolReg *tool.Registry) *NodeRegistry {
	reg = NewNodeRegistry()

	reg.Register(NewStartNodeFactory(), startNodeScheme)
	reg.Register(NewEndNodeFactory(), endNodeScheme)
	reg.Register(NewSubWorkflowNodeFactory(), SubWorkflowNodeSchema)
	reg.Register(NewMapNodeFactory(), MapNodeSchema)
	reg.Register(NewLoopNodeFactory(), LoopNodeSchema)
	reg.Register(NewAwaitNodeFactory(), AwaitNodeSchema)

	reg.Register(NewToolFactory(toolReg), toolNodeScheme)

	return reg
}
