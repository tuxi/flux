// Package store 定义 Workflow Runtime 的持久化端口。
//
// Runtime 只依赖这里的接口，不依赖 SQLite / PostgreSQL / 任何具体存储。
// Adapter 包（flux/adapter/）提供具体实现，在装配时注入。
//
// 依赖方向（不可违反）：
//
//	flux/runtime  ←  flux/store（store 可以 import runtime 的类型）
//	flux/adapter/* →  flux/store（adapter 实现 store 接口）
//	flux/runtime  ←✗  flux/adapter（runtime 绝不 import adapter）
package store

import (
	"time"

	"github.com/tuxi/flux/runtime"
)

// RunMeta 是一次 Workflow 运行的元数据。
type RunMeta struct {
	ID             string // 外部传入或自动生成
	ConversationID string // 调起此 workflow 的 Conversation（关联，非依赖）
	Goal           string // 用户目标 / Plan 的描述
	ToolCatalog    []ToolInfo
}

// ToolInfo 是 DAGPlanner 工具目录中的一条工具信息。存快照用于回放。
type ToolInfo struct {
	Name        string
	Description string
	InputSchema []byte // JSON Schema
}

// TaskMeta 是一次任务执行的元数据。
type TaskMeta struct {
	ID       string
	ParentID string // 子 task 的父 task
	RootID   string // 根 task
}

// WorkflowRun 是一次完整的 Workflow 执行记录。
type WorkflowRun struct {
	ID             string
	ConversationID string
	Goal           string
	Status         string // "running" | "completed" | "failed" | "suspended"
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Task 是一次任务执行记录。
type Task struct {
	ID        string
	RunID     string
	ParentID  string
	RootID    string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NodeRecord 是一个节点的持久化记录。
type NodeRecord struct {
	NodeName string
	State    runtime.NodeState
	Output   map[string]any
	Error    string
}

// AwaitBinding 是异步节点的挂起/恢复凭证。
// 它独立于 Conversation 的生命周期持久化。
type AwaitBinding struct {
	BindingID      string
	TaskID         string
	NodeName       string
	ProviderTaskID string // 外部 Provider 返回的 job_id / task_id
	Status         string // "awaiting" | "completed" | "failed"
	Input          map[string]any
	CreatedAt      time.Time
	ResolvedAt     time.Time // zero = 未完成
}

// AwaitBinding status constants.
const (
	AwaitStatusAwaiting  = "awaiting"
	AwaitStatusCompleted = "completed"
	AwaitStatusFailed    = "failed"
)
