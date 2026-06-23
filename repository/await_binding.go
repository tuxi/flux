package repository

import (
	"context"
	"time"

	"flux/domain"
)

type AwaitBindingRepository interface {
	Create(ctx context.Context, b *domain.AwaitBinding) error
	Update(ctx context.Context, b *domain.AwaitBinding) error

	GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error)
	ListByTaskID(ctx context.Context, taskID int64) ([]*domain.AwaitBinding, error)
	GetByTaskAndNode(ctx context.Context, taskID int64, nodeName string) (*domain.AwaitBinding, error)

	FindWaitingByProviderTaskID(ctx context.Context, provider, providerTaskID string) (*domain.AwaitBinding, error)
	FindWaitingByAPITaskID(ctx context.Context, provider, apiTaskID string) (*domain.AwaitBinding, error)
	FindWaitingBySignal(ctx context.Context, signalName, callbackToken string) (*domain.AwaitBinding, error)

	TransitionStatus(ctx context.Context, id int64, from domain.AwaitBindingStatus, to domain.AwaitBindingStatus) (bool, error)
	ClaimCompleting(ctx context.Context, id int64, expectedStatuses []domain.AwaitBindingStatus) (bool, error)

	FindPollDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error)
	FindTimeoutDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error)
}
