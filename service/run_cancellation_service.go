package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"flux/domain"
	"flux/repository"
)

const CancelReasonSupersededByRevision = "superseded_by_revision"

var (
	ErrRunCancellationTaskNotFound = errors.New("run cancellation: task not found")
	ErrRunCancellationNotAllowed   = errors.New("run cancellation: task status cannot be canceled")
)

type RunCancellationService interface {
	CancelForSupersededRevision(ctx context.Context, taskID int64) (*RunCancellationResult, error)
}

type RunCancellationResult struct {
	TaskID                  int64
	Reason                  string
	AlreadyCanceled         bool
	CanceledTaskIDs         []int64
	CanceledNodeIDs         []int64
	CanceledAwaitBindingIDs []int64
}

type runCancellationService struct {
	taskRepo         repository.TaskRepository
	nodeRepo         repository.NodeRuntimeRepository
	awaitBindingRepo repository.AwaitBindingRepository
}

func NewRunCancellationService(
	taskRepo repository.TaskRepository,
	nodeRepo repository.NodeRuntimeRepository,
	awaitBindingRepo repository.AwaitBindingRepository,
) RunCancellationService {
	return &runCancellationService{
		taskRepo:         taskRepo,
		nodeRepo:         nodeRepo,
		awaitBindingRepo: awaitBindingRepo,
	}
}

func (s *runCancellationService) CancelForSupersededRevision(ctx context.Context, taskID int64) (*RunCancellationResult, error) {
	if s == nil || s.taskRepo == nil {
		return nil, fmt.Errorf("run cancellation: task repository is nil")
	}

	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ErrRunCancellationTaskNotFound
	}
	if !isSupersededCancellationAllowed(task.Status) {
		return nil, fmt.Errorf("%w: %s", ErrRunCancellationNotAllowed, task.Status)
	}

	result := &RunCancellationResult{
		TaskID:          taskID,
		Reason:          CancelReasonSupersededByRevision,
		AlreadyCanceled: task.Status == domain.TaskCanceled,
	}
	visited := map[int64]struct{}{}
	if err := s.cancelTaskTree(ctx, task, CancelReasonSupersededByRevision, result, visited); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *runCancellationService) cancelTaskTree(
	ctx context.Context,
	task *domain.Task,
	reason string,
	result *RunCancellationResult,
	visited map[int64]struct{},
) error {
	if task == nil {
		return nil
	}
	if _, ok := visited[task.ID]; ok {
		return nil
	}
	visited[task.ID] = struct{}{}

	if s.taskRepo != nil {
		children, err := s.taskRepo.ListChildrenByParentID(ctx, task.ID)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child == nil {
				continue
			}
			if err := s.cancelTaskTree(ctx, child, reason, result, visited); err != nil {
				return err
			}
		}
	}

	switch task.Status {
	case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended:
		task.Status = domain.TaskCanceled
		task.ErrorMessage = reason
		task.OutputJSON = nil
		task.Progress = 0
		if err := s.taskRepo.Update(ctx, task); err != nil {
			return err
		}
		result.CanceledTaskIDs = append(result.CanceledTaskIDs, task.ID)
	case domain.TaskCanceled:
	default:
		return nil
	}

	if err := s.cancelTaskNodes(ctx, task.ID, reason, result); err != nil {
		return err
	}
	if err := s.cancelAwaitBindings(ctx, task.ID, reason, result); err != nil {
		return err
	}
	return nil
}

func (s *runCancellationService) cancelTaskNodes(ctx context.Context, taskID int64, reason string, result *RunCancellationResult) error {
	if s.nodeRepo == nil {
		return nil
	}
	nodes, err := s.nodeRepo.FindByTaskID(ctx, taskID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, node := range nodes {
		if node == nil || !isRunCancellationNodeCancelable(node.State) {
			continue
		}
		node.State = domain.NodeCanceled
		node.Error = reason
		node.FinishedAt = &now
		node.LastHeartbeat = nil
		node.Progress = 0
		if err := s.nodeRepo.Update(ctx, node); err != nil {
			return err
		}
		result.CanceledNodeIDs = append(result.CanceledNodeIDs, node.ID)
	}
	return nil
}

func (s *runCancellationService) cancelAwaitBindings(ctx context.Context, taskID int64, reason string, result *RunCancellationResult) error {
	if s.awaitBindingRepo == nil {
		return nil
	}
	bindings, err := s.awaitBindingRepo.ListByTaskID(ctx, taskID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, binding := range bindings {
		if binding == nil || isAwaitBindingTerminal(binding.Status) {
			continue
		}
		binding.Status = domain.AwaitBindingCanceled
		binding.ErrorMessage = reason
		binding.CanceledAt = &now
		binding.NextPollAt = nil
		binding.LastEventID = nil
		binding.LastEventSource = nil
		binding.LastEventPayload = nil
		if err := s.awaitBindingRepo.Update(ctx, binding); err != nil {
			return err
		}
		result.CanceledAwaitBindingIDs = append(result.CanceledAwaitBindingIDs, binding.ID)
	}
	return nil
}

func isSupersededCancellationAllowed(status domain.TaskStatus) bool {
	switch status {
	case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended, domain.TaskCanceled:
		return true
	default:
		return false
	}
}

func isRunCancellationNodeCancelable(state domain.NodeState) bool {
	switch state {
	case domain.NodePending,
		domain.NodeReady,
		domain.NodeRunning,
		domain.NodeAwaiting,
		domain.NodeRetrying,
		domain.NodeSuccessPendingEdges,
		domain.NodeFailedPendingEdges:
		return true
	default:
		return false
	}
}
