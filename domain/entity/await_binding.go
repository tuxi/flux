package entity

import (
	"time"

	"gorm.io/datatypes"
)

type AwaitBindingModel struct {
	ID int64 `gorm:"primaryKey"`

	TaskID            int64  `gorm:"not null;index"`
	RootTaskID        int64  `gorm:"not null;index"`
	NodeName          string `gorm:"type:varchar(100);not null;index"`
	WorkflowVersionID int64  `gorm:"not null;index"`

	AwaitType string `gorm:"type:varchar(50);not null;index"`
	Source    string `gorm:"type:varchar(50);not null"`
	Status    string `gorm:"type:varchar(30);not null;index"`

	Provider       *string `gorm:"type:varchar(50);index"`
	ProviderTaskID *string `gorm:"type:varchar(255);index"`
	APITaskID      *string `gorm:"type:varchar(255);index"`
	ExternalTaskID *string `gorm:"type:varchar(255);index"`

	SignalName    *string `gorm:"type:varchar(100);index"`
	MessageName   *string `gorm:"type:varchar(100);index"`
	CallbackToken *string `gorm:"type:varchar(255);index"`

	CorrelationJSON datatypes.JSON `gorm:"type:jsonb"`
	ConfigJSON      datatypes.JSON `gorm:"type:jsonb"`

	LastEventID      *string        `gorm:"type:varchar(255);index"`
	LastEventSource  *string        `gorm:"type:varchar(50)"`
	LastEventPayload datatypes.JSON `gorm:"type:jsonb"`

	ResultPayload datatypes.JSON `gorm:"type:jsonb"`
	ErrorMessage  *string        `gorm:"type:text"`

	FallbackPollEnabled bool       `gorm:"not null;default:false"`
	FallbackPollTool    *string    `gorm:"type:varchar(100)"`
	PollAttempts        int        `gorm:"not null;default:0"`
	MaxPollAttempts     int        `gorm:"not null;default:0"`
	LastPolledAt        *time.Time `gorm:"index"`
	NextPollAt          *time.Time `gorm:"index"`

	WaitingStartedAt *time.Time `gorm:"index"`
	TimeoutAt        *time.Time `gorm:"index"`
	CompletedAt      *time.Time `gorm:"index"`
	FailedAt         *time.Time `gorm:"index"`
	CanceledAt       *time.Time `gorm:"index"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (AwaitBindingModel) TableName() string {
	return "await_bindings"
}
