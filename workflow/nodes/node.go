package nodes

import (
	"flux/definition"
)

// Node DAG 节点，带状态、依赖、重试策略
type Node struct {
	Name    string
	Label   string // 面向客户端的展示名
	Step    Step   // Step 是顶部步骤的代码 不存数据库
	Version string // 代码逻辑版本号。当你改了 Tool 代码，手动改这个值（如 "v1.1"）

	Config       map[string]any    // 节点配置，静态参数
	InputMapping map[string]string // 数据流映射，运行时输
	Type         definition.NodeType
	Weight       float64 // 节点权重
}
