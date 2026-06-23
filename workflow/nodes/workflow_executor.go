package nodes

import (
	"flux/repository"
)

// WorkflowExecutor 工作流执行协议
type WorkflowExecutor interface {
	RunSubWorkflow(
		execCtx *NodeExecContext,
		workflowName string,
		input map[string]any,
	) (map[string]any, error)

	TaskRepo() repository.TaskRepository
	NodeRepo() repository.NodeRuntimeRepository
}
