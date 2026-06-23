package dto

type CreateWorkflowReq struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type NodeReq struct {
	Name string `json:"name" binding:"required"`
	// 可扩展 InputSchema / OutputSchema / ToolName / Version
}

type RunWorkflowReq struct {
	WorkflowName string         `json:"workflow_name" binding:"required"`
	Input        map[string]any `json:"input"`
}
