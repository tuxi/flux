package engine

type AsyncJob struct {
	TaskID      int64          `json:"task_id"`
	Node        string         `json:"node"`
	StepAdapter string         `json:"step_adapter"`
	Input       map[string]any `json:"input"`
	Hash        string         `json:"hash"`
}
