package nodes

import (
	"flux/definition"
	"flux/tool"
	"sync"
)

// NodeExecContext 每一个节点的执行上下文，包含一个只读的TaskContext全局上下文
type NodeExecContext struct {
	TaskContext *Context // 全局只读
	// Input (当前节点的“入参表”)：这是执行前，从全局 Output 搬运过来的、专门给当前这个 Step 用的数据。比如：{"prompt": "画一只猫"}
	Input map[string]any
	// Output 当前节点的“成绩单”)：这是 Step.Run 执行完后，产出的结果。比如：{"image_url": "http://..."}。
	Output map[string]any
	mu     sync.Mutex

	NodeDef  *definition.NodeDefinition
	Executor WorkflowExecutor
}

func (n *NodeExecContext) SetOutput(key string, val any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Output == nil {
		n.Output = make(map[string]any)
	}
	n.Output[key] = val
}

// EmitToolEvent 发送工具事件
func (n *NodeExecContext) EmitToolEvent(event tool.ToolEvent) {
	n.TaskContext.EmitToolEvent(n.NodeDef.Name, event)
}

// EmitNodeEvent 发送节点事件
func (n *NodeExecContext) EmitNodeEvent(event NodeEvent) {
	n.TaskContext.EmitNodeEvent(n.NodeDef.Name, event)
}
