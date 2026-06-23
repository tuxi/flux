package repository

import (
	"context"
	"flux/domain"
	"time"
)

// NodeRuntimeRepository 节点状态存储
type NodeRuntimeRepository interface {
	Create(ctx context.Context, n *domain.NodeRuntime) error
	Update(ctx context.Context, n *domain.NodeRuntime) error
	// FindByTaskID 根据任务ID 查找所有节点
	FindByTaskID(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error)
	// FindByTaskIDAndNode 根据任务ID 和 节点名称 查找节点
	FindByTaskIDAndNode(ctx context.Context, taskID int64, node string) (*domain.NodeRuntime, error)
	// MarkRunningAsRetrying 标记正在运行中的节点为重试状态
	MarkRunningAsRetrying(ctx context.Context, taskID int64) error
	MarkAsRetrying(ctx context.Context, taskID int64, name string) error
	MarkFailed(ctx context.Context, taskID int64, name string, errMessage string) error

	FindExpiredRunningNodes(ctx context.Context, expire time.Time) ([]*domain.NodeRuntime, error)
	AttemptCompletePendingEdges(ctx context.Context, taskID int64, nodeName string, output map[string]any, errMsg string) (bool, error)

	CloneCheckpoint(ctx context.Context, fromTaskID, toTaskID int64) error
}
