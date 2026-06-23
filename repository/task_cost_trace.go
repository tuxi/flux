package repository

import (
	"context"

	"flux/domain"
)

type TaskCostTraceRepository interface {
	Upsert(ctx context.Context, trace *domain.TaskCostTrace) error
	ListByTaskID(ctx context.Context, taskID int64) ([]*domain.TaskCostTrace, error)
	SumEstimatedCostByTaskID(ctx context.Context, taskID int64) (float64, error)
	SumEstimatedCostByRootTaskID(ctx context.Context, rootTaskID int64) (float64, error)
}
