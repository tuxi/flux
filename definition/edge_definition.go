package definition

// EdgeDefinition 工作流中边的定义，也是工作流中的控制流（Condition / Case）
type EdgeDefinition struct {
	From string   `json:"from"` // 源节点Name
	To   string   `json:"to"`   // 目标节点Name
	Type EdgeType // edge类型
	// 分支条件（核心）
	Condition string `json:"condition,omitempty"` // 条件表达式 expr 表达式（可选）（如："hit == true"）
	// switch case（可选）
	CaseKey  string `json:"case_key,omitempty"` // 分支枚举值（如："true"/"false"）
	Label    string // UI 显示
	Priority int    // 多分支优先级
}

// EdgeType 描述拓扑关系，工作流定义中边的类型
type EdgeType string

const (
	EdgeNormal EdgeType = "normal"

	EdgeCondition EdgeType = "condition"
)
