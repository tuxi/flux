package query

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flux/domain"
	"flux/domain/entity"
	"flux/repository"
	"gorm.io/gorm"
)

type awaitBindingRepository struct {
	db *gorm.DB
}

func NewAwaitBindingRepository(db *gorm.DB) repository.AwaitBindingRepository {
	return &awaitBindingRepository{db: db}
}

func (r *awaitBindingRepository) Create(ctx context.Context, b *domain.AwaitBinding) error {
	model := entity.AwaitBindingModel{
		ID:                  b.ID,
		TaskID:              b.TaskID,
		RootTaskID:          b.RootTaskID,
		NodeName:            b.NodeName,
		WorkflowVersionID:   b.WorkflowVersionID,
		AwaitType:           string(b.AwaitType),
		Source:              string(b.Source),
		Status:              string(b.Status),
		Provider:            b.Provider,
		ProviderTaskID:      b.ProviderTaskID,
		APITaskID:           b.APITaskID,
		ExternalTaskID:      b.ExternalTaskID,
		SignalName:          b.SignalName,
		MessageName:         b.MessageName,
		CallbackToken:       b.CallbackToken,
		CorrelationJSON:     mustJSON(b.Correlation),
		ConfigJSON:          mustJSON(b.Config),
		LastEventID:         b.LastEventID,
		LastEventSource:     b.LastEventSource,
		LastEventPayload:    mustJSON(b.LastEventPayload),
		ResultPayload:       mustJSON(b.ResultPayload),
		FallbackPollEnabled: b.FallbackPollEnabled,
		FallbackPollTool:    b.FallbackPollTool,
		PollAttempts:        b.PollAttempts,
		MaxPollAttempts:     b.MaxPollAttempts,
		LastPolledAt:        b.LastPolledAt,
		NextPollAt:          b.NextPollAt,
		WaitingStartedAt:    b.WaitingStartedAt,
		TimeoutAt:           b.TimeoutAt,
		CompletedAt:         b.CompletedAt,
		FailedAt:            b.FailedAt,
		CanceledAt:          b.CanceledAt,
	}
	if b.ErrorMessage != "" {
		model.ErrorMessage = &b.ErrorMessage
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	b.ID = model.ID
	return nil
}

func (r *awaitBindingRepository) Update(ctx context.Context, b *domain.AwaitBinding) error {
	updates := map[string]any{
		"task_id":               b.TaskID,
		"root_task_id":          b.RootTaskID,
		"node_name":             b.NodeName,
		"workflow_version_id":   b.WorkflowVersionID,
		"await_type":            string(b.AwaitType),
		"source":                string(b.Source),
		"status":                string(b.Status),
		"provider":              b.Provider,
		"provider_task_id":      b.ProviderTaskID,
		"api_task_id":           b.APITaskID,
		"external_task_id":      b.ExternalTaskID,
		"signal_name":           b.SignalName,
		"message_name":          b.MessageName,
		"callback_token":        b.CallbackToken,
		"correlation_json":      mustJSON(b.Correlation),
		"config_json":           mustJSON(b.Config),
		"last_event_id":         b.LastEventID,
		"last_event_source":     b.LastEventSource,
		"last_event_payload":    mustJSON(b.LastEventPayload),
		"result_payload":        mustJSON(b.ResultPayload),
		"fallback_poll_enabled": b.FallbackPollEnabled,
		"fallback_poll_tool":    b.FallbackPollTool,
		"poll_attempts":         b.PollAttempts,
		"max_poll_attempts":     b.MaxPollAttempts,
		"last_polled_at":        b.LastPolledAt,
		"next_poll_at":          b.NextPollAt,
		"waiting_started_at":    b.WaitingStartedAt,
		"timeout_at":            b.TimeoutAt,
		"completed_at":          b.CompletedAt,
		"failed_at":             b.FailedAt,
		"canceled_at":           b.CanceledAt,
		"updated_at":            time.Now(),
		"error_message":         nil,
	}
	if b.ErrorMessage != "" {
		updates["error_message"] = b.ErrorMessage
	}

	return r.db.WithContext(ctx).
		Model(&entity.AwaitBindingModel{}).
		Where("id = ?", b.ID).
		Updates(updates).Error
}

func (r *awaitBindingRepository) GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error) {
	var model entity.AwaitBindingModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&model).Error; err != nil {
		return nil, err
	}
	return toAwaitBinding(&model), nil
}

func (r *awaitBindingRepository) ListByTaskID(ctx context.Context, taskID int64) ([]*domain.AwaitBinding, error) {
	var models []entity.AwaitBindingModel
	if err := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("created_at asc, id asc").
		Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.AwaitBinding, 0, len(models))
	for i := range models {
		out = append(out, toAwaitBinding(&models[i]))
	}
	return out, nil
}

func (r *awaitBindingRepository) GetByTaskAndNode(ctx context.Context, taskID int64, nodeName string) (*domain.AwaitBinding, error) {
	var model entity.AwaitBindingModel
	err := r.db.WithContext(ctx).
		Where("task_id = ? AND node_name = ?", taskID, nodeName).
		First(&model).Error
	if err != nil {
		return nil, err
	}
	return toAwaitBinding(&model), nil
}

func (r *awaitBindingRepository) FindWaitingByProviderTaskID(ctx context.Context, provider, providerTaskID string) (*domain.AwaitBinding, error) {
	return r.findOne(ctx,
		"provider = ? AND provider_task_id = ? AND status = ?",
		provider, providerTaskID, string(domain.AwaitBindingWaiting),
	)
}

func (r *awaitBindingRepository) FindWaitingByAPITaskID(ctx context.Context, provider, apiTaskID string) (*domain.AwaitBinding, error) {
	return r.findOne(ctx,
		"provider = ? AND api_task_id = ? AND status = ?",
		provider, apiTaskID, string(domain.AwaitBindingWaiting),
	)
}

func (r *awaitBindingRepository) FindWaitingBySignal(ctx context.Context, signalName, callbackToken string) (*domain.AwaitBinding, error) {
	return r.findOne(ctx,
		"signal_name = ? AND callback_token = ? AND status = ?",
		signalName, callbackToken, string(domain.AwaitBindingWaiting),
	)
}

func (r *awaitBindingRepository) TransitionStatus(ctx context.Context, id int64, from domain.AwaitBindingStatus, to domain.AwaitBindingStatus) (bool, error) {
	if !domain.IsAllowedAwaitBindingTransition(from, to) {
		return false, fmt.Errorf("illegal await binding transition: %s -> %s", from, to)
	}

	result := r.db.WithContext(ctx).
		Model(&entity.AwaitBindingModel{}).
		Where("id = ? AND status = ?", id, string(from)).
		Updates(map[string]any{
			"status":     string(to),
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *awaitBindingRepository) ClaimCompleting(ctx context.Context, id int64, expectedStatuses []domain.AwaitBindingStatus) (bool, error) {
	if len(expectedStatuses) == 0 {
		expectedStatuses = []domain.AwaitBindingStatus{domain.AwaitBindingWaiting}
	}
	statuses := make([]string, 0, len(expectedStatuses))
	for _, status := range expectedStatuses {
		if !domain.IsAllowedAwaitBindingTransition(status, domain.AwaitBindingCompleting) {
			return false, fmt.Errorf("illegal await binding transition: %s -> %s", status, domain.AwaitBindingCompleting)
		}
		statuses = append(statuses, string(status))
	}

	result := r.db.WithContext(ctx).
		Model(&entity.AwaitBindingModel{}).
		Where("id = ? AND status IN ?", id, statuses).
		Updates(map[string]any{
			"status":     string(domain.AwaitBindingCompleting),
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *awaitBindingRepository) FindPollDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return r.findMany(ctx,
		"status = ? AND next_poll_at IS NOT NULL AND next_poll_at <= ?",
		limit, string(domain.AwaitBindingWaiting), now,
	)
}

func (r *awaitBindingRepository) FindTimeoutDue(ctx context.Context, now time.Time, limit int) ([]*domain.AwaitBinding, error) {
	return r.findMany(ctx,
		"status = ? AND timeout_at IS NOT NULL AND timeout_at <= ?",
		limit, string(domain.AwaitBindingWaiting), now,
	)
}

func (r *awaitBindingRepository) findOne(ctx context.Context, query string, args ...any) (*domain.AwaitBinding, error) {
	var model entity.AwaitBindingModel
	err := r.db.WithContext(ctx).Where(query, args...).First(&model).Error
	if err != nil {
		return nil, err
	}
	return toAwaitBinding(&model), nil
}

func (r *awaitBindingRepository) findMany(ctx context.Context, query string, limit int, args ...any) ([]*domain.AwaitBinding, error) {
	if limit <= 0 {
		limit = 100
	}
	var models []entity.AwaitBindingModel
	err := r.db.WithContext(ctx).Where(query, args...).Limit(limit).Find(&models).Error
	if err != nil {
		return nil, err
	}
	out := make([]*domain.AwaitBinding, 0, len(models))
	for i := range models {
		out = append(out, toAwaitBinding(&models[i]))
	}
	return out, nil
}

func toAwaitBinding(model *entity.AwaitBindingModel) *domain.AwaitBinding {
	if model == nil {
		return nil
	}
	var correlation map[string]any
	_ = json.Unmarshal(model.CorrelationJSON, &correlation)
	var config map[string]any
	_ = json.Unmarshal(model.ConfigJSON, &config)
	var lastEventPayload map[string]any
	_ = json.Unmarshal(model.LastEventPayload, &lastEventPayload)
	var resultPayload map[string]any
	_ = json.Unmarshal(model.ResultPayload, &resultPayload)

	errMsg := ""
	if model.ErrorMessage != nil {
		errMsg = *model.ErrorMessage
	}

	return &domain.AwaitBinding{
		ID:                  model.ID,
		TaskID:              model.TaskID,
		RootTaskID:          model.RootTaskID,
		NodeName:            model.NodeName,
		WorkflowVersionID:   model.WorkflowVersionID,
		AwaitType:           domain.AwaitType(model.AwaitType),
		Source:              domain.AwaitSource(model.Source),
		Status:              domain.AwaitBindingStatus(model.Status),
		Provider:            model.Provider,
		ProviderTaskID:      model.ProviderTaskID,
		APITaskID:           model.APITaskID,
		ExternalTaskID:      model.ExternalTaskID,
		SignalName:          model.SignalName,
		MessageName:         model.MessageName,
		CallbackToken:       model.CallbackToken,
		Correlation:         correlation,
		Config:              config,
		LastEventID:         model.LastEventID,
		LastEventSource:     model.LastEventSource,
		LastEventPayload:    lastEventPayload,
		ResultPayload:       resultPayload,
		ErrorMessage:        errMsg,
		FallbackPollEnabled: model.FallbackPollEnabled,
		FallbackPollTool:    model.FallbackPollTool,
		PollAttempts:        model.PollAttempts,
		MaxPollAttempts:     model.MaxPollAttempts,
		LastPolledAt:        model.LastPolledAt,
		NextPollAt:          model.NextPollAt,
		WaitingStartedAt:    model.WaitingStartedAt,
		TimeoutAt:           model.TimeoutAt,
		CompletedAt:         model.CompletedAt,
		FailedAt:            model.FailedAt,
		CanceledAt:          model.CanceledAt,
		CreatedAt:           model.CreatedAt,
		UpdatedAt:           model.UpdatedAt,
	}
}

func mustJSON(data map[string]any) []byte {
	if len(data) == 0 {
		return nil
	}
	raw, _ := json.Marshal(data)
	return raw
}
