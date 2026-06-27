package store

import "context"

// AwaitStore 管理异步节点的挂起/恢复凭证。
//
// 它与 Conversation 生命周期完全解耦：一个 AwaitBinding 可能在 Conversation Turn 结束、
// Session 被删除后仍然存活，直到外部 Notify 到达。
//
// 实现要求：
//   - CreateBinding 和 ResolveBinding 必须是线程安全的（可能被 HTTP handler 和 poll worker 并发调用）
//   - ResolveBinding 必须是幂等的（重复 resolve 同一个 binding 返回 claimed=false，不报错）
//   - FindByProviderTaskID 用于通过外部 Provider 返回的 task ID 反查 binding
type AwaitStore interface {
	// CreateBinding 登记一个异步等待。
	// 调用时机：Scheduler 执行 async 节点，Begin 返回后。
	CreateBinding(ctx context.Context, binding AwaitBinding) error

	// ResolveBinding 原子完成一个等待。
	// 幂等保证：如果 binding 已经被其他线程 resolve，返回 (false, nil)。
	// 调用时机：外部 Notify / poll 到达。
	ResolveBinding(ctx context.Context, bindingID string) (claimed bool, err error)

	// FindByProviderTaskID 通过外部 Provider 返回的 task ID 查找 binding。
	// 调用时机：Notify handler 收到 providerTaskID，需要找到对应的 binding。
	// 无匹配时返回 nil, nil。
	FindByProviderTaskID(ctx context.Context, providerTaskID string) (*AwaitBinding, error)

	// ListPending 列出所有 awaiting 状态的 binding。
	// 用于 poll worker 定期扫描超时或需要轮询的 binding。
	ListPending(ctx context.Context) ([]AwaitBinding, error)

	// ListByTask 列出某个 Task 下的所有 binding。
	ListByTask(ctx context.Context, taskID string) ([]AwaitBinding, error)
}
