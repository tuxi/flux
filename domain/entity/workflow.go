package entity

import (
	"time"

	"gorm.io/datatypes"
)

// WorkflowModel 表示一个逻辑工作流
type WorkflowModel struct {
	ID          int64 `gorm:"primaryKey"`
	UserID      *int64
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (WorkflowModel) TableName() string {
	return "workflows"
}

// WorkflowVersionModel 表示这个工作流某个发布版本的执行快照
type WorkflowVersionModel struct {
	ID         int64 `gorm:"primaryKey"`
	WorkflowID int64
	Version    int64
	// Definition 表示一个 WorkflowModel
	// {
	//  "nodes": [
	//    {
	//      "id": "start",
	//      "type": "start"
	//    },
	//    {
	//      "id": "llm1",
	//      "type": "llm",
	//      "depends": ["start"],
	//      "config": {
	//          "model":"gpt4",
	//          "prompt":"写一个视频脚本"
	//      }
	//    }
	//  ]
	//}
	DefinitionJSON datatypes.JSON `gorm:"type:jsonb"`
	Hash           string         `gorm:"type:varchar(64)"`
	CreatedAt      time.Time
}

func (WorkflowVersionModel) TableName() string {
	return "workflow_versions"
}
