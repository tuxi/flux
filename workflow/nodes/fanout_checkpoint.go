package nodes

type FanoutKind string

const (
	FanoutKindNone        FanoutKind = ""
	FanoutKindMap         FanoutKind = "map"
	FanoutKindLoop        FanoutKind = "loop"
	FanoutKindSubWorkflow FanoutKind = "subworkflow"
)

const (
	cpFanoutKind = "fanout_kind"
)

func CPFanoutKind() string {
	return cpFanoutKind
}
