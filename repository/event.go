package repository

import (
	"context"

	"flux/domain"
)

type EventRepository interface {
	Create(ctx context.Context, event *domain.TaskEvent) error
	FindByTaskID(ctx context.Context, taskID int64, isByRoot bool) ([]domain.TaskEvent, error)
	FindByTaskIDAndTypePrefixes(ctx context.Context, taskID int64, prefixes []string, isByRoot bool) ([]domain.TaskEvent, error)
	// FindPersistentByTaskID returns only persistent-grade events, ordered by sequence.
	// If afterSequence > 0, only events with sequence > afterSequence are returned (incremental recovery).
	FindPersistentByTaskID(ctx context.Context, taskID int64, afterSequence int64, limit int, isByRoot bool) ([]domain.TaskEvent, error)
}
