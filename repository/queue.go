package repository

import (
	"context"
)

type TaskQueue interface {
	Push(ctx context.Context, taskID int64) error
	//Pop(ctx context.Context) (int64, error)
	PopAndReserve(ctx context.Context) (int64, error)
	Ack(ctx context.Context, taskID int64) error
	MoveToDead(ctx context.Context, taskID int64) error
}
