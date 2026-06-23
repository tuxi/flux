package repository

import (
	"context"
	"flux/domain"
)

type WorkflowVersionRepository interface {
	Create(ctx context.Context, version *domain.WorkflowVersion) error
	Get(ctx context.Context, id int64) (*domain.WorkflowVersion, error)
	GetLatestByWorkflowID(ctx context.Context, id int64) (*domain.WorkflowVersion, error)
	GetLatestByWorkflowName(ctx context.Context, name string) (*domain.WorkflowVersion, error)
	UpdateDefinitionJSON(ctx context.Context, versionID int64, json []byte) error
}
