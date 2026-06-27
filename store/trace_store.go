package store

import (
	"context"

	"flux/runtime"
)

// TraceStore 是 trace event log 的持久化端口。
//
// Trace 是确定性/回放/分叉的真源（event-sourcing 的 log）。它记录完整的执行真相：
//   - ClassExecution：节点生命周期 + tool I/O（确定性流）
//   - ClassControl：planner 决策（非确定性流，一旦记录即固化为确定性事实）
//
// 两条流共享同一条单调 Seq，保证跨流因果可回放。
type TraceStore interface {
	// AppendTrace 追加一批 trace event。调用方可批量写入以降低 I/O。
	AppendTrace(ctx context.Context, taskID string, events []runtime.TraceEvent) error

	// ReplayTrace 从指定 Seq 起回放 trace event。用于恢复执行状态。
	// sinceSeq=0 表示从头回放。
	ReplayTrace(ctx context.Context, taskID string, sinceSeq int64) ([]runtime.TraceEvent, error)
}
