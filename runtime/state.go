package runtime

import "context"

// NodeState 运行时节点状态（自包含，不依赖 domain）。
// 适配器层负责与 domain.NodeRuntime 的状态互转。
type NodeState uint8

const (
	NodePending NodeState = iota
	NodeRunning
	NodeAwaiting // 已挂起等外部事件（对应 domain.NodeAwaiting）
	NodeSuccess
	NodeFailed
	NodeSkipped
)

func (s NodeState) Terminal() bool {
	return s == NodeSuccess || s == NodeFailed || s == NodeSkipped
}

// ExecState 是底座读写的"活"执行上下文。
// 现有的 workflow/nodes.Context（持 Output/Runtime + expr 环境）将实现这个接口，
// 从而老路径无需改数据结构即可跑在新底座上。
type ExecState interface {
	Input() map[string]any // 任务级输入
	Output(node string) map[string]any
	SetOutput(node string, out map[string]any)

	State(node string) NodeState
	Transition(node string, to NodeState)

	// Nodes 返回当前已知节点名（动态前沿下会随 planner 增长）
	Nodes() []string
}

// ── 端口（ports）：底座定义接口，基础设施做适配器 ──────────────────────────

// Invoker 执行一次工具调用。tool.Registry 适配它（Tool-First）。
type Invoker interface {
	Invoke(ctx context.Context, toolName string, input map[string]any, emit Emitter) (map[string]any, error)
}

// AwaitController 异步原语：提交外部任务 + 建 AwaitBinding，等待外部唤醒。
// 现有 engine 的 executeAwaitNode / CompleteAwaitNode + AwaitBindingRepository 适配它。
type AwaitController interface {
	// Begin 为 async 节点登记一个等待（提交外部请求、写 AwaitBinding）。
	// 返回后节点进入 NodeAwaiting，由外部事件/poll 通过 Scheduler.Resume 唤醒。
	Begin(ctx context.Context, node *PlanNode, input map[string]any) (bindingID int64, err error)
}

// Store 持久化端口：节点状态/输出落库（现有 NodeRuntime 持久化适配它）。
type Store interface {
	PersistNode(ctx context.Context, node string, state NodeState, out map[string]any) error
}

// Emitter 事件端口（现有 nodes.Context.EmitToolEvent 适配它）。
type Emitter interface {
	Emit(event Event)
}

type Event struct {
	Node     string
	Type     string
	Message  string
	Progress float64
	Data     map[string]any
}
