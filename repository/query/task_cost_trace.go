package query

import (
	"context"
	"encoding/json"

	"flux/domain"
	"flux/domain/entity"
	"flux/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type taskCostTraceRepository struct {
	db *gorm.DB
}

func NewTaskCostTraceRepository(db *gorm.DB) repository.TaskCostTraceRepository {
	return &taskCostTraceRepository{db: db}
}

func (r *taskCostTraceRepository) Upsert(ctx context.Context, trace *domain.TaskCostTrace) error {
	payload, _ := json.Marshal(trace.TracePayload)

	model := entity.TaskCostTraceModel{
		ID:                trace.ID,
		TaskID:            trace.TaskID,
		RootTaskID:        trace.RootTaskID,
		NodeRuntimeID:     trace.NodeRuntimeID,
		WorkflowName:      trace.WorkflowName,
		NodeName:          trace.NodeName,
		StepName:          trace.StepName,
		ResourceType:      trace.ResourceType,
		Provider:          trace.Provider,
		Model:             trace.Model,
		ProviderRequestID: trace.ProviderRequestID,
		UsageQuantity:     trace.UsageQuantity,
		UsageUnit:         trace.UsageUnit,
		UnitPrice:         trace.UnitPrice,
		EstimatedCost:     trace.EstimatedCost,
		ActualCost:        trace.ActualCost,
		Currency:          trace.Currency,
		Status:            trace.Status,
		IdempotencyKey:    trace.IdempotencyKey,
		TracePayload:      payload,
	}

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "idempotency_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"usage_quantity", "usage_unit", "unit_price", "estimated_cost", "actual_cost", "currency", "status", "trace_payload", "updated_at"}),
	}).Create(&model).Error
}

func (r *taskCostTraceRepository) ListByTaskID(ctx context.Context, taskID int64) ([]*domain.TaskCostTrace, error) {
	var models []entity.TaskCostTraceModel
	if err := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("created_at asc, id asc").
		Find(&models).Error; err != nil {
		return nil, err
	}

	result := make([]*domain.TaskCostTrace, 0, len(models))
	for _, model := range models {
		tracePayload := map[string]any{}
		if len(model.TracePayload) > 0 {
			_ = json.Unmarshal(model.TracePayload, &tracePayload)
		}
		result = append(result, &domain.TaskCostTrace{
			ID:                model.ID,
			TaskID:            model.TaskID,
			RootTaskID:        model.RootTaskID,
			NodeRuntimeID:     model.NodeRuntimeID,
			WorkflowName:      model.WorkflowName,
			NodeName:          model.NodeName,
			StepName:          model.StepName,
			ResourceType:      model.ResourceType,
			Provider:          model.Provider,
			Model:             model.Model,
			ProviderRequestID: model.ProviderRequestID,
			UsageQuantity:     model.UsageQuantity,
			UsageUnit:         model.UsageUnit,
			UnitPrice:         model.UnitPrice,
			EstimatedCost:     model.EstimatedCost,
			ActualCost:        model.ActualCost,
			Currency:          model.Currency,
			Status:            model.Status,
			IdempotencyKey:    model.IdempotencyKey,
			TracePayload:      tracePayload,
			CreatedAt:         model.CreatedAt,
			UpdatedAt:         model.UpdatedAt,
		})
	}
	return result, nil
}

func (r *taskCostTraceRepository) SumEstimatedCostByTaskID(ctx context.Context, taskID int64) (float64, error) {
	var sum float64
	err := r.db.WithContext(ctx).
		Model(&entity.TaskCostTraceModel{}).
		Where("task_id = ?", taskID).
		Select("COALESCE(SUM(estimated_cost), 0)").
		Scan(&sum).Error
	return sum, err
}

func (r *taskCostTraceRepository) SumEstimatedCostByRootTaskID(ctx context.Context, rootTaskID int64) (float64, error) {
	var sum float64
	err := r.db.WithContext(ctx).
		Model(&entity.TaskCostTraceModel{}).
		Where("root_task_id = ?", rootTaskID).
		Select("COALESCE(SUM(estimated_cost), 0)").
		Scan(&sum).Error
	return sum, err
}
