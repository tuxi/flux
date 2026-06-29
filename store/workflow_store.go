package store

import (
	"context"

	"github.com/tuxi/flux/runtime"
)

// WorkflowStore 是 Workflow Runtime 的核心持久化端口。
//
// 它管理 WorkflowRun → Task → Node 的层级关系，以及 Plan 的持久化（crash recovery）。
// Runtime（Engine / Scheduler）只依赖此接口；SQLite / PostgreSQL / Memory 实现由 Adapter 包提供。
type WorkflowStore interface {
	// ── WorkflowRun ──

	// CreateRun 创建一个新的 Workflow 执行记录。
	CreateRun(ctx context.Context, meta RunMeta) (*WorkflowRun, error)

	// LoadRun 按 ID 加载 Workflow 执行记录。
	LoadRun(ctx context.Context, runID string) (*WorkflowRun, error)

	// UpdateRunStatus 更新 Workflow 运行状态。
	UpdateRunStatus(ctx context.Context, runID string, status string) error

	// ── Task ──

	// CreateTask 创建一个新 Task（可能是一次 tool 调用批次）。
	CreateTask(ctx context.Context, runID string, meta TaskMeta) (*Task, error)

	// LoadTask 按 ID 加载 Task。
	LoadTask(ctx context.Context, taskID string) (*Task, error)

	// ListTasks 列出某个 Run 下的所有 Task。
	ListTasks(ctx context.Context, runID string) ([]Task, error)

	// ── Node ──

	// PersistNode 持久化一个节点的状态和输出。幂等：重复写入同一 node 为 update。
	PersistNode(ctx context.Context, taskID string, nodeName string, state runtime.NodeState, output map[string]any) error

	// LoadNodeStates 加载某个 Task 下所有节点的状态。
	LoadNodeStates(ctx context.Context, taskID string) ([]NodeRecord, error)

	// ── Plan 持久化（crash recovery）──

	// SavePlan 保存 Plan 快照。用于 crash recovery 时恢复 Scheduler 状态。
	// nil plan 表示清除（任务已完成/失败）。
	SavePlan(ctx context.Context, taskID string, plan *runtime.Plan) error

	// LoadPlan 加载上次保存的 Plan 快照。无已保存 plan 时返回 nil。
	LoadPlan(ctx context.Context, taskID string) (*runtime.Plan, error)
}
