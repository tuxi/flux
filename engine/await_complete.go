package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"flux/domain"
	"gorm.io/gorm"
)

func (e *Engine) CompleteAwaitNode(
	bindingID int64,
	eventPayload map[string]any,
	eventErr string,
	source string,
) RunResult {
	if e.awaitBindingRepo == nil {
		return RunResult{Status: RunFailed, Err: fmt.Errorf("await binding repository is nil")}
	}

	binding, err := e.loadAwaitBinding(bindingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RunResult{Status: RunNoop}
		}
		return RunResult{Status: RunFailed, Err: err}
	}
	if binding == nil {
		return RunResult{Status: RunNoop}
	}

	claimed, err := e.awaitBindingRepo.ClaimCompleting(
		context.Background(),
		binding.ID,
		[]domain.AwaitBindingStatus{domain.AwaitBindingWaiting},
	)
	if err != nil {
		return RunResult{Status: RunFailed, Err: err}
	}
	if !claimed {
		return RunResult{Status: RunNoop}
	}

	now := time.Now()
	publicPayload, usageFacts := splitAwaitEventPayload(eventPayload)
	sourceCopy := source
	binding.LastEventSource = &sourceCopy
	binding.LastEventPayload = deepCloneMap(publicPayload)
	binding.Status = domain.AwaitBindingCompleting
	_ = e.awaitBindingRepo.Update(context.Background(), binding)

	ok, err := e.nodeRepo.AttemptCompletePendingEdges(
		context.Background(),
		binding.TaskID,
		binding.NodeName,
		publicPayload,
		eventErr,
	)
	if err != nil {
		_, _ = e.awaitBindingRepo.TransitionStatus(context.Background(), binding.ID, domain.AwaitBindingCompleting, domain.AwaitBindingFailed)
		binding.Status = domain.AwaitBindingFailed
		binding.ErrorMessage = err.Error()
		binding.FailedAt = &now
		_ = e.awaitBindingRepo.Update(context.Background(), binding)
		return RunResult{Status: RunFailed, Err: err}
	}
	if !ok {
		return RunResult{Status: RunNoop}
	}

	resumeMeta := publicPayload
	if len(usageFacts) > 0 {
		if resumeMeta == nil {
			resumeMeta = map[string]any{}
		}
		resumeMeta[AwaitUsageFactsMetaKey] = usageFacts
	}
	result := e.ResumeTask(binding.TaskID, binding.NodeName, resumeMeta)

	if eventErr != "" || result.Status == RunFailed {
		_, _ = e.awaitBindingRepo.TransitionStatus(context.Background(), binding.ID, domain.AwaitBindingCompleting, domain.AwaitBindingFailed)
		binding.Status = domain.AwaitBindingFailed
		binding.ErrorMessage = eventErr
		if binding.ErrorMessage == "" && result.Err != nil {
			binding.ErrorMessage = result.Err.Error()
		}
		binding.FailedAt = &now
	} else {
		_, _ = e.awaitBindingRepo.TransitionStatus(context.Background(), binding.ID, domain.AwaitBindingCompleting, domain.AwaitBindingCompleted)
		binding.Status = domain.AwaitBindingCompleted
		binding.ResultPayload = deepCloneMap(publicPayload)
		binding.CompletedAt = &now
	}
	_ = e.awaitBindingRepo.Update(context.Background(), binding)

	return result
}

func splitAwaitEventPayload(eventPayload map[string]any) (map[string]any, []map[string]any) {
	if len(eventPayload) == 0 {
		return map[string]any{}, nil
	}

	publicPayload := make(map[string]any, len(eventPayload))
	var usageFacts []map[string]any
	for key, value := range eventPayload {
		if key == AwaitUsageFactsMetaKey {
			usageFacts = toUsageFacts(value)
			continue
		}
		publicPayload[key] = value
	}
	return publicPayload, usageFacts
}

func toUsageFacts(value any) []map[string]any {
	rawSlice, ok := value.([]map[string]any)
	if ok {
		return rawSlice
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if ok {
			out = append(out, m)
		}
	}
	return out
}

func (e *Engine) loadAwaitBinding(bindingID int64) (*domain.AwaitBinding, error) {
	ctx := context.Background()
	if repo, ok := e.awaitBindingRepo.(interface {
		GetByID(ctx context.Context, id int64) (*domain.AwaitBinding, error)
	}); ok {
		return repo.GetByID(ctx, bindingID)
	}
	return nil, fmt.Errorf("await binding repository does not support GetByID")
}
