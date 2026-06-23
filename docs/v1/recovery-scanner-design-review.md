# RecoveryScanner 设计与 Task 状态结算审查

审查日期：2026-05-02

## 1. heartbeat 更新机制 —— 异步等待期间的误判风险

**结论：存在部分风险。`NodeAwaiting` 状态和 async 工具节点安全，但同步工具内部挂起路径危险。**

心跳只在 `runNodeWithHeartbeat`（`ai-engine/engine/engine.go:278`）运行期间持续更新（每 5 秒）。函数返回时 `defer close(done)` 立即停止心跳 goroutine。

| 场景 | 节点状态 | heartbeat 是否更新 | 是否会被误扫 |
|---|---|---|---|
| Await 节点 (`NodeAwait` type) | `NodeRunning` → `NodeAwaiting` | 从不启动心跳 | **安全** — `FindExpiredRunningNodes` 只查 `running`/`retrying`，不包含 `awaiting` |
| Async 工具节点 (`Mode() == AsyncExecution`) | `NodeRunning`，然后挂起 | 不启动心跳，`last_heartbeat` 为 nil | **安全** — SQL 条件 `last_heartbeat IS NOT NULL` 排除 |
| **同步工具内部挂起** | `NodeRunning` | 心跳启动后停止，`last_heartbeat` 保留挂起前的值 | **危险** — 外部 API 等待超过 10 分钟会被误判 crash |

**问题时序：**

```
T0:   同步 tool step 执行，runNodeWithHeartbeat 启动，heartbeat 每 5s 更新
T1:   tool step 提交 Kling 任务，创建 await binding，throw WorkflowSuspendedError
T1:   heartbeat goroutine 停止，last_heartbeat 冻结在 T1 附近
T1+10m: RecoveryScanner 发现 last_heartbeat < now-10min → 误判 crash
       → PrepareTaskRetry → Enqueue → 任务被错误重试
T1+15m: Kling 回调到达，但任务可能已在重新执行或状态已被破坏
```

**建议修复：** 在 `runDAG`（`ai-engine/engine/executor.go:599`）捕获 `WorkflowSuspendedError` 时，对挂起的节点显式地将 `last_heartbeat` 置 nil 或状态转为 `NodeAwaiting`，使其被 `FindExpiredRunningNodes` 的查询条件排除。

---

## 2. PrepareTaskRetry 的幂等性

**结论：有多层保护，但非事务性，存在低概率的边缘情况。**

三层保护：

1. **节点状态重置**（`ai-engine/service/task_retry_service.go:465`）：`resetNodeRuntimeForRetry` 将节点状态改为 `NodePending`。下一次 `FindExpiredRunningNodes`（只查 `running`/`retrying`）不会再找到。
2. **任务状态检查**（`ai-engine/service/task_retry_service.go:69-73`）：入口处校验 task 必须是 `TaskFailed`/`TaskSuspended`/`TaskRunning`。若已被上一轮改为 `TaskPending` 则直接返回 error。
3. **顺序扫描**（`ai-engine/worker/recovery_scanner.go:47`）：ticker 驱动，扫描串行，不会并发重叠。

**边缘情况：** 若在 `PrepareTaskRetry` 成功（task → `TaskPending`）之后、`Enqueue` 之前进程崩溃，任务会卡在 `TaskPending` 但不在队列中。下一轮 scan 不会发现（节点已是 `pending`），任务永久卡住。

**建议：** 增加对长时间处于 `TaskPending` 且无运行中节点的任务的兜底扫描。

---

## 3. TaskFailed 与计费结算 —— 已确认设计（2026-05-02）

### 3.1 核心澄清

`TaskFailed` 状态**本身不触发退款**。退款的唯一触发源是 `TaskEventFinalFailed` **事件**。

详见 `ai-engine/docs/task-terminal-billing-settlement-design.md` 第 4.5 节：

> `task_failed` 只表示"一次 attempt 失败"，不表示"最终失败"。自动重试窗口内的失败不应退款。

### 3.2 完整生命周期

```
任务创建 → CreateTaskWithFreeze（冻结点数）→ TaskPending
→ Worker 拉起 → TaskRunning
→ executeTask():
    ├─ RunSuccess  → TaskSuccess + TaskEventSucceeded → ConsumeTask （扣点）
    ├─ RunSuspended → TaskSuspended（不触发结算）
    └─ RunFailed   → TaskFailed + TaskEventFailed:
                        ├─ RetryCount < 3 → PrepareTaskRetry → TaskPending → 重新入队（点数保持冻结）
                        └─ RetryCount >= 3 → failTask():
                                               → TaskFailed（不触发结算）
                                               → publishFinalFailed(TaskEventFinalFailed)
                                               → Billing Listener → SettleTaskFailure（退款）
```

### 3.3 Scanner 对 TaskFailed 的恢复策略 —— 决定移除

**决定：Scanner 不再恢复 `TaskFailed` 状态的任务。**

原因：
1. `TaskFailed` 在 auto-retry 耗尽后会发布 `TaskEventFinalFailed` 触发退款
2. 点数已退还，Scanner 恢复会导致用户免费执行（见原计费一致性问题）
3. auto-retry 的 3 次机会已充分覆盖瞬时故障，持续失败说明需要用户介入

**改动：** `recovery_scanner.go:87-91` 中移除 `domain.TaskFailed`，只保留 `TaskRunning` 和 `TaskSuspended`。

### 3.4 手动重试

用户可以对 `TaskFailed` 或 `TaskCanceled` 任务发起手动重试。重试时会重新冻结点数（走正常的 `CreateTaskWithFreeze` 流程），保证计费一致性。

---

## 4. 冻结点数的超时释放兜底

**结论：没有超时自动释放机制，存在点数永久冻结风险。**

冻结的点数只能通过以下路径释放：
- `SettleTaskSuccess` → `ConsumeTask`（任务成功）
- `SettleTaskFailure` → `RefundTask`（`TaskEventFinalFailed` 事件触发）
- `CancelTaskFreeze` → `unfreezeTask`（用户主动取消，见[任务取消设计](task-cancel-design.md)）

**可能永久冻结的场景：**
1. 任务卡在 `TaskSuspended` 等待异步回调，但回调永远不来（第三方 webhook 丢失）
2. 任务卡在 `TaskPending` 但不在队列中（PrepareTaskRetry 成功但 Enqueue 失败的边缘情况）
3. 任务 `TaskRunning` 但节点 heartbeat 持续更新（<10min）却实际上无进展（死循环）

上述场景下 `TaskEventFinalFailed` 永远不会发布，点数永久冻结。

**建议：** 增加定时任务（如每小时），扫描超过 N 小时（如 24 小时）仍未完成的 `TaskBillingRecord`（status = `frozen`），自动调用 `RefundTask` 释放点数。

---

## 总结

| 问题 | 严重程度 | 最终决定 |
|---|---|---|
| heartbeat 在异步等待期间停止 | 中 | 方案已明确，待实施 |
| PrepareTaskRetry 幂等性 | 低 | 增加 TaskPending 僵尸任务的兜底扫描 |
| TaskFailed 被 Scanner 恢复 + 计费不一致 | **高** | Scanner 移除 `TaskFailed`，只恢复 `TaskRunning`/`TaskSuspended`。`TaskFailed` 任务由用户手动重试，重试时重新冻结点数 |
| 冻结点数无超时释放 | **高** | 用户可通过取消 API 主动释放（[任务取消设计](task-cancel-design.md)），超时兜底方案待后续实施 |
