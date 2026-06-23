package dto

import (
	"flux/definition"
	"flux/domain"
)

type TaskSummary struct {
	Task            *domain.Task                 `json:"task"`
	Nodes           []*domain.NodeRuntime        `json:"nodes"`
	NodeDefinitions []*definition.NodeDefinition `json:"node_definitions"`
}

type ResumeTaskReq struct {
	TaskID       int64             `form:"task_id" json:"task_id" binding:"required"`
	ResumeFrom   string            `json:"resume_from,omitempty"`
	Patches      []RuntimePatchDTO `json:"patches,omitempty"`
	ChildResumes []ChildResumeSpec `json:"child_resumes,omitempty"`
}

// ChildResumeSpec 指定一个子任务的恢复策略。
type ChildResumeSpec struct {
	ChildTaskID   int64             `json:"child_task_id" binding:"required"`
	ResumeFrom    string            `json:"resume_from,omitempty"`
	Patches       []RuntimePatchDTO `json:"patches,omitempty"`
	OverrideInput map[string]any    `json:"override_input,omitempty"`
}

// ChildTaskDTO 是返回给 UI 的子任务精简视图。
type ChildTaskDTO struct {
	TaskID       int64  `json:"task_id"`
	Status       string `json:"status"`
	MapIndex     *int   `json:"map_index,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	RetryCount   int    `json:"retry_count"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// GetChildrenByNodeResp 是查询某个节点下所有子任务的响应。
type GetChildrenByNodeResp struct {
	NodeName string          `json:"node_name"`
	Children []ChildTaskInfo `json:"children"`
}

// ChildTaskInfo 包含子任务、节点运行时和节点定义，供 UI 展示节点选择器。
type ChildTaskInfo struct {
	Child           ChildTaskDTO                 `json:"child"`
	Nodes           []*domain.NodeRuntime        `json:"nodes"`
	NodeDefinitions []*definition.NodeDefinition `json:"node_definitions"`
}

type CancelTaskReq struct {
	TaskID int64 `form:"task_id" json:"task_id" binding:"required"`
}

// Task 是发布中心列表使用的轻量任务读取模型
type Task struct {
	ID       int64   `json:"id"`
	UserID   int64   `json:"user_id"`
	Status   string  `json:"status"`
	Type     string  `json:"type"`
	Progress float64 `json:"progress"`

	EntryType     *string `json:"entry_type"`
	EntryTitle    *string `json:"entry_title"`
	EntrySubtitle *string `json:"entry_subtitle"`
	RouteKey      *string `json:"route_key"`
	ModeKey       *string `json:"mode_key"`

	Input  map[string]any     `json:"input"`
	Result *domain.TaskOutput `json:"result"`

	ErrorMessage *string `json:"error_message"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

type TaskDetail struct {
	ID       int64   `json:"id"`
	UserID   int64   `json:"user_id"`
	Status   string  `json:"status"`
	Type     string  `json:"type"`
	Progress float64 `json:"progress"`

	EntryType     *string `json:"entry_type"`
	EntryTitle    *string `json:"entry_title"`
	EntrySubtitle *string `json:"entry_subtitle"`
	RouteKey      *string `json:"route_key"`
	ModeKey       *string `json:"mode_key"`

	Input map[string]any `json:"input"`

	Result *domain.TaskOutput `json:"result"`

	ErrorMessage *string `json:"error_message"`

	RetryCount int `json:"retry_count"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// TaskListReq 管理后台任务发布中心列表请求
type TaskListReq struct {
	Page     int    `form:"page"`
	PageSize int    `form:"page_size"`
	Keyword  string `form:"keyword"`

	// pending / running / success / failed / suspended
	Status string `form:"status"`

	// tool / template / workflow
	EntryType string `form:"entry_type"`

	// video / image / audio
	ResultType string `form:"result_type"`

	RouteKey string `form:"route_key"`
	ModeKey  string `form:"mode_key"`

	// 支持逗号分隔的多个排除 key，例如：?exclude_route_keys=dream_studio,other_key
	ExcludeRouteKeys string `form:"exclude_route_keys"` // 接收 "a,b,c"
	ExcludeModeKeys  string `form:"exclude_mode_keys"`  // 接收 "a,b,c"

	// all / unpublished / asset_published / asset_bound / template_published
	PublishState string `form:"publish_state"`

	OnlySuccess *bool `form:"only_success"`
}

type TaskListResp struct {
	Items    []*Task `json:"items"`
	Total    int64   `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}
