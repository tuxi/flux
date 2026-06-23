package query

import (
	"context"
	"flux/domain"
	"flux/domain/entity"
	"flux/repository"

	"gorm.io/gorm"
)

type workflowVersionRepository struct {
	db *gorm.DB
}

func NewWorkflowVersionRepository(db *gorm.DB) repository.WorkflowVersionRepository {
	return &workflowVersionRepository{db: db}
}

func (r *workflowVersionRepository) Get(
	ctx context.Context,
	id int64,
) (*domain.WorkflowVersion, error) {

	var version entity.WorkflowVersionModel

	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&version).Error

	if err != nil {
		return nil, err
	}

	return &domain.WorkflowVersion{
		ID:             version.ID,
		WorkflowID:     version.WorkflowID,
		Version:        version.Version,
		DefinitionJSON: version.DefinitionJSON,
		Hash:           version.Hash,
		CreatedAt:      version.CreatedAt,
	}, nil
}

func (r *workflowVersionRepository) GetLatestByWorkflowID(ctx context.Context, id int64) (*domain.WorkflowVersion, error) {
	var version entity.WorkflowVersionModel
	err := r.db.WithContext(ctx).
		Where("workflow_id = ?", id).
		Order("version DESC").
		Limit(1).
		First(&version).Error
	if err != nil {
		return nil, err
	}
	return &domain.WorkflowVersion{
		ID:             version.ID,
		WorkflowID:     version.WorkflowID,
		Version:        version.Version,
		DefinitionJSON: version.DefinitionJSON,
		Hash:           version.Hash,
		CreatedAt:      version.CreatedAt,
	}, nil
}

func (r *workflowVersionRepository) UpdateDefinitionJSON(ctx context.Context, versionID int64, json []byte) error {
	return r.db.WithContext(ctx).
		Model(entity.WorkflowVersionModel{}).
		Where("id = ?", versionID).
		Update("definition_json", json).Error
}

func (r *workflowVersionRepository) Create(ctx context.Context, version *domain.WorkflowVersion) error {
	err := r.db.WithContext(ctx).Model(entity.WorkflowVersionModel{}).Create(version).Error
	return err
}

func (r *workflowVersionRepository) GetLatestByWorkflowName(
	ctx context.Context,
	name string,
) (*domain.WorkflowVersion, error) {
	var version entity.WorkflowVersionModel

	err := r.db.WithContext(ctx).
		Table("workflow_versions AS wv").
		Select("wv.*").
		Joins("JOIN workflows AS wd ON wd.id = wv.workflow_id").
		Where("wd.name = ?", name).
		Order("wv.version DESC").
		Limit(1).
		First(&version).Error
	if err != nil {
		return nil, err
	}

	return &domain.WorkflowVersion{
		ID:             version.ID,
		WorkflowID:     version.WorkflowID,
		Version:        version.Version,
		DefinitionJSON: version.DefinitionJSON,
		Hash:           version.Hash,
		CreatedAt:      version.CreatedAt,
	}, nil
}
