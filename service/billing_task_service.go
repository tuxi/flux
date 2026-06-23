package service

import (
	"context"
	"errors"
	"flux/domain"
	"flux/dto"
)

var (
	ErrBillingInsufficientPoints = errors.New("billing insufficient points")
	ErrBillingEntitlementDenied  = errors.New("billing entitlement denied")
	ErrBillingTaskAlreadyBilled  = errors.New("billing task already billed")
	ErrBillingTaskNotResumable   = errors.New("billing task not resumable")
	ErrBillingInvalidQuoteReq    = errors.New("billing invalid quote request")
)

type pointLotAllocation struct {
	LotID  int64 `json:"lot_id"`
	Points int64 `json:"points"`
}

type BillingTaskService interface {
	BuildQuoteReq(sceneType, sceneKey string, input map[string]any) dto.BillingQuoteReq
	CreateTaskWithFreeze(ctx context.Context, task *domain.Task, quoteReq dto.BillingQuoteReq) error
	CancelTaskFreeze(ctx context.Context, taskID int64, reason string) error
	ConsumeTask(ctx context.Context, taskID int64, actualPoints int64) error
	RefundTask(ctx context.Context, taskID int64, reason string) error
	SettleTaskSuccess(ctx context.Context, taskID int64) error
	SettleTaskSuccessWithDuration(ctx context.Context, taskID int64, actualDurationSec int) error
	SettleTaskFailure(ctx context.Context, taskID int64, reason string) error
	AssertTaskResumable(ctx context.Context, taskID int64) error
	RefreezeTask(ctx context.Context, taskID int64) error
	//GetTaskPointLotDetail(ctx context.Context, taskID int64) (*dto.AdminTaskPointLotDetailRes, error)
}
