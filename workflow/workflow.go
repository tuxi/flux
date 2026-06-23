package workflow

import (
	"flux/definition"
	"flux/engine/graph"
	"flux/workflow/nodes"
)

/*
workflow 包是流程引擎核心

负责：
	•	Step 定义
	•	Engine 执行逻辑
	•	Workflow 注册机制
*/

// Workflow AI 工作流协议
type Workflow interface {
	Name() string                           // 工作流名称
	Nodes() map[string]nodes.Node           // 所有节点map
	Graph() *graph.Graph                    // DAG 的基础结构
	Order() []string                        // 拓扑排序结果（Topological Order）
	Source() *definition.WorkflowDefinition // 源工作流定义
}
