# Await Retry Duplicate Binding 排障记录

## 背景

- 任务 ID：`2048025021015670784`
- 现象：
  - 任务在重试时失败
  - 错误信息：`volcengine_wait node status is faile, ERROR: duplicate key value violates unique constraint "await_bindings_pkey" (SQLSTATE 23505)`

本记录用于说明这次问题的完整链路，并澄清 `AwaitPollWorker` 在其中的角色，避免后续团队把“第一次 await 失败”和“重试时 duplicate key”混为一个问题。

## 直接结论

这次重试失败的直接原因不是 `AwaitPollWorker` 重复创建了 binding，而是 await 节点在 retry 时复用了旧 binding 的主键，然后又继续执行了 `Create`。

更准确地说：

1. `AwaitPollWorker` 参与了第一次 await 失败，它把旧 binding 从 `waiting` 推进到了 `failed`。
2. 后续任务进入 retry，`task_retry_service` 只重置了 `task` 和 `node_runtime`，没有处理旧的 `await_binding`。
3. `volcengine_wait` 再次执行时查到了这条旧 binding，把旧 `ID` 复用回新 binding。
4. 因为旧 binding 已经是终态 `failed`，代码没有走“继续等待”的分支，而是继续执行 `Create`。
5. 最终以同一个主键再次插入 `await_bindings`，触发 `await_bindings_pkey` 冲突。

## 证据

### 1. 任务表中的最终报错

数据库查询结果显示：

- `tasks.id = 2048025021015670784`
- `tasks.status = failed`
- `tasks.error_message = volcengine_wait node status is faile, ERROR: duplicate key value violates unique constraint "await_bindings_pkey" (SQLSTATE 23505)`

对应的任务收尾报错来自 [engine.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/engine/engine.go:579)：

```go
return fmt.Errorf("%s node status is faile, %s", firstFailedNode.Name, firstFailedNode.Error)
```

这说明 `faile` 只是收尾报错里的拼写问题，不是独立故障点。

### 2. 节点运行时中的失败点

`task_nodes` 中同一个任务和节点的记录显示：

- `node_name = volcengine_wait`
- `state = failed`
- `error = ERROR: duplicate key value violates unique constraint "await_bindings_pkey" (SQLSTATE 23505)`
- 失败时间：`2026-04-25 21:25:34 +08:00`

这说明任务不是在 provider 回调或 poll 完成阶段失败，而是在 await 节点重新执行时失败。

### 3. 旧 await binding 已经存在且已失败

`await_bindings` 中已经存在旧记录：

- `id = 12`
- `task_id = 2048025021015670784`
- `node_name = volcengine_wait`
- `status = failed`
- `failed_at = 2026-04-25 21:06:24 +08:00`
- `provider = volcengine`
- `provider_task_id = seedream-i2i-6ec14b9f465f4db4`
- `api_task_id = seedream-i2i-6ec14b9f465f4db4`

它的失败信息是：

`seedream 图片任务失败，code=seedream_generate_failed message=seedream 图片生成失败，status=404 code=InvalidEndpointOrModel.NotFound message=The model or endpoint doubao-seedream-4.0 does not exist or you do not have access to it.`

这说明第一次 await 失败已经落库，后续 retry 面对的是一条终态旧 binding。

## 与 AwaitPollWorker 的关系

`AwaitPollWorker` 的职责在 [await_poll_worker.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/worker/await_poll_worker.go) 中比较明确：

- 只扫描 `status=waiting` 的 binding
- 处理 timeout 或 fallback poll
- 对已有 binding 执行 `Update`
- 或调用 `CompleteAwaitNode(binding.ID, ...)`

它不会新建 binding，因为整个文件里没有 `awaitBindingRepo.Create(...)`。

关键链路：

- [await_poll_worker.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/worker/await_poll_worker.go:68) `RunOnce()` 只处理 `FindTimeoutDue` / `FindPollDue`
- [await_poll_worker.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/worker/await_poll_worker.go:124) `processPollBinding()` 只会 `Update` 现有 binding，或者调用 `CompleteAwaitNode`
- [await_complete.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/engine/await_complete.go:16) `CompleteAwaitNode()` 也是按 `binding.ID` 更新状态，不会创建新 binding

因此，`AwaitPollWorker` 在这次事故中的角色是：

- 它很可能负责把第一次 `volcengine_wait` 对应的 binding 从 `waiting` 推到 `failed`
- 但它不是 `duplicate key` 的直接制造者

## 真正出问题的代码路径

问题集中在 await 节点执行逻辑 [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/engine/executor.go:145)：

```go
existing, err := e.awaitBindingRepo.GetByTaskAndNode(runCtx.Ctx, runCtx.Task.ID, node.Name)
if err == nil && existing != nil {
	binding.ID = existing.ID
	if existing.Status == domain.AwaitBindingWaiting || existing.Status == domain.AwaitBindingPending {
		e.transitionLocked(runCtx, runtime, domain.NodeAwaiting, nil, nil)
		return &domain.WorkflowSuspendedError{Reason: domain.SuspendAsyncNode}
	}
}

if err := e.awaitBindingRepo.Create(runCtx.Ctx, binding); err != nil {
	return err
}
```

这里有一个明显的状态分支漏洞：

1. 查到旧 binding 后，无论旧 binding 是不是终态，都会先执行 `binding.ID = existing.ID`
2. 只有旧状态是 `waiting/pending` 时才直接复用并挂起
3. 如果旧状态是 `failed/completed/canceled`，代码不会 update 旧记录，也不会清空 `binding.ID`
4. 最后继续 `Create`

这就导致：

- 旧 binding 主键被复用
- 但实际执行的是 insert
- 从而触发 `await_bindings_pkey` 冲突

## 为什么 retry 会放大这个问题

重试准备逻辑在 [task_retry_service.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/service/task_retry_service.go:53)。

当前实现会：

- 修复 loop/map checkpoint
- 重置失败子树上的 `node_runtime`
- 把任务重新置为 `pending`

但不会：

- 取消旧 `await_binding`
- 删除旧 `await_binding`
- 或把旧 `await_binding` 重置回可复用状态

这意味着 retry 之后：

- `node_runtime` 看起来像“全新待执行”
- 但 `await_bindings` 里仍然残留上一轮的终态记录

这正好触发了 `executeAwaitNode()` 的主键复用漏洞。

## 结论拆分

为了避免沟通时混淆，这次故障应拆成两个层次：

### 业务失败

第一次执行时，provider/poll 链路返回了终态失败，旧 binding 被正确地推进到 `failed`。

这部分与：

- provider 模型配置
- fallback poll 工具执行结果
- `AwaitPollWorker`

有关。

### 引擎重试失败

任务 retry 时，await 运行时没有正确处理旧 binding，导致重复插入同一主键。

这部分与：

- `TaskRetryService`
- `Engine.executeAwaitNode`
- `await_bindings` 的 retry 语义

有关。

本次用户看到的最终失败，是第二层，也就是“引擎重试失败”。

## 修复建议

建议分两层处理。

### P0：修正 await 节点的创建逻辑

在 [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/engine/executor.go:154) 附近收紧分支：

- 只有旧 binding 需要继续复用时，才保留 `existing.ID`
- 如果旧 binding 已经终态：
  - 要么原地 reset/update 旧 binding
  - 要么生成新 ID 再创建新 binding
- 不能出现“复用旧 ID，但仍然执行 Create”的路径

### P0：明确 retry 对 await_binding 的处理策略

在 [task_retry_service.go](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/service/task_retry_service.go:53) 里补 await 语义：

- 对 retry 子树上的 await 节点，统一处理旧 binding
- 可选策略：
  - 标记旧 binding 为 `canceled`
  - 删除未完成 binding
  - 或在 retry 前显式归档旧 binding 并生成新 binding

### P1：补测试

至少补以下回归测试：

- 旧 binding 为 `failed` 时，retry 不会触发主键冲突
- 旧 binding 为 `completed` 时，retry 行为符合预期
- 旧 binding 为 `waiting` 时，仍然按当前幂等语义直接挂起
- `AwaitPollWorker` 完成旧 binding 后，再触发 retry，不会破坏 await 状态机

## 建议的后续动作

1. 先修 `executeAwaitNode()` 的主键复用漏洞，这是直接止血项。
2. 再补 `PrepareTaskRetry()` 对 `await_bindings` 的策略，避免 retry 语义继续悬空。
3. 最后补一组 await retry 回归测试，把这条链路纳入引擎测试网。
