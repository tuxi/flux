package domain

import "time"

type AwaitType string

const (
	AwaitTypeExternalTask AwaitType = "external_task"
	AwaitTypeUserInput    AwaitType = "user_input"
	AwaitTypeMessage      AwaitType = "message"
	AwaitTypeApproval     AwaitType = "approval"
	// AwaitTypeSubWorkflow 把"父节点等待 subworkflow 子任务完成"建模成 await binding，
	// 复用 await 的状态机 / poll / timeout 基建，统一父子任务的"等待—恢复"机制。
	AwaitTypeSubWorkflow AwaitType = "subworkflow"
)

type AwaitSource string

const (
	AwaitSourceWebhook       AwaitSource = "webhook"
	AwaitSourceSignal        AwaitSource = "signal"
	AwaitSourcePoll          AwaitSource = "poll"
	AwaitSourceWebhookOrPoll AwaitSource = "webhook_or_poll"
	AwaitSourceMessage       AwaitSource = "message"
	AwaitSourceSubWorkflow   AwaitSource = "subworkflow"
)

type AwaitBindingStatus string

const (
	AwaitBindingPending    AwaitBindingStatus = "pending"
	AwaitBindingWaiting    AwaitBindingStatus = "waiting"
	AwaitBindingCompleting AwaitBindingStatus = "completing"
	AwaitBindingCompleted  AwaitBindingStatus = "completed"
	AwaitBindingFailed     AwaitBindingStatus = "failed"
	AwaitBindingTimedOut   AwaitBindingStatus = "timed_out"
	AwaitBindingCanceled   AwaitBindingStatus = "canceled"
)

var AllowedTransitionsAwaitBinding = map[AwaitBindingStatus][]AwaitBindingStatus{
	AwaitBindingPending:    {AwaitBindingWaiting, AwaitBindingCanceled},
	AwaitBindingWaiting:    {AwaitBindingCompleting, AwaitBindingTimedOut, AwaitBindingCanceled},
	AwaitBindingCompleting: {AwaitBindingCompleted, AwaitBindingFailed},
}

type AwaitBinding struct {
	ID                int64              `json:"id"`
	TaskID            int64              `json:"task_id"`
	RootTaskID        int64              `json:"root_task_id"`
	NodeName          string             `json:"node_name"`
	WorkflowVersionID int64              `json:"workflow_version_id"`
	AwaitType         AwaitType          `json:"await_type"`
	Source            AwaitSource        `json:"source"`
	Status            AwaitBindingStatus `json:"status"`
	Provider          *string            `json:"provider,omitempty"`
	ProviderTaskID    *string            `json:"provider_task_id,omitempty"`
	APITaskID         *string            `json:"api_task_id,omitempty"`
	ExternalTaskID    *string            `json:"external_task_id,omitempty"`
	SignalName        *string            `json:"signal_name,omitempty"`
	MessageName       *string            `json:"message_name,omitempty"`
	CallbackToken     *string            `json:"callback_token,omitempty"`
	Correlation       map[string]any     `json:"correlation,omitempty"`
	Config            map[string]any     `json:"config,omitempty"`
	LastEventID       *string            `json:"last_event_id,omitempty"`
	LastEventSource   *string            `json:"last_event_source,omitempty"`
	LastEventPayload  map[string]any     `json:"last_event_payload,omitempty"`
	ResultPayload     map[string]any     `json:"result_payload,omitempty"`
	ErrorMessage      string             `json:"error_message,omitempty"`

	FallbackPollEnabled bool       `json:"fallback_poll_enabled"`
	FallbackPollTool    *string    `json:"fallback_poll_tool,omitempty"`
	PollAttempts        int        `json:"poll_attempts"`
	MaxPollAttempts     int        `json:"max_poll_attempts"`
	LastPolledAt        *time.Time `json:"last_polled_at,omitempty"`
	NextPollAt          *time.Time `json:"next_poll_at,omitempty"`

	WaitingStartedAt *time.Time `json:"waiting_started_at,omitempty"`
	TimeoutAt        *time.Time `json:"timeout_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	FailedAt         *time.Time `json:"failed_at,omitempty"`
	CanceledAt       *time.Time `json:"canceled_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func IsAllowedAwaitBindingTransition(from, to AwaitBindingStatus) bool {
	for _, candidate := range AllowedTransitionsAwaitBinding[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

func (b *AwaitBinding) CanTransitionTo(to AwaitBindingStatus) bool {
	if b == nil {
		return false
	}
	return IsAllowedAwaitBindingTransition(b.Status, to)
}
