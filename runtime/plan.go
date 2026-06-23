// Package runtime 是 Flux 的执行底座：只认"机制"（依赖/并行/异步/状态/复用/分叉），
// 不认识 workflow DSL。它不 import definition、不 import expr。
//
// 对应关系：本文件取代 engine/run_plan.go 的 RunPlan/NodePlan，
// 把 NodePlan.NodeType (definition.NodeType) 退化为 Async / Join 两个运行时原语。
package runtime

import (
	"context"
	"time"
)

// Plan 是一张"已解析的可执行 DAG"。谁产出的不重要：
//   - workflow 编译器把人写的 WorkflowDefinition 编译成 Plan
//   - planner 把 Goal 增量生成成 Plan
//   - SDK/代码直接拼 Plan
type Plan struct {
	Nodes map[string]*PlanNode
}

// PlanNode 一个可执行单元 = 一次工具调用（Tool-First）。
// 注意这里没有 input_mapping 表达式、没有 edge condition —— 那些是编排语义，
// 已在前端编译期解析掉，只留下纯依赖边 + 一个 InputResolver 回调。
type PlanNode struct {
	Name      string
	ToolName  string   // 节点要调用的工具（registry 里查）
	DependsOn []string // 纯拓扑依赖边（不含条件）

	// Async 取代 definition 的 await/async node type。
	// true → 走"挂起 + AwaitBinding + 外部唤醒"路径（对应 executor.go:133）。
	Async bool

	// Join 决定依赖满足语义：map/loop 的并行汇聚下沉成的原语。
	Join JoinKind

	// Resolve 在执行到本节点时、用实时上游产出算出本节点入参。
	// workflow 编译器 → expr 求值实现；planner → 直接给值。expr-lang 因此被关在前端。
	Resolve InputResolver

	Retry RetryPolicy
}

type JoinKind uint8

const (
	JoinAll JoinKind = iota // 所有依赖 success 才就绪（默认）
	JoinAny                 // 任一依赖 success 即就绪（条件分支 / merge）
)

// InputResolver：给定当前执行状态，产出本节点入参。失败即节点失败。
type InputResolver func(ctx context.Context, state ExecState) (map[string]any, error)

type RetryPolicy struct {
	MaxRetries int
	Interval   time.Duration
}
