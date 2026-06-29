package flux

import (
	"context"

	"github.com/tuxi/flux/store"
)

// ── Config ────────────────────────────────────────────────────────────────────

// Config 是 Engine 的配置。Backend 为必填（向后兼容），Store 接口为可选（v3 新增）。
type Config struct {
	Backend Backend // 宿主提供的持久化 + 异步基础设施（向后兼容）

	// ── v3 Store 接口（可选）──
	// 当 Store 接口非 nil 时，Engine 优先使用 Store 接口（而非 Backend）进行持久化。
	// 当 Store 接口为 nil 时，Engine 回退到 Backend（向后兼容）。
	WorkflowStore store.WorkflowStore // nil → 回退到 Backend
	AwaitStore    store.AwaitStore    // nil → 回退到 Backend
	TraceStore    store.TraceStore    // nil → 不记录 trace
}

// Backend 是宿主必须实现的持久化契约。
// DreamAI 用自己现有的 DB 实现这个接口（NodeRuntime 表 + AwaitBinding 表）。
type Backend interface {
	// PersistNode 持久化单个节点的状态和输出。
	PersistNode(ctx context.Context, taskID string, node string, state NodeState, output map[string]any) error

	// CreateAwait 为异步节点创建一个外部等待凭证。
	// 返回 bindingID：Flux 用它关联后续的 Notify。
	CreateAwait(ctx context.Context, taskID string, node string, providerTaskID string, input map[string]any) (bindingID string, err error)

	// CompleteAwait 原子地将 binding 标记为完成（waiting→completing）。
	// 返回 (claimed, error)。claimed=false 表示已被其他线程完成（幂等安全）。
	CompleteAwait(ctx context.Context, bindingID string) (claimed bool, err error)

	// Lock 获取分布式锁。用于 Resume 并发控制。
	// 返回 unlock 函数。获取失败时阻塞等待。
	Lock(ctx context.Context, key string) (unlock func(), err error)

	// LoadState 加载任务的所有节点状态（crash 恢复用）。
	LoadState(ctx context.Context, taskID string) (*TaskState, error)
}

// TaskState 是任务的完整可恢复状态。
type TaskState struct {
	Input map[string]any            `json:"input"`
	Nodes map[string]NodeSnapshot   `json:"nodes"`
}

// NodeSnapshot 是单个节点的持久化快照。
type NodeSnapshot struct {
	State  NodeState      `json:"state"`
	Output map[string]any `json:"output,omitempty"`
}

// ── 公开类型 ──────────────────────────────────────────────────────────────────

// NodeState 是节点的生命周期状态。
type NodeState int

const (
	NodePending  NodeState = iota // 尚未就绪
	NodeRunning                    // 正在执行
	NodeAwaiting                   // 等待外部事件
	NodeSuccess                    // 执行成功
	NodeFailed                     // 执行失败
	NodeSkipped                    // 被跳过
)

// RunRequest 是一次执行请求。
type RunRequest struct {
	Asset  string         // 已注册的 Asset 名
	Input  map[string]any // 任务输入
	TaskID string         // 可选：宿主已有的 task ID（如 DreamAI 的 Task.ID）。为空则自动生成
}

// RunResult 是一次执行的结果。
type RunResult struct {
	Status RunStatus      // Completed / Suspended / Failed
	Output map[string]any // 完成时的最终产出
	TaskID string         // 内部 task ID（供 Notify 定位）
	Err    error          // 失败时的错误
}

// RunStatus 是执行结果状态。
type RunStatus int

const (
	StatusCompleted RunStatus = iota
	StatusSuspended          // 有异步节点等待外部事件
	StatusFailed
)

// Event 是外部世界发生的事件（webhook、poll、消息队列）。
type Event struct {
	Provider       string         // 外部服务标识（如 "tts"、"aliyun"）
	ProviderTaskID string         // 外部 Provider 的任务 ID
	Output         map[string]any // 外部任务完成的产出
	Error          string         // 外部任务失败的信息（为空表示成功）
}
