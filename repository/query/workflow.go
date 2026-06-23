package query

import (
	"context"
	"flux/domain"
	entity2 "flux/domain/entity"
	"flux/repository"
	"time"

	"gorm.io/gorm"
)

type workflowRepository struct {
	db *gorm.DB
}

func NewWorkflowRepository(db *gorm.DB) repository.WorkflowRepository {
	return &workflowRepository{db: db}
}

func (w *workflowRepository) Create(ctx context.Context, wf *domain.Workflow) error {
	wf.CreatedAt = time.Now()
	wf.UpdatedAt = time.Now()
	err := w.db.WithContext(ctx).
		Create(&wf).Error
	return err
}

func (w *workflowRepository) GetByID(ctx context.Context, id int64) (*domain.Workflow, error) {
	var model entity2.WorkflowModel
	err := w.db.WithContext(ctx).First(&model, "id = ?", id).Error

	return &domain.Workflow{
		ID:          model.ID,
		Name:        model.Name,
		Description: model.Description,
		CreatedAt:   model.CreatedAt,
		UpdatedAt:   model.UpdatedAt,
		UserID:      model.UserID,
	}, err
}

func (w *workflowRepository) Update(ctx context.Context, wf *domain.Workflow) error {
	return w.db.WithContext(ctx).
		Model(&entity2.WorkflowModel{}).
		Where("id = ?", wf.ID).
		Updates(map[string]interface{}{
			"name":        wf.Name,
			"description": wf.Description,
			"updated_at":  time.Now(),
		}).Error
}

func (w *workflowRepository) GetByName(ctx context.Context, name string) (*domain.Workflow, error) {
	var model entity2.WorkflowModel
	err := w.db.WithContext(ctx).First(&model, "name = ?", name).Error
	return &domain.Workflow{
		ID:          model.ID,
		Name:        model.Name,
		Description: model.Description,
		CreatedAt:   model.CreatedAt,
		UpdatedAt:   model.UpdatedAt,
		UserID:      model.UserID,
	}, err
}

func (w *workflowRepository) List(ctx context.Context) ([]*domain.Workflow, error) {
	var models []*entity2.WorkflowModel
	err := w.db.WithContext(ctx).Find(&models).Error
	var workflows []*domain.Workflow
	for _, model := range models {
		workflows = append(workflows, &domain.Workflow{
			ID:          model.ID,
			Name:        model.Name,
			Description: model.Description,
			CreatedAt:   model.CreatedAt,
			UpdatedAt:   model.UpdatedAt,
			UserID:      model.UserID,
		})
	}
	return workflows, err
}
