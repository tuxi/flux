package domain

type PatchTarget string

const (
	PatchTargetNodeOutput     PatchTarget = "node_output"
	PatchTargetNodeCheckpoint PatchTarget = "node_checkpoint"
)

type PatchOp string

const (
	PatchOpSet    PatchOp = "set"
	PatchOpDelete PatchOp = "delete"
	PatchOpMerge  PatchOp = "merge"
)

type RuntimePatch struct {
	Target PatchTarget `json:"target"` // node_output / node_checkpoint
	Node   string      `json:"node"`   // 节点名
	Path   string      `json:"path"`   // 点路径，如 intent.scene / results.0.caption
	Op     PatchOp     `json:"op"`     // set / delete / merge
	Value  any         `json:"value"`  // set/merge 时使用
	Label  string      `json:"label"`  // 给 UI 展示，可选
}

type ResumeSpec struct {
	ResumeFrom string         `json:"resume_from"` // 从哪个节点开始重跑
	Patches    []RuntimePatch `json:"patches"`     // 本次 patch 集合
}
