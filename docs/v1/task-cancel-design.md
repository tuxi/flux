# Task 取消 API 设计

日期：2026-05-02

## 1. 背景

当前系统没有用户主动取消任务的入口。以下场景需要取消能力：

| 场景 | Task 状态 | 卡住原因 |
|---|---|---|
| Worker 全部宕机，消息丢失 | `TaskPending` | 任务入队了但永远不会被消费 |
| Worker 进程崩溃 | `TaskRunning` | 节点心跳停止，等待 Scanner 发现的空窗期 |
| 引擎 bug 导致死循环 | `TaskRunning` | 节点心跳持续更新但无实际进展 |
| 异步回调丢失 | `TaskSuspended` | 第三方 webhook 没来，await binding 永远等不到 |

这些状态下，点数已被冻结，用户需要能主动取消以释放点数。

## 2. 可取消的状态

| 状态 | 允许取消 | 前提条件 |
|---|---|---|
| `TaskPending` | 是 | 任务创建超过 1 分钟（防止误操作刚创建的任务） |
| `TaskRunning` | 是 | 所有运行中节点的 `last_heartbeat` 均已过期（确认任务已死） |
| `TaskSuspended` | 是 | 任务挂起超过 5 分钟（给异步回调充分的到达时间） |
| `TaskFailed` | 否 | 已走完 auto-retry 并可能已退款，走手动重试路径 |
| `TaskSuccess` | 否 | 终态，已完成结算 |
| `TaskCanceled` | 否 | 已经是取消状态 |

## 3. 心跳存活判断

取消 `TaskRunning` 任务前，必须确认任务已死而非正在活跃执行。

### 3.1 判断逻辑

```
查询任务的所有 node_runtime
如果有任何节点满足以下条件 → 任务存活 → 拒绝取消：
  - state IN ('running', 'retrying')
  - last_heartbeat 不为 nil 且距今 < 2 分钟（心跳阈值）

否则 → 任务已死 → 允许取消
```

### 3.2 为什么用 2 分钟而非 Scanner 的 10 分钟

Scanner 的 10 分钟超时偏向"宁可漏判也不误判"，需要给足恢复时间。取消 API 的阈值可以更短，因为：

- 正常运行中的节点 heartbeat 每 5 秒更新一次
- 2 分钟已经是心跳间隔的 24 倍，足够容忍瞬时网络抖动
- 不需要等到 10 分钟才发现任务死了，用户可以更快拿回点数

### 3.3 异步节点的 heartbeat

**对于取消逻辑，异步节点不是问题。** 原因：

- 异步工具节点通过 `scheduleAsyncActivity` 执行 → 立即返回 `WorkflowSuspendedError` → task 状态变为 `TaskSuspended`
- 异步节点从不在 `TaskRunning` 状态中长期存在
- `TaskSuspended` 的取消不需要查心跳，只需要检查挂起时长

唯一需要关注的是：sync 工具内部挂起（抛 `WorkflowSuspendedError`）的情况。此时节点可能处于 `NodeRunning` 状态且 `last_heartbeat` 有值（因为 `runNodeWithHeartbeat` 曾启动过）。心跳阈值 2 分钟可以覆盖：挂起超过 2 分钟的，`last_heartbeat` 会过期，允许取消。

### 3.4 边缘情况：刚启动的节点

同步节点启动后，`runNodeWithHeartbeat` 的首次心跳在 5 秒后才写入。此期间 `last_heartbeat` 可能为 nil。需要增加对 `started_at` 的判断：

```
如果 last_heartbeat 为 nil 且 started_at 距今 < 30 秒 → 视为存活
```

## 4. API 设计

### 4.1 端点

```
POST /api/v1/ai/tasks/:id/cancel
```

### 4.2 处理流程

```
1. 加载 task
2. 校验 task 归属（只能取消自己的任务）
3. 校验 task 状态在允许列表中（pending/running/suspended）
4. 根据状态做存活检查：
   - pending: 检查创建时间 > 1 分钟
   - running: 检查所有运行中节点的心跳是否过期（见第 3 节）
   - suspended: 检查挂起时间 > 5 分钟
5. 调用 billingTaskSvc.SettleTaskFailure() 退还冻结的点数
6. 设置 task.Status = TaskCanceled
7. 返回成功
```

### 4.3 错误码

| 场景 | HTTP 状态码 | 错误信息 |
|---|---|---|
| 任务不存在 | 404 | task not found |
| 无权取消 | 403 | permission denied |
| 状态不允许取消 | 400 | task status "success" cannot be canceled |
| 任务仍活跃 | 409 | task is still active, heartbeat is recent |
| 挂起时间不足 | 425 | task suspended less than 5 minutes, please wait |
| 创建时间不足 | 425 | task created less than 1 minute, please wait |
| billing 已结算 | 409 | task billing already settled |

## 5. 取消后的恢复

取消不是终态。用户可以手动重试 `TaskCanceled` 的任务。重试时会走正常的冻结点数流程（`CreateTaskWithFreeze`），因为点数在取消时已退还。

详见手动重试流程（在 `PrepareTaskRetry` 中处理）。

## 6. 待定：超时自动取消

用户取消是手动操作。对于永不取消的僵尸任务（用户不上线、不操作），仍需要超时自动取消机制。该机制独立于本设计，建议后续实现为一个定时扫描任务。

## 7. 改动范围

| 文件 | 改动 |
|---|---|
| `ai-engine/handler/workflow_handler.go` | 新增 `CancelTask` handler |
| `ai-engine/router/` | 注册取消路由 |
| `ai-engine/worker/recovery_scanner.go` | 移除 `TaskFailed` 恢复（配套改动） |
| `ai-engine/service/task_retry_service.go` | `PrepareTaskRetry` 接受 `TaskCanceled`（配套改动） |
| `ai-engine/repository/query/node.go` | 可选：新增 `HasActiveHeartbeatNodes` 查询方法 |
