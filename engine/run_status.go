package engine

type RunStatus string

const (
	RunSuccess   RunStatus = "success"
	RunSuspended RunStatus = "suspended"
	RunFailed    RunStatus = "failed"
	RunNoop      RunStatus = "noop"
)

type RunResult struct {
	Status RunStatus
	Err    error

	// 可选
	SuspendReason string
	SuspendNode   string
}
