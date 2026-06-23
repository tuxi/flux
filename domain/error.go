package domain

import "fmt"

type SuspendReason string

const (
	// SuspendAsyncNode 用来调度异步节点抛出的挂起错误
	SuspendAsyncNode SuspendReason = "async_node"
	// SuspendSubWorkflow 用来调度子工作流节点抛出的挂起错误
	SuspendSubWorkflow SuspendReason = "sub_workflow"
	// SuspendWaitingGate 用来调度时间门控节点的挂起错误
	SuspendWaitingGate SuspendReason = "waiting_gate"
)

type WorkflowSuspendedError struct {
	Reason SuspendReason
}

func (e *WorkflowSuspendedError) Error() string {
	return fmt.Sprintf("workflow suspended: %s", e.Reason)
}
