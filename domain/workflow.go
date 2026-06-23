package domain

import (
	"time"

	"gorm.io/datatypes"
)

// Workflow 表示一个逻辑工作流
type Workflow struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	Name        string    `json:"name"`
	UserID      *int64    `json:"user_id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (Workflow) TableName() string {
	return "workflows"
}

// WorkflowVersion 工作流执行的版本
type WorkflowVersion struct {
	ID         int64 `json:"id"`
	WorkflowID int64 `json:"workflow_id"`
	Version    int64 `json:"version"`
	// DefinitionJSON (DAG) 包含每一个 节点的定义NodeDefinition
	// 表示节点结构（DAG）, 当前版本的工作流每个节点的定义都存储在这里面
	DefinitionJSON datatypes.JSON `json:"definition_json"`
	Hash           string         `json:"hash"`
	CreatedAt      time.Time      `json:"created_at"`
}
