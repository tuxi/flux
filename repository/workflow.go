package repository

import (
	"context"
	"flux/domain"
)

// WorkflowRepository 存 工作流定义 元信息
type WorkflowRepository interface {
	Create(ctx context.Context, wf *domain.Workflow) error
	Update(ctx context.Context, wf *domain.Workflow) error
	GetByID(ctx context.Context, id int64) (*domain.Workflow, error)
	GetByName(ctx context.Context, name string) (*domain.Workflow, error)
	List(ctx context.Context) ([]*domain.Workflow, error)
}
