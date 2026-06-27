package postgres

import (
	"context"
	"encoding/json"
	"sync"

	"flux/runtime"
	"flux/store"

	"gorm.io/gorm"
)

// TraceStore 实现 store.TraceStore，使用 GORM 写入 flux_trace_events 表。
// v3 新增表，独立于现有 domain.TaskEvent 表，避免耦合。
type TraceStore struct {
	db *gorm.DB

	mu sync.Mutex
}

var _ store.TraceStore = (*TraceStore)(nil)

// traceEventModel 是 trace event 的数据库模型。
type traceEventModel struct {
	ID      int64  `gorm:"primaryKey;autoIncrement"`
	TaskID  string `gorm:"index;not null"` // 关联的 task ID
	Seq     int64  `gorm:"not null"`       // 全局单调序列号
	Class   uint8  `gorm:"not null"`       // runtime.TraceClass
	Node    string // 关联节点
	Type    string `gorm:"not null"`       // runtime.TraceType
	Payload []byte // JSON-serialized payload
}

// TableName 指定表名。
func (traceEventModel) TableName() string {
	return "flux_trace_events"
}

func NewTraceStore(db *gorm.DB) *TraceStore {
	// 自动建表（仅在表不存在时）
	_ = db.AutoMigrate(&traceEventModel{})
	return &TraceStore{db: db}
}

func (s *TraceStore) AppendTrace(ctx context.Context, taskID string, events []runtime.TraceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	models := make([]traceEventModel, 0, len(events))
	for _, ev := range events {
		payload, _ := json.Marshal(ev.Payload)
		models = append(models, traceEventModel{
			TaskID:  taskID,
			Seq:     ev.Seq,
			Class:   uint8(ev.Class),
			Node:    ev.Node,
			Type:    string(ev.Type),
			Payload: payload,
		})
	}
	return s.db.WithContext(ctx).Create(&models).Error
}

func (s *TraceStore) ReplayTrace(ctx context.Context, taskID string, sinceSeq int64) ([]runtime.TraceEvent, error) {
	var models []traceEventModel
	if err := s.db.WithContext(ctx).
		Where("task_id = ? AND seq > ?", taskID, sinceSeq).
		Order("seq asc").
		Find(&models).Error; err != nil {
		return nil, err
	}

	out := make([]runtime.TraceEvent, 0, len(models))
	for _, m := range models {
		ev := runtime.TraceEvent{
			Seq:   m.Seq,
			Class: runtime.TraceClass(m.Class),
			Node:  m.Node,
			Type:  runtime.TraceType(m.Type),
		}
		_ = json.Unmarshal(m.Payload, &ev.Payload)
		out = append(out, ev)
	}
	return out, nil
}
