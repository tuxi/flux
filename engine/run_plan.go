package engine

import (
	"flux/definition"
	"flux/domain"
)

// engine/run_plan.go

type RunPlanMode string

const (
	RunPlanModeInitial RunPlanMode = "initial"
	RunPlanModeFork    RunPlanMode = "fork"
)

type PlanAction string

const (
	PlanActionReuse   PlanAction = "reuse"   // 直接复用父快照并注入 success
	PlanActionPatch   PlanAction = "patch"   // 复用父快照后打 patch，patched success
	PlanActionExecute PlanAction = "execute" // 进入 pending，等待 runDAG 真执行
)

type ExecutionReason string

const (
	ExecutionReasonNone             ExecutionReason = ""
	ExecutionReasonReuseNode        ExecutionReason = "reuse_node"
	ExecutionReasonPatchedNode      ExecutionReason = "patched_node"
	ExecutionReasonResumeBoundary   ExecutionReason = "resume_boundary"
	ExecutionReasonUpstreamDirty    ExecutionReason = "upstream_dirty"
	ExecutionReasonInputChanged     ExecutionReason = "input_changed"
	ExecutionReasonMissingParent    ExecutionReason = "missing_parent_snapshot"
	ExecutionReasonParentNotReady   ExecutionReason = "parent_not_success"
	ExecutionReasonInputResolveFail ExecutionReason = "input_resolve_failed"
)

type NodePlan struct {
	Name         string
	Label        string
	NodeType     definition.NodeType
	Action       PlanAction
	Reason       ExecutionReason
	ReuseKind    domain.ReuseKind
	MapItemReuse map[int]bool
	Patches      []domain.RuntimePatch

	// 调试 / 观测信息
	ParentTaskID *int64
	ParentNode   *string
}

type RunPlan struct {
	TaskID       int64
	Mode         RunPlanMode
	ResumeFrom   string
	ParentTaskID *int64
	Nodes        map[string]*NodePlan
	TopoOrder    []string
}
