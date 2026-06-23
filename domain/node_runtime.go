package domain

import (
	"strconv"
	"time"

	"flux/utils"
)

type NodeState string

// 状态迁移模型
const (
	NodePending             NodeState = "pending" // 调度态
	NodeReady               NodeState = "ready"   // DAG 调度完成后的验证态，在ready期间才可以构造、校验输入参数
	NodeRunning             NodeState = "running" // 是执行态
	NodeAwaiting            NodeState = "awaiting"
	NodeRetrying            NodeState = "retrying"
	NodeSuccess             NodeState = "success"
	NodeFailed              NodeState = "failed" // 失败的任务需要需要人为干预在表中设置为retrying才可以在服务重启时重新被执行，
	NodeSkipped             NodeState = "skipped"
	NodeCanceled            NodeState = "canceled"
	NodeSuccessPendingEdges NodeState = "success_pending_edges" // 任务成功待计算边
	NodeFailedPendingEdges            = "failed_pending_edges"  // 失败待关闭边
)

// AllowedTransitionsNodes 允许的状态迁移规则
var AllowedTransitionsNodes = map[NodeState][]NodeState{
	NodePending: {NodeReady, NodeSkipped},
	NodeReady:   {NodeRunning, NodeFailed, NodeSkipped},
	// NodeSkipped 允许从 Running/Retrying 进入：用于「可选节点」执行失败后被降级为 skip
	// （不拖垮整个任务），见 Engine.finalizeOptionalFailedNode。
	NodeRunning:             {NodeAwaiting, NodeSuccess, NodeFailed, NodeRetrying, NodeSkipped},
	NodeAwaiting:            {NodeSuccessPendingEdges, NodeFailedPendingEdges, NodeCanceled},
	NodeRetrying:            {NodeRunning, NodeFailed, NodeSuccess, NodeSkipped},
	NodeSuccessPendingEdges: {NodeSuccess},
	NodeFailedPendingEdges:  {NodeFailed},
}

type ReuseKind string

const (
	ReuseNone     ReuseKind = ""          // 完全重新执行
	ReuseNode     ReuseKind = "node"      // 整个节点输出直接复用
	ReuseMapItems ReuseKind = "map_items" // map 节点本次执行，但部分 item 复用
)

type NodeRuntime struct {
	ID         int64      `json:"id"`
	TaskID     int64      `json:"task_id"`
	Name       string     `json:"name"`
	State      NodeState  `json:"state"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`

	InputHash     string         `json:"input_hash"`     // 存储上次执行成功时的“指纹”
	ResolvedInput map[string]any `json:"resolved_input"` // 节点最终解析后的输入快照，执行期临时字段，不写入数据库

	// public contract，给下游/表达式/UI 使用
	Output map[string]any `json:"output"`
	// internal durable state，给恢复/fork/replay/map fan-in/fan-out 使用
	Checkpoint    map[string]any `json:"checkpoint"`
	Error         string         `json:"error"`
	LastHeartbeat *time.Time     `json:"last_heartbeat"`

	Progress float64 `json:"progress"`  // 当前节点进度 Progress = 0~1
	Weight   float64 `json:"weight"`    // 节点权重 Weight = 0~1
	Index    int     `json:"index"`     // 在节点中的位置
	BizIndex int     `json:"biz_index"` // 在节点中属于业务的位置，去除start和end系统节点得来的

	// 每个 Node 只存“自己发出的边”
	// {
	//  "A->B": true,
	//  "A->C": false
	// }
	ActivatedEdges map[string]bool `json:"activated_edges"` // 存储已经激活的边

	// ===== 新增 =====
	OutputHash       string     `json:"output_hash"`         // 输出快照hash
	ReusedFromTaskID *int64     `json:"reused_from_task_id"` // 复用自哪个任务
	ReusedFromNode   *string    `json:"reused_from_node"`    // 复用自哪个节点
	IsInjected       bool       `json:"is_injected"`         // 是否注入输出
	IsDirty          bool       `json:"is_dirty"`            // 是否脏节点
	DirtyReason      string     `json:"dirty_reason"`        // input_changed / upstream_dirty / manual_override
	CheckpointedAt   *time.Time `json:"checkpointed_at"`     // 快照时间
	ReuseKind        ReuseKind  `json:"reuse_kind"`

	ExecutionReason string     `json:"execution_reason"`
	PlanAction      string     `json:"plan_action"`
	PatchedAt       *time.Time `json:"patched_at"`
	MaterializedAt  *time.Time `json:"materialized_at"`
	LastPatchLabel  *string    `json:"last_patch_label"`
}

func (runtime *NodeRuntime) WriteMapItemResult(
	index int,
	itemHash string,
	result map[string]any,
	reused bool,
) {
	if runtime == nil {
		return
	}
	if runtime.Checkpoint == nil {
		runtime.Checkpoint = map[string]any{}
	}

	results, ok := runtime.Checkpoint["results"].(map[string]any)
	if !ok || results == nil {
		results = map[string]any{}
	}

	itemHashes, ok := runtime.Checkpoint["item_hashes"].(map[string]any)
	if !ok || itemHashes == nil {
		itemHashes = map[string]any{}
	}

	reusedItems, ok := runtime.Checkpoint["reused_items"].(map[string]any)
	if !ok || reusedItems == nil {
		reusedItems = map[string]any{}
	}

	key := strconv.Itoa(index)

	_, existed := results[key]

	results[key] = result
	itemHashes[key] = itemHash
	reusedItems[key] = reused

	runtime.Checkpoint["results"] = results
	runtime.Checkpoint["item_hashes"] = itemHashes
	runtime.Checkpoint["reused_items"] = reusedItems

	if !existed {
		done := utils.ToInt(runtime.Checkpoint["done"])
		runtime.Checkpoint["done"] = done + 1
	}
}

func (dst *NodeRuntime) MergeRuntimePreserveMeta(src *NodeRuntime) {
	if src == nil || dst == nil {
		return
	}

	if dst.ReuseKind == ReuseNone {
		dst.ReuseKind = src.ReuseKind
	}

	if dst.ReusedFromTaskID == nil {
		dst.ReusedFromTaskID = src.ReusedFromTaskID
	}

	if dst.ReusedFromNode == nil {
		dst.ReusedFromNode = src.ReusedFromNode
	}

	if !dst.IsInjected {
		dst.IsInjected = src.IsInjected
	}

	// ❗ Dirty 信息不能丢（后面测试会用到）
	if !dst.IsDirty {
		dst.IsDirty = src.IsDirty
		dst.DirtyReason = src.DirtyReason
	}
}
