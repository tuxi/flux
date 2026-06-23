package query

import (
	"context"
	"encoding/json"

	"flux/domain"
	"flux/domain/entity"
	"flux/repository"

	"gorm.io/gorm"
)

type eventRepository struct {
	db *gorm.DB
}

func NewEventRepository(db *gorm.DB) repository.EventRepository {
	return &eventRepository{db: db}
}

func (r *eventRepository) Create(ctx context.Context, event *domain.TaskEvent) error {
	meta, _ := json.Marshal(event.Meta)
	model := entity.TaskEventModel{
		TaskID:     event.TaskID,
		RootTaskID: event.RootTaskID,
		Step:       event.Step,
		Message:    event.Message,
		Meta:       meta,
		Error:      event.Error,
		CreatedAt:  event.CreatedAt,
		Type:       event.Type,
		Progress:   event.Progress,
		Level:      event.Level,
		NodeIndex:  event.NodeIndex,
		NodeTotal:  event.NodeTotal,
		Grade:      string(event.Grade),
	}

	err := r.db.WithContext(ctx).Create(&model).Error
	if err != nil {
		return err
	}

	// 回填 DB 自增 ID 作为全局 sequence
	event.Sequence = model.ID
	return nil
}

func (r *eventRepository) FindByTaskID(ctx context.Context, taskID int64, isByRoot bool) ([]domain.TaskEvent, error) {
	var taskIDKey = "task_id"
	if isByRoot {
		taskIDKey = "root_task_id"
	}
	var models []entity.TaskEventModel
	err := r.db.WithContext(ctx).
		Find(&models, "%s = ?", taskIDKey, taskID).
		Order("created_at asc, id asc").
		Error
	if err != nil {
		return nil, err
	}
	return toDomainEvents(models), nil
}

func (r *eventRepository) FindByTaskIDAndTypePrefixes(ctx context.Context, taskID int64, prefixes []string, isByRoot bool) ([]domain.TaskEvent, error) {
	query := r.db.WithContext(ctx)
	if isByRoot {
		query = query.Where("root_task_id = ?", taskID)
	} else {
		query = query.Where("task_id = ?", taskID)
	}

	if len(prefixes) > 0 {
		orDB := query.Session(&gorm.Session{}).Where("1 = 0")
		for _, prefix := range prefixes {
			orDB = orDB.Or("type LIKE ?", prefix+"%")
		}
		query = query.Where(orDB)
	}
	var models []entity.TaskEventModel
	err := query.Order("created_at asc, id asc").Find(&models).Error
	if err != nil {
		return nil, err
	}
	return toDomainEvents(models), nil
}

func (r *eventRepository) FindPersistentByTaskID(ctx context.Context, taskID int64, afterSequence int64, limit int, isByRoot bool) ([]domain.TaskEvent, error) {

	query := r.db.WithContext(ctx)
	if isByRoot {
		query = query.Where("root_task_id = ?", taskID)
	} else {
		query = query.Where("task_id = ?", taskID)
	}

	query = query.
		Where("grade = ?", domain.GradePersistent)
	if afterSequence > 0 {
		query = query.Where("id > ?", afterSequence)
	}
	if limit <= 0 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}
	var models []entity.TaskEventModel
	err := query.Order("id asc").Limit(limit).Find(&models).Error
	if err != nil {
		return nil, err
	}
	return toDomainEvents(models), nil
}

func toDomainEvents(models []entity.TaskEventModel) []domain.TaskEvent {
	events := make([]domain.TaskEvent, 0, len(models))
	for _, m := range models {
		var meta map[string]any
		_ = json.Unmarshal(m.Meta, &meta)
		events = append(events, domain.TaskEvent{
			ID:         m.ID,
			TaskID:     m.TaskID,
			RootTaskID: m.RootTaskID,
			Step:       m.Step,
			Message:    m.Message,
			Meta:       meta,
			Error:      m.Error,
			CreatedAt:  m.CreatedAt,
			Type:       m.Type,
			Progress:   m.Progress,
			Level:      m.Level,
			NodeIndex:  m.NodeIndex,
			NodeTotal:  m.NodeTotal,
			Grade:      domain.EventGrade(m.Grade),
			Sequence:   m.ID, // DB auto-increment ID is the global sequence
		})
	}
	return events
}
