package nodes

// NodeEvent 节点事件
type NodeEvent struct {
	Type     string
	Message  string
	Progress float64 // 本节点的执行进度
	Data     map[string]any
}

type NodeEmitter interface {
	EmitNodeEvent(event NodeEvent)
}
