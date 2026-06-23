package cost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flux/domain"
)

type RecordContext struct {
	NodeRuntimeID int64
	TaskID        int64
	RootTaskID    int64
	WorkflowName  string
	NodeName      string
	NodeType      string
	StepName      string
	Input         map[string]any
	OutputHash    string
	Output        map[string]any
	UsageFacts    []map[string]any
}

type Recorder interface {
	RecordNodeSuccess(ctx context.Context, recordCtx RecordContext) ([]UsageFact, error)
}

type DefaultRecorder struct{}

func NewDefaultRecorder() *DefaultRecorder {
	return &DefaultRecorder{}
}

func (r *DefaultRecorder) RecordNodeSuccess(ctx context.Context, recordCtx RecordContext) ([]UsageFact, error) {
	_ = ctx

	if len(recordCtx.UsageFacts) > 0 {
		explicitProtocolPriority := hasProtocolPriorityUsage(recordCtx.UsageFacts)
		facts, err := ParseUsageFacts(recordCtx.UsageFacts)
		if err != nil {
			return nil, err
		}
		if explicitProtocolPriority {
			return facts, nil
		}
		if len(facts) > 0 {
			return facts, nil
		}
	}

	facts := make([]UsageFact, 0, 2)
	return facts, nil
}

func hasProtocolPriorityUsage(rawFacts []map[string]any) bool {
	for _, raw := range rawFacts {
		resourceType := ResourceType(asString(raw["resource_type"]))
		switch resourceType {
		case ResourceTypeTTS,
			ResourceTypeLLM,
			ResourceTypeVLM,
			ResourceTypeImageGeneration,
			ResourceTypeVideoGeneration:
			return true
		}
	}
	return false
}

type TaskCostTraceRecorder struct {
	taskRepo  taskSummaryStore
	traceRepo taskCostTraceStore
}

type taskSummaryStore interface {
	GetByID(ctx context.Context, id int64) (*domain.Task, error)
	Update(ctx context.Context, task *domain.Task) error
}

type taskCostTraceStore interface {
	Upsert(ctx context.Context, trace *domain.TaskCostTrace) error
	SumEstimatedCostByTaskID(ctx context.Context, taskID int64) (float64, error)
	SumEstimatedCostByRootTaskID(ctx context.Context, rootTaskID int64) (float64, error)
}

func NewTaskCostTraceRecorder(
	taskRepo taskSummaryStore,
	traceRepo taskCostTraceStore,
) *TaskCostTraceRecorder {
	return &TaskCostTraceRecorder{
		taskRepo:  taskRepo,
		traceRepo: traceRepo,
	}
}

func (r *TaskCostTraceRecorder) RecordNodeSuccess(ctx context.Context, recordCtx RecordContext) ([]UsageFact, error) {
	if r == nil || r.taskRepo == nil || r.traceRepo == nil {
		return nil, nil
	}

	base := NewDefaultRecorder()
	facts, err := base.RecordNodeSuccess(ctx, recordCtx)
	if err != nil || len(facts) == 0 {
		return facts, err
	}

	for idx, fact := range facts {
		trace := buildTaskCostTrace(recordCtx, fact, idx)
		if err := r.traceRepo.Upsert(ctx, trace); err != nil {
			return facts, err
		}
	}

	if recordCtx.TaskID > 0 && recordCtx.TaskID != recordCtx.RootTaskID {
		if err := r.refreshTaskSummary(ctx, recordCtx.TaskID); err != nil {
			return facts, err
		}
	}
	if recordCtx.RootTaskID > 0 {
		if err := r.refreshRootTaskSummary(ctx, recordCtx.RootTaskID); err != nil {
			return facts, err
		}
	}

	return facts, nil
}

func (r *TaskCostTraceRecorder) refreshTaskSummary(ctx context.Context, taskID int64) error {
	task, err := r.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return err
	}

	estimated, err := r.traceRepo.SumEstimatedCostByTaskID(ctx, taskID)
	if err != nil {
		return err
	}
	task.EstimatedCostTotal = estimated
	task.CostStatus = "estimated"
	return r.taskRepo.Update(ctx, task)
}

func (r *TaskCostTraceRecorder) refreshRootTaskSummary(ctx context.Context, rootTaskID int64) error {
	task, err := r.taskRepo.GetByID(ctx, rootTaskID)
	if err != nil {
		return err
	}

	estimated, err := r.traceRepo.SumEstimatedCostByRootTaskID(ctx, rootTaskID)
	if err != nil {
		return err
	}
	task.EstimatedCostTotal = estimated
	task.CostStatus = "estimated"
	return r.taskRepo.Update(ctx, task)
}

func buildTaskCostTrace(recordCtx RecordContext, fact UsageFact, index int) *domain.TaskCostTrace {
	payload := map[string]any{
		"node_type":       recordCtx.NodeType,
		"usage_breakdown": fact.UsageBreakdown,
		"billable":        fact.Billable,
	}

	idempotencyKey := buildIdempotencyKey(recordCtx, fact, index)
	var nodeRuntimeID *int64
	if recordCtx.NodeRuntimeID > 0 {
		nodeRuntimeID = &recordCtx.NodeRuntimeID
	}

	return &domain.TaskCostTrace{
		TaskID:            recordCtx.TaskID,
		RootTaskID:        recordCtx.RootTaskID,
		NodeRuntimeID:     nodeRuntimeID,
		WorkflowName:      recordCtx.WorkflowName,
		NodeName:          recordCtx.NodeName,
		StepName:          recordCtx.StepName,
		ResourceType:      string(fact.ResourceType),
		Provider:          fact.Provider,
		Model:             fact.Model,
		ProviderRequestID: fact.ProviderRequestID,
		UsageQuantity:     fact.UsageQuantity,
		UsageUnit:         fact.UsageUnit,
		EstimatedCost:     fact.EstimatedCost,
		Currency:          "CNY",
		Status:            "estimated",
		IdempotencyKey:    idempotencyKey,
		TracePayload:      payload,
	}
}

func buildIdempotencyKey(recordCtx RecordContext, fact UsageFact, index int) string {
	raw := map[string]any{
		"task_id":             recordCtx.TaskID,
		"root_task_id":        recordCtx.RootTaskID,
		"node_runtime_id":     recordCtx.NodeRuntimeID,
		"node_name":           recordCtx.NodeName,
		"step_name":           recordCtx.StepName,
		"resource_type":       fact.ResourceType,
		"provider":            fact.Provider,
		"provider_request_id": fact.ProviderRequestID,
		"usage_unit":          fact.UsageUnit,
		"output_hash":         recordCtx.OutputHash,
		"fact_index":          index,
	}
	b, _ := json.Marshal(raw)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
