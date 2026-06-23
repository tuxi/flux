package domain

import "time"

type TaskCostTrace struct {
	ID int64 `json:"id"`

	TaskID        int64  `json:"task_id"`
	RootTaskID    int64  `json:"root_task_id"`
	NodeRuntimeID *int64 `json:"node_runtime_id,omitempty"`

	WorkflowName string `json:"workflow_name,omitempty"`
	NodeName     string `json:"node_name,omitempty"`
	StepName     string `json:"step_name,omitempty"`

	ResourceType string `json:"resource_type"`
	Provider     string `json:"provider"`
	Model        string `json:"model,omitempty"`

	ProviderRequestID string  `json:"provider_request_id,omitempty"`
	UsageQuantity     float64 `json:"usage_quantity"`
	UsageUnit         string  `json:"usage_unit"`
	UnitPrice         float64 `json:"unit_price"`
	EstimatedCost     float64 `json:"estimated_cost"`
	ActualCost        float64 `json:"actual_cost"`
	Currency          string  `json:"currency"`
	Status            string  `json:"status"`
	IdempotencyKey    string  `json:"idempotency_key"`

	TracePayload map[string]any `json:"trace_payload,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
