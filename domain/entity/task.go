package entity

import (
	"time"

	"gorm.io/datatypes"
)

type TaskEventModel struct {
	ID         int64          `gorm:"primaryKey"`
	TaskID     int64          `gorm:"not null;index"`
	RootTaskID int64          `gorm:"not null;index"`
	Step       string         `gorm:"type:varchar(100);not null"`
	Message    string         `gorm:"type:text;"`
	Error      string         `gorm:"type:text;"`
	Meta       datatypes.JSON `gorm:"type:jsonb"`
	Type       string         `gorm:"type:varchar(100)"`

	Progress  float64
	Level     string
	NodeIndex int
	NodeTotal int

	Grade    string `gorm:"type:varchar(16);not null;default:'persistent'"`
	// Sequence is derived from the auto-increment ID column.
	// Query uses WHERE root_task_id = ? AND id > after_sequence for per-task scoping.

	CreatedAt time.Time `gorm:"index"`
}

func (TaskEventModel) TableName() string {
	return "task_events"
}

// TaskModel 任务实例
// 每一次运行工作流，都会产生一个 Task。
type TaskModel struct {
	ID     int64 `gorm:"primaryKey"`
	UserID int64 `gorm:"index;not null"`

	// 当前任务属于哪个工作流模版
	WorkflowDefinitionID int64

	Type   string `gorm:"type:varchar(100);not null"`
	Status string `gorm:"type:varchar(30);not null;index"`

	InputJSON  datatypes.JSON `gorm:"type:jsonb"`
	OutputJSON datatypes.JSON `gorm:"type:jsonb"`

	ParentID *int64 `gorm:"index"`
	RootID   int64  `gorm:"index"`

	RetryCount   int     `gorm:"not null;default:0"`
	ErrorMessage *string `gorm:"type:text"`

	// WorkflowVersionID 表示当前使用的那个版本的工作流，对应WorkflowVersion表
	WorkflowVersionID int64 `gorm:"index"`

	// 任务抢占字段
	WorkerID  string
	StartedAt time.Time

	SubKey     *string `gorm:"type:varchar(255);uniqueIndex"` // 恢复子工作流的key
	ParentNode *string `json:"parent_node"`                   // 当前任务所属的父节点
	MapIndex   *int    `json:"-"`                             // 在 map node 中执行的位置

	Progress float64 `json:"progress"` // 任务进度

	BaseRunID  int64   `gorm:"index"`
	ForkedFrom *int64  `gorm:"index"`
	RunDepth   int     `gorm:"not null;default:0"`
	EditAction *string `gorm:"type:varchar(64);index"`
	EditLabel  *string `gorm:"type:varchar(255)"`

	// ===== patch / resume =====
	ResumeFrom *string        // 本次 fork 从哪个节点开始恢复
	PatchJSON  datatypes.JSON `gorm:"type:jsonb"` // []RuntimePatch 的 JSON

	// ===== 业务归属字段 =====
	EntryType         string `json:"entry_type"` // tool / template / workflow
	ToolDefinitionID  *int64 `json:"tool_definition_id"`
	ToolModeID        *int64 `json:"tool_mode_id"`
	ToolModeVersionID *int64 `json:"tool_mode_version_id"`
	TemplateID        *int64 `json:"template_id"`
	TemplateVersionID *int64 `json:"template_version_id"`

	// 展示冗余字段（方便列表页直出）
	EntryTitle    *string `json:"entry_title"`    // 比如“视频生成”
	EntrySubtitle *string `json:"entry_subtitle"` // 比如“图生视频”
	RouteKey      *string `json:"route_key"`      // video_generation
	ModeKey       *string `json:"mode_key"`       // image_to_video

	EstimatedCostTotal float64 `gorm:"not null;default:0"`
	ActualCostTotal    float64 `gorm:"not null;default:0"`
	CostStatus         string  `gorm:"type:varchar(32);not null;default:'none'"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (TaskModel) TableName() string {
	return "tasks"
}

// TaskNodeModel 任务运行时的节点
type TaskNodeModel struct {
	ID       int64  `gorm:"primaryKey"`
	TaskID   int64  `gorm:"not null;index;uniqueIndex:uniq_task_node"`
	NodeName string `gorm:"type:varchar(100);not null;uniqueIndex:uniq_task_node"`
	State    string `gorm:"type:varchar(30);not null;index:idx_state_heartbeat"`

	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      *string `gorm:"type:text"`

	LastHeartbeat     *time.Time     `gorm:"index:idx_state_heartbeat"` // 节点心跳时间, idx_state_heartbeat联合索引(state,state_heartbeat)
	OutputJSON        datatypes.JSON `gorm:"type:jsonb"`                // 存储该节点的输出结果
	InputHash         string         `gorm:"type:text"`                 // 存储上次执行成功时的“指纹”
	CheckpointJSON    datatypes.JSON `gorm:"type:jsonb"`
	Index             int
	BizIndex          int
	Weight            float64 `gorm:"type:float"` // 节点权重0～1

	ActivatedEdgesJSON []byte `json:"activated_edges_json"` // 存储已经激活的边

	CreatedAt time.Time
	UpdatedAt time.Time

	// ===== 新增 =====
	OutputHash       string  `gorm:"type:text"`
	ReusedFromTaskID *int64  `gorm:"index"`
	ReusedFromNode   *string `gorm:"type:varchar(100)"`
	IsInjected       bool    `gorm:"not null;default:false"`
	IsDirty          bool    `gorm:"not null;default:false;index"`
	DirtyReason      *string `gorm:"type:varchar(64)"`
	CheckpointedAt   *time.Time
	ReuseKind        string `json:"reuse_kind"`
}

func (TaskNodeModel) TableName() string {
	return "task_nodes"
}

type TaskCostTraceModel struct {
	ID int64 `gorm:"primaryKey"`

	TaskID        int64  `gorm:"not null;index"`
	RootTaskID    int64  `gorm:"not null;index"`
	NodeRuntimeID *int64 `gorm:"index"`

	WorkflowName string `gorm:"type:varchar(120);index"`
	NodeName     string `gorm:"type:varchar(100);index"`
	StepName     string `gorm:"type:varchar(100);index"`

	ResourceType string `gorm:"type:varchar(50);not null;index"`
	Provider     string `gorm:"type:varchar(50);not null;index"`
	Model        string `gorm:"type:varchar(120)"`

	ProviderRequestID string `gorm:"type:varchar(255);index"`
	UsageQuantity     float64
	UsageUnit         string  `gorm:"type:varchar(32);not null"`
	UnitPrice         float64 `gorm:"not null;default:0"`
	EstimatedCost     float64 `gorm:"not null;default:0;index"`
	ActualCost        float64 `gorm:"not null;default:0"`
	Currency          string  `gorm:"type:varchar(8);not null;default:'CNY'"`
	Status            string  `gorm:"type:varchar(32);not null;default:'estimated';index"`
	IdempotencyKey    string  `gorm:"type:varchar(255);not null;uniqueIndex"`

	TracePayload datatypes.JSON `gorm:"type:jsonb"`

	CreatedAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

func (TaskCostTraceModel) TableName() string {
	return "task_cost_traces"
}
