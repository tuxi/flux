package workflow

import (
	"flux/definition"
	"flux/engine/graph"
	"flux/workflow/nodes"
	"fmt"
	"reflect"

	"github.com/expr-lang/expr"
)

type Builder struct {
	registry *nodes.NodeRegistry
}

func NewBuilder(reg *nodes.NodeRegistry) *Builder {
	return &Builder{registry: reg}
}

func (b *Builder) GetRegister() *nodes.NodeRegistry {
	return b.registry
}

// Build 构建DAG
func (b *Builder) Build(def *definition.WorkflowDefinition) (Workflow, error) {

	// 分支转换
	//normalizeBranch(def)

	// 工作流输出规则校验
	if err := validateOutputMapping(def.Output); err != nil {
		return nil, err
	}

	// edge 增强校验
	if err := validateEdges(def); err != nil {
		return nil, err
	}

	// 节点Schema 校验
	for _, nd := range def.Nodes {
		// condition\switch\branch 已经由Edge 控制
		if nd.Type == "condition" || nd.Type == "switch" || nd.Type == "branch" {
			return nil, fmt.Errorf("node type %s is deprecated, use edge condition instead", nd.Type)
		}
		err := b.registry.ValidateNode(&nd)
		if err != nil {
			return nil, err
		}
	}

	// 构建Graph
	gh, err := graph.Build(def)
	if err != nil {
		return nil, err
	}

	if err := graph.DetectCycle(gh); err != nil {
		return nil, err
	}

	order, err := graph.TopoSort(gh)
	if err != nil {
		return nil, err
	}

	nodeMap := make(map[string]nodes.Node)

	for _, name := range order {

		nd := gh.Nodes[name]

		factory, err := b.registry.Get(string(nd.Type))
		if err != nil {
			return nil, err
		}

		step, err := factory.Create(*nd)
		if err != nil {
			return nil, err
		}

		nodeMap[name] = nodes.Node{
			Label:        nd.Label,
			Name:         nd.Name,
			Step:         step,
			Version:      nd.Version,
			Config:       nd.Config,
			InputMapping: nd.InputMapping,
			Type:         nd.Type,
			Weight:       nd.Weight,
		}
	}

	return &CompiledWorkflow{
		defSource: def,
		name:      def.Name,
		nodes:     nodeMap,
		graph:     gh,
		order:     order,
	}, nil
}

// validateEdges 增加 Edge 校验
func validateEdges(def *definition.WorkflowDefinition) error {
	nodeSet := map[string]bool{}

	for _, n := range def.Nodes {
		nodeSet[n.Name] = true
	}

	for _, e := range def.Edges {

		if !nodeSet[e.From] {
			return fmt.Errorf("edge from not exist: %s", e.From)
		}

		if !nodeSet[e.To] {
			return fmt.Errorf("edge to not exist: %s", e.To)
		}

		// ❗ Condition & CaseKey 不能同时存在
		if e.Condition != "" && e.CaseKey != "" {
			return fmt.Errorf("edge cannot have both condition and case_key: %s->%s", e.From, e.To)
		}
	}

	return nil
}

type CompiledWorkflow struct {
	defSource *definition.WorkflowDefinition
	name      string
	nodes     map[string]nodes.Node
	graph     *graph.Graph
	order     []string
}

func (c CompiledWorkflow) Name() string {
	return c.name
}

func (c CompiledWorkflow) Nodes() map[string]nodes.Node {
	return c.nodes
}

func (c CompiledWorkflow) Graph() *graph.Graph {
	return c.graph
}

func (c CompiledWorkflow) Order() []string {
	return c.order
}

func (c *CompiledWorkflow) NodeList() []nodes.Node {
	res := make([]nodes.Node, 0, len(c.order))
	for _, name := range c.order {
		res = append(res, c.nodes[name])
	}
	return res
}

func (c *CompiledWorkflow) Source() *definition.WorkflowDefinition {
	return c.defSource
}

// 工作流输出的规则校验
func validateOutputMapping(mapping definition.OutputDefinition) error {
	v := reflect.ValueOf(mapping)
	t := reflect.TypeOf(mapping)

	// 1. 遍历结构体中的所有导出字段
	for i := 0; i < v.NumField(); i++ {
		fieldName := t.Field(i).Name
		fieldValue := v.Field(i)

		switch fieldValue.Kind() {
		case reflect.String:
			exprStr := fieldValue.String()

			// ResultType 是固定值（枚举），不需要表达式编译校验，只需非空校验
			if fieldName == "ResultType" {
				if exprStr == "" {
					return fmt.Errorf("output field [ResultType] is required")
				}
				continue
			}

			// 其他字段如 PrimaryFileUrl, CoverUrl 等需要进行表达式编译校验
			if err := validateExpr(fieldName, exprStr); err != nil {
				return err
			}

		case reflect.Map:
			// 2. 校验 Extras 动态映射
			if fieldName == "Extras" {
				extras, ok := fieldValue.Interface().(map[string]string)
				if !ok {
					continue
				}
				for k, exprStr := range extras {
					if k == "" {
						return fmt.Errorf("output extras key cannot be empty")
					}
					if err := validateExpr("Extras."+k, exprStr); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// 提取公共的表达式编译逻辑
func validateExpr(fieldName, exprStr string) error {
	// 如果是选填项且为空，可以跳过校验（根据业务需求决定是否强制非空）
	// 例如 Width, Height 如果不传表达式可以允许为空
	if exprStr == "" {
		// 如果你要求 PrimaryFileUrl 必须有表达式，可以在这里加特殊判断
		return nil
	}

	_, err := expr.Compile(exprStr, expr.Env(map[string]any{
		"input": map[string]any{},
		"nodes": map[string]any{},
	}))
	if err != nil {
		return fmt.Errorf("invalid output expr in field [%s]: %w", fieldName, err)
	}
	return nil
}
