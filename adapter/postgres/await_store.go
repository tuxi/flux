package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"flux/domain"
	"flux/repository"
	"flux/store"
)

// AwaitStore 实现 store.AwaitStore，内部委托给现有的 GORM AwaitBindingRepository。
type AwaitStore struct {
	repo repository.AwaitBindingRepository
}

var _ store.AwaitStore = (*AwaitStore)(nil)

func NewAwaitStore(repo repository.AwaitBindingRepository) *AwaitStore {
	return &AwaitStore{repo: repo}
}

func (s *AwaitStore) CreateBinding(ctx context.Context, binding store.AwaitBinding) error {
	taskID, _ := strconv.ParseInt(binding.TaskID, 10, 64)

	b := &domain.AwaitBinding{
		TaskID:         taskID,
		NodeName:       binding.NodeName,
		ProviderTaskID: &binding.ProviderTaskID,
		AwaitType:      domain.AwaitTypeExternalTask,
		Status:         domain.AwaitBindingWaiting,
		Correlation:    binding.Input,
	}
	return s.repo.Create(ctx, b)
}

func (s *AwaitStore) ResolveBinding(ctx context.Context, bindingID string) (bool, error) {
	id, err := strconv.ParseInt(bindingID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid binding id: %w", err)
	}
	return s.repo.ClaimCompleting(ctx, id, []domain.AwaitBindingStatus{
		domain.AwaitBindingWaiting,
	})
}

func (s *AwaitStore) FindByProviderTaskID(ctx context.Context, providerTaskID string) (*store.AwaitBinding, error) {
	b, err := s.repo.FindWaitingByProviderTaskID(ctx, "", providerTaskID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	return domainToStoreBinding(b), nil
}

func (s *AwaitStore) ListPending(ctx context.Context) ([]store.AwaitBinding, error) {
	// 使用遥远的未来时间让 FindPollDue 返回所有 awaiting binding
	bs, err := s.repo.FindPollDue(ctx, time.Now().Add(365*24*time.Hour), 1000)
	if err != nil {
		return nil, err
	}
	out := make([]store.AwaitBinding, 0, len(bs))
	for _, b := range bs {
		out = append(out, *domainToStoreBinding(b))
	}
	return out, nil
}

func (s *AwaitStore) ListByTask(ctx context.Context, taskID string) ([]store.AwaitBinding, error) {
	id, _ := strconv.ParseInt(taskID, 10, 64)
	bs, err := s.repo.ListByTaskID(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]store.AwaitBinding, 0, len(bs))
	for _, b := range bs {
		out = append(out, *domainToStoreBinding(b))
	}
	return out, nil
}

// ── 类型转换 ──

func domainToStoreBinding(b *domain.AwaitBinding) *store.AwaitBinding {
	sb := &store.AwaitBinding{
		BindingID:      strconv.FormatInt(b.ID, 10),
		TaskID:         strconv.FormatInt(b.TaskID, 10),
		NodeName:       b.NodeName,
		Status:         domainStatusToStore(b.Status),
		Input:          b.Correlation,
		CreatedAt:      b.CreatedAt,
	}
	if b.ProviderTaskID != nil {
		sb.ProviderTaskID = *b.ProviderTaskID
	}
	if b.CompletedAt != nil {
		sb.ResolvedAt = *b.CompletedAt
	}
	return sb
}

func domainStatusToStore(s domain.AwaitBindingStatus) string {
	switch s {
	case domain.AwaitBindingWaiting, domain.AwaitBindingPending:
		return store.AwaitStatusAwaiting
	case domain.AwaitBindingCompleted:
		return store.AwaitStatusCompleted
	case domain.AwaitBindingFailed, domain.AwaitBindingTimedOut, domain.AwaitBindingCanceled:
		return store.AwaitStatusFailed
	default:
		return store.AwaitStatusAwaiting
	}
}
