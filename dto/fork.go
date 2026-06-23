package dto

// ForkTaskReq Fork 任务请求
type ForkTaskReq struct {
	OverrideInput map[string]any `json:"override_input,omitempty"`
	ResumeSpec    *ResumeSpecReq `json:"resume_spec,omitempty"`
	EditAction    string         `json:"edit_action,omitempty"`
	EditLabel     string         `json:"edit_label,omitempty"`
}

// ResumeSpecReq 恢复执行的规格说明
type ResumeSpecReq struct {
	ResumeFrom string            `json:"resume_from"`
	Patches    []RuntimePatchDTO `json:"patches,omitempty"`
}

// ForkTaskResp Fork 任务响应
type ForkTaskResp struct {
	TaskID     int64   `json:"task_id"`
	ForkedFrom int64   `json:"forked_from"`
	Status     string  `json:"status"`
	BaseRunID  int64   `json:"base_run_id"`
	RunDepth   int     `json:"run_depth"`
	ResumeFrom *string `json:"resume_from,omitempty"`
}

// UserNodeDataDTO 用户侧节点可编辑数据（轻量版）。
// GET /user/works/:id/nodes/:node
type UserNodeDataDTO struct {
	Name         string            `json:"name"`
	Label        string            `json:"label,omitempty"`
	Type         string            `json:"type"`
	State        string            `json:"state"`
	InputMapping map[string]string `json:"input_mapping"`
	Output       map[string]any    `json:"output"`
}
