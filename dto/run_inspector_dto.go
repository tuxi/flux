package dto

import "time"

// 用于页面主入口：
// GET /runs/:taskID/inspect

type RunInspectorResp struct {
	Run           RunSummaryDTO         `json:"run"`
	Workflow      WorkflowSummaryDTO    `json:"workflow"`
	DAG           RunDAGDTO             `json:"dag"`
	Snapshot      RunSnapshotDTO        `json:"snapshot"`
	Lineage       *RunLineageSummaryDTO `json:"lineage,omitempty"`
	Patches       []RuntimePatchDTO     `json:"patches,omitempty"`
	Resume        *ResumeSpecSummaryDTO `json:"resume,omitempty"`
	AwaitBindings []RunAwaitBindingDTO  `json:"await_bindings,omitempty"`
}

type RunSummaryDTO struct {
	TaskID        int64      `json:"task_id"`
	RootID        int64      `json:"root_id"`
	BaseRunID     int64      `json:"base_run_id"`
	ForkedFrom    *int64     `json:"forked_from,omitempty"`
	RunDepth      int        `json:"run_depth"`
	Status        string     `json:"status"`
	Progress      float64    `json:"progress"`
	Type          string     `json:"type"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	EditAction    *string    `json:"edit_action,omitempty"`
	EditLabel     *string    `json:"edit_label,omitempty"`
	ResumeFrom    *string    `json:"resume_from,omitempty"`
	WorkflowID    int64      `json:"workflow_id"`
	WorkflowVerID int64      `json:"workflow_version_id"`
}

type WorkflowSummaryDTO struct {
	WorkflowID  int64  `json:"workflow_id"`
	Name        string `json:"name"`
	VersionID   int64  `json:"version_id"`
	Version     int64  `json:"version"`
	Description string `json:"description,omitempty"`
}

type RunSnapshotDTO struct {
	Input map[string]any `json:"input"`
	Final map[string]any `json:"final,omitempty"`
}

type RunTimelineEventDTO struct {
	ID        int64          `json:"id"`
	TaskID    int64          `json:"task_id"`
	NodeName  string         `json:"node_name,omitempty"`
	Phase     string         `json:"phase"` // planning/materialization/execution
	Type      string         `json:"type"`  // node_planned/node_patched/node_running...
	Title     string         `json:"title"`
	Message   string         `json:"message,omitempty"`
	Level     string         `json:"level,omitempty"`
	Progress  float64        `json:"progress,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type ResumeSpecSummaryDTO struct {
	ResumeFrom string `json:"resume_from"`
	PatchCount int    `json:"patch_count"`
}

type RunLineageSummaryDTO struct {
	BaseRunID      int64   `json:"base_run_id"`
	ForkedFrom     *int64  `json:"forked_from,omitempty"`
	AncestorRunIDs []int64 `json:"ancestor_run_ids,omitempty"`
	ChildRunIDs    []int64 `json:"child_run_ids,omitempty"`
}

// 右侧节点详情面板主数据。
// GET /runs/:taskID/nodes/:nodeName

type RunNodeDetailResp struct {
	Run          RunSummaryDTO         `json:"run"`
	Node         RunNodeDetailDTO      `json:"node"`
	Parent       *RunNodeParentDTO     `json:"parent,omitempty"`
	Patches      []RuntimePatchDTO     `json:"patches,omitempty"`
	Timeline     []RunTimelineEventDTO `json:"timeline,omitempty"`
	Diff         *RunNodeDiffDTO       `json:"diff,omitempty"`
	AwaitBinding *RunAwaitBindingDTO   `json:"await_binding,omitempty"`
}

type RunAwaitBindingDTO struct {
	ID                  int64                   `json:"id"`
	TaskID              int64                   `json:"task_id"`
	RootTaskID          int64                   `json:"root_task_id"`
	NodeName            string                  `json:"node_name"`
	WorkflowVersionID   int64                   `json:"workflow_version_id"`
	AwaitType           string                  `json:"await_type"`
	Source              string                  `json:"source"`
	Status              string                  `json:"status"`
	Provider            *string                 `json:"provider,omitempty"`
	ProviderTaskID      *string                 `json:"provider_task_id,omitempty"`
	APITaskID           *string                 `json:"api_task_id,omitempty"`
	ExternalTaskID      *string                 `json:"external_task_id,omitempty"`
	SignalName          *string                 `json:"signal_name,omitempty"`
	MessageName         *string                 `json:"message_name,omitempty"`
	CallbackToken       *string                 `json:"callback_token,omitempty"`
	Correlation         map[string]any          `json:"correlation,omitempty"`
	Config              map[string]any          `json:"config,omitempty"`
	LastEventID         *string                 `json:"last_event_id,omitempty"`
	LastEventSource     *string                 `json:"last_event_source,omitempty"`
	LastEventPayload    map[string]any          `json:"last_event_payload,omitempty"`
	ResultPayload       map[string]any          `json:"result_payload,omitempty"`
	ErrorMessage        string                  `json:"error_message,omitempty"`
	FallbackPollEnabled bool                    `json:"fallback_poll_enabled"`
	FallbackPollTool    *string                 `json:"fallback_poll_tool,omitempty"`
	PollAttempts        int                     `json:"poll_attempts"`
	MaxPollAttempts     int                     `json:"max_poll_attempts"`
	LastPolledAt        *time.Time              `json:"last_polled_at,omitempty"`
	NextPollAt          *time.Time              `json:"next_poll_at,omitempty"`
	WaitingStartedAt    *time.Time              `json:"waiting_started_at,omitempty"`
	TimeoutAt           *time.Time              `json:"timeout_at,omitempty"`
	CompletedAt         *time.Time              `json:"completed_at,omitempty"`
	FailedAt            *time.Time              `json:"failed_at,omitempty"`
	CanceledAt          *time.Time              `json:"canceled_at,omitempty"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
	StatusCategory      string                  `json:"status_category"`
	StatusLabel         string                  `json:"status_label"`
	WaitingFor          string                  `json:"waiting_for,omitempty"`
	NextAction          string                  `json:"next_action,omitempty"`
	IsTerminal          bool                    `json:"is_terminal"`
	CorrelationKeys     []string                `json:"correlation_keys,omitempty"`
	EventSummary        RunAwaitEventSummaryDTO `json:"event_summary"`
	PollSummary         RunAwaitPollSummaryDTO  `json:"poll_summary"`
}

type RunAwaitEventSummaryDTO struct {
	LastSource      *string  `json:"last_source,omitempty"`
	HasLastPayload  bool     `json:"has_last_payload"`
	LastPayloadKeys []string `json:"last_payload_keys,omitempty"`
	HasResult       bool     `json:"has_result"`
	ResultKeys      []string `json:"result_keys,omitempty"`
}

type RunAwaitPollSummaryDTO struct {
	Enabled      bool       `json:"enabled"`
	Tool         *string    `json:"tool,omitempty"`
	Attempts     int        `json:"attempts"`
	MaxAttempts  int        `json:"max_attempts"`
	LastPolledAt *time.Time `json:"last_polled_at,omitempty"`
	NextPollAt   *time.Time `json:"next_poll_at,omitempty"`
	IsDue        bool       `json:"is_due"`
	HasCapacity  bool       `json:"has_capacity"`
}

type RunNodeDetailDTO struct {
	Name         string            `json:"name"`
	Label        string            `json:"label,omitempty"`
	Type         string            `json:"type"`
	Version      string            `json:"version,omitempty"`
	Config       map[string]any    `json:"config,omitempty"`
	InputMapping map[string]string `json:"input_mapping,omitempty"`

	State           string          `json:"state"`
	Action          string          `json:"action"`
	ExecutionReason string          `json:"execution_reason,omitempty"`
	ReuseKind       string          `json:"reuse_kind,omitempty"`
	IsInjected      bool            `json:"is_injected"`
	IsDirty         bool            `json:"is_dirty"`
	DirtyReason     string          `json:"dirty_reason,omitempty"`
	InputHash       string          `json:"input_hash,omitempty"`
	OutputHash      string          `json:"output_hash,omitempty"`
	ResolvedInput   map[string]any  `json:"resolved_input,omitempty"`
	Output          map[string]any  `json:"output,omitempty"`
	Checkpoint      map[string]any  `json:"checkpoint,omitempty"`
	ActivatedEdges  map[string]bool `json:"activated_edges,omitempty"`
	Error           string          `json:"error,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	LastHeartbeat   *time.Time      `json:"last_heartbeat,omitempty"`
	CheckpointedAt  *time.Time      `json:"checkpointed_at,omitempty"`
	Progress        float64         `json:"progress"`
	Meta            map[string]any  `json:"meta,omitempty"`
}

type RunNodeParentDTO struct {
	TaskID        int64          `json:"task_id"`
	NodeName      string         `json:"node_name"`
	State         string         `json:"state"`
	InputHash     string         `json:"input_hash,omitempty"`
	OutputHash    string         `json:"output_hash,omitempty"`
	ResolvedInput map[string]any `json:"resolved_input,omitempty"`
	Output        map[string]any `json:"output,omitempty"`
	Checkpoint    map[string]any `json:"checkpoint,omitempty"`
}

type RunNodeDiffDTO struct {
	BaseTaskID     *int64         `json:"base_task_id,omitempty"`
	BaseNodeName   string         `json:"base_node_name,omitempty"`
	InputDiff      []FieldDiffDTO `json:"input_diff,omitempty"`
	OutputDiff     []FieldDiffDTO `json:"output_diff,omitempty"`
	CheckpointDiff []FieldDiffDTO `json:"checkpoint_diff,omitempty"`
	PlanDiff       []FieldDiffDTO `json:"plan_diff,omitempty"`
}

type FieldDiffDTO struct {
	Path     string `json:"path"`
	Change   string `json:"change"` // added/removed/modified
	OldValue any    `json:"old_value,omitempty"`
	NewValue any    `json:"new_value,omitempty"`
}

// RunDAGDTO Run Inspector 左边 DAG 面板的核心。
type RunDAGDTO struct {
	Nodes          []RunDAGNodeDTO `json:"nodes"`
	Edges          []RunDAGEdgeDTO `json:"edges"`
	ActivatedEdges map[string]bool `json:"activated_edges"`
	Stats          RunDAGStatsDTO  `json:"stats"`
}

type RunDAGNodeDTO struct {
	Name         string            `json:"name"`
	Label        string            `json:"label,omitempty"`
	Type         string            `json:"type"`
	Version      string            `json:"version,omitempty"`
	Config       map[string]any    `json:"config,omitempty"`
	InputMapping map[string]string `json:"input_mapping,omitempty"`

	GroupKind   string `json:"group_kind,omitempty"` // map / subworkflow / branch
	GroupID     string `json:"group_id,omitempty"`
	Parallelism int    `json:"parallelism,omitempty"`

	State             string  `json:"state"`
	Action            string  `json:"action"`
	ExecutionReason   string  `json:"execution_reason,omitempty"`
	ReuseKind         string  `json:"reuse_kind,omitempty"`
	IsInjected        bool    `json:"is_injected"`
	IsDirty           bool    `json:"is_dirty"`
	DirtyReason       string  `json:"dirty_reason,omitempty"`
	IsPatched         bool    `json:"is_patched"`
	IsResumeBoundary  bool    `json:"is_resume_boundary"`
	HasCheckpoint     bool    `json:"has_checkpoint"`
	HasOutput         bool    `json:"has_output"`
	Progress          float64 `json:"progress"`
	Index             int     `json:"index"`
	BizIndex          int     `json:"biz_index"`
	Weight            float64 `json:"weight"`
	ParentTaskID      *int64  `json:"parent_task_id,omitempty"`
	ReusedFromTaskID  *int64  `json:"reused_from_task_id,omitempty"`
	ReusedFromNode    *string `json:"reused_from_node,omitempty"`
	PatchCount        int     `json:"patch_count"`
	MapItemReuse      []int   `json:"map_item_reuse,omitempty"`
	ResolvedInputHash string  `json:"input_hash,omitempty"`
	OutputHash        string  `json:"output_hash,omitempty"`

	MapSummary         *RunMapSummaryDTO         `json:"map_summary,omitempty"`
	SubworkflowSummary *RunSubworkflowSummaryDTO `json:"subworkflow_summary,omitempty"`

	Meta map[string]any `json:"meta,omitempty"`
}

type RunDAGEdgeDTO struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Activated bool   `json:"activated"`
	Type      string `json:"type,omitempty"`
	Condition string `json:"condition,omitempty"`
	CaseKey   string `json:"case_key,omitempty"`
	Label     string `json:"label,omitempty"`
	Priority  int    `json:"priority,omitempty"`
}

type RunDAGStatsDTO struct {
	TotalNodes    int `json:"total_nodes"`
	SuccessNodes  int `json:"success_nodes"`
	FailedNodes   int `json:"failed_nodes"`
	RunningNodes  int `json:"running_nodes"`
	SkippedNodes  int `json:"skipped_nodes"`
	PatchedNodes  int `json:"patched_nodes"`
	ReusedNodes   int `json:"reused_nodes"`
	ExecutedNodes int `json:"executed_nodes"`
}

type RuntimePatchDTO struct {
	Target string `json:"target"`
	Node   string `json:"node"`
	Path   string `json:"path"`
	Op     string `json:"op"`
	Value  any    `json:"value,omitempty"`
	Label  string `json:"label,omitempty"`
}

type PatchPreviewReq struct {
	ResumeFrom    string            `json:"resume_from"`
	Patches       []RuntimePatchDTO `json:"patches" binding:"required"`
	OverrideInput map[string]any    `json:"override_input,omitempty"`
}

type PatchPreviewResp struct {
	Valid   bool              `json:"valid"`
	Message string            `json:"message,omitempty"`
	RunPlan RunPlanPreviewDTO `json:"run_plan"`
}

type RunPlanNodePreviewDTO struct {
	Name              string `json:"name"`
	Label             string `json:"label,omitempty"`
	Type              string `json:"type"`
	Action            string `json:"action"`
	Reason            string `json:"reason,omitempty"`
	ReuseKind         string `json:"reuse_kind,omitempty"`
	IsPatched         bool   `json:"is_patched"`
	IsResumeBoundary  bool   `json:"is_resume_boundary"`
	HasFailedChildren bool   `json:"has_failed_children"`
	MapItemReuse      []int  `json:"map_item_reuse,omitempty"`
}

type RunRedoReq struct {
	ResumeFrom    string            `json:"resume_from" binding:"required"`
	Patches       []RuntimePatchDTO `json:"patches,omitempty"`
	OverrideInput map[string]any    `json:"override_input,omitempty"`
	EditAction    string            `json:"edit_action" binding:"required"`
	EditLabel     string            `json:"edit_label,omitempty"`
	Note          string            `json:"note,omitempty"`
}

type RunRedoResp struct {
	TaskID       int64  `json:"task_id"`
	Status       string `json:"status"`
	ParentTaskID int64  `json:"parent_task_id"`
	ResumeFrom   string `json:"resume_from,omitempty"`
}

type RunPlanPreviewSummaryDTO struct {
	ExecuteCount        int `json:"execute_count"`
	ReuseCount          int `json:"reuse_count"`
	PatchCount          int `json:"patch_count"`
	ResumeBoundaryCount int `json:"resume_boundary_count"`
}

type RunPlanPreviewDTO struct {
	Mode         string                   `json:"mode"`
	ResumeFrom   string                   `json:"resume_from,omitempty"`
	ParentTaskID *int64                   `json:"parent_task_id,omitempty"`
	Summary      RunPlanPreviewSummaryDTO `json:"summary"`
	Nodes        []RunPlanNodePreviewDTO  `json:"nodes"`
}

type RunMapSummaryDTO struct {
	ItemCount     int    `json:"item_count"`
	Parallelism   int    `json:"parallelism"`
	SuccessCount  int    `json:"success_count"`
	RunningCount  int    `json:"running_count"`
	FailedCount   int    `json:"failed_count"`
	ReuseCount    int    `json:"reuse_count"`
	ChildWorkflow string `json:"child_workflow,omitempty"`
	Expandable    bool   `json:"expandable"`
}

type RunSubworkflowSummaryDTO struct {
	ChildRunID    *int64 `json:"child_run_id,omitempty"`
	ChildWorkflow string `json:"child_workflow,omitempty"`
	Status        string `json:"status,omitempty"`
	Expandable    bool   `json:"expandable"`
}

type RunNodeExpansionResp struct {
	ParentNodeName    string                     `json:"parent_node_name"`
	Kind              string                     `json:"kind"` // map / subworkflow
	ChildWorkflowName string                     `json:"child_workflow_name,omitempty"`
	ChildRunID        *int64                     `json:"child_run_id,omitempty"`
	ItemCount         *int                       `json:"item_count,omitempty"`
	Items             []RunNodeExpansionItemDTO  `json:"items,omitempty"`
	Nodes             []RunNodeExpansionNodeDTO  `json:"nodes"`
	Edges             []RunNodeExpansionEdgeDTO  `json:"edges"`
	Groups            []RunNodeExpansionGroupDTO `json:"groups,omitempty"`
}

type RunNodeExpansionItemDTO struct {
	ItemIndex    int    `json:"item_index"`
	ItemKey      string `json:"item_key,omitempty"`
	DisplayTitle string `json:"display_title,omitempty"`
	State        string `json:"state"`
	ChildRunID   *int64 `json:"child_run_id,omitempty"`
}

type RunNodeExpansionNodeDTO struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Title    string  `json:"title,omitempty"`
	Kind     string  `json:"kind"`      // start/end/tool/map/subworkflow/virtual_fan_out...
	NodeType string  `json:"node_type"` // 原始类型
	State    string  `json:"state"`
	Action   string  `json:"action"`
	Progress float64 `json:"progress"`

	SourceNodeName string `json:"source_node_name,omitempty"`

	ExecutionReason  string `json:"execution_reason,omitempty"`
	ReuseKind        string `json:"reuse_kind,omitempty"`
	IsInjected       bool   `json:"is_injected"`
	IsDirty          bool   `json:"is_dirty"`
	IsPatched        bool   `json:"is_patched"`
	IsResumeBoundary bool   `json:"is_resume_boundary"`
	HasCheckpoint    bool   `json:"has_checkpoint"`
	HasOutput        bool   `json:"has_output"`
	InputHash        string `json:"input_hash,omitempty"`
	OutputHash       string `json:"output_hash,omitempty"`

	ItemContext *RunNodeExpansionItemRefDTO `json:"item_context,omitempty"`
}

type RunNodeExpansionItemRefDTO struct {
	ItemIndex    int    `json:"item_index"`
	ItemKey      string `json:"item_key,omitempty"`
	DisplayTitle string `json:"display_title,omitempty"`
}

type RunNodeExpansionEdgeDTO struct {
	ID          string `json:"id"`
	FromNodeID  string `json:"from_node_id"`
	ToNodeID    string `json:"to_node_id"`
	Kind        string `json:"kind"` // normal / condition / fan_out / fan_in / item_flow / virtual
	IsActivated bool   `json:"is_activated"`
	Label       string `json:"label,omitempty"`
	Condition   string `json:"condition,omitempty"`
	CaseKey     string `json:"case_key,omitempty"`
	Priority    int    `json:"priority,omitempty"`
}

type RunNodeExpansionGroupDTO struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Kind    string   `json:"kind"` // workflow / map / subworkflow / item_lane / branch_lane
	NodeIDs []string `json:"node_ids"`
}
