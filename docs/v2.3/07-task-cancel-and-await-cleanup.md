# 07 · Task Cancel And Await Cleanup

## 1. 为什么 superseded 不足够

产品语义 `superseded` 只说明旧运行被新版替代。Engine 不认识这个语义。

如果旧 task 仍是 `suspended`，旧 await binding 仍是 `waiting`：

- `AwaitPollWorker` 仍会扫描 poll/timeout due binding。
- 用户重复提交旧 ReviewCard signal 仍可能命中 waiting binding。
- `CompleteAwaitNode` 会 resume 旧 task。
- Recovery 仍可能处理 running/suspended 任务相关节点。

因此 V2.3 必须把旧 task 和 await binding 进入不可恢复状态。

## 2. 当前 Task 状态机

代码位置：`ai-engine/domain/task.go`, `ai-engine/domain/task_status.go:3`

状态：

```text
pending
running
success
failed
suspended
canceled
```

允许迁移：

```text
pending   -> running | canceled
running   -> suspended | success | failed | canceled
suspended -> running | failed | canceled
```

`canceled` 是 terminal。`engine.transitionTaskStatus` 会重新读取 DB，如果发现当前 task 已是 `canceled`，阻止后续写成 success/failed/suspended，并返回 `ErrTaskCanceled`。

## 3. Worker 如何避免执行 canceled task

代码位置：`ai-engine/repository/query/task.go:507`, `ai-engine/worker/worker.go:121`

`TryClaimTask` 只允许：

```text
status = pending
OR status = running AND started_at < leaseExpire
```

不会 claim `suspended` 或 `canceled`。

Worker claim 后再次读取 task：

```text
if task.Status == TaskCanceled -> queue.Ack and continue
```

对应代码在 `ai-engine/worker/worker.go:133`。

因此 task 真正进入 `canceled` 后，不会被普通队列 Worker 执行。

## 4. Recovery 如何处理 canceled task

代码位置：`ai-engine/worker/recovery_scanner.go:98`

RecoveryScanner 扫描 expired running nodes 后读取 task：

```text
TaskRunning/TaskSuspended -> 可处理恢复
TaskCanceled/cancelled -> cancelRunningNode
其它状态 -> skip
```

它不会把 canceled task 重新 enqueue。对 canceled task 只会把遗留 running node 标记为 `NodeCanceled`。

## 5. Await binding 状态机

代码位置：`ai-engine/domain/await_binding.go`

状态：

```text
pending
waiting
completing
completed
failed
timed_out
canceled
```

允许迁移：

```text
pending    -> waiting | canceled
waiting    -> completing | timed_out | canceled
completing -> completed | failed
```

`completed/failed/timed_out/canceled` 都应视为 terminal。

## 6. AwaitPollWorker 如何扫描

代码位置：`ai-engine/repository/query/await_binding.go:212`, `ai-engine/worker/await_poll_worker.go`

轮询和超时扫描只查：

```text
status = waiting AND next_poll_at <= now
status = waiting AND timeout_at <= now
```

因此把 binding 改为 `canceled` 并清理 `next_poll_at` 后，AwaitPollWorker 不会再处理它。

## 7. Signal 如何恢复 task

代码位置：`ai-engine/handler/await_handler.go:124`, `ai-engine/engine/await_complete.go:34`

Signal handler：

```text
FindWaitingBySignal(signal_name, callback_token)
```

只命中 `status=waiting`。

`CompleteAwaitNode` 再做一次原子 claim：

```text
ClaimCompleting(binding.ID, expectedStatuses=[waiting])
```

repository 层 `FindWaitingBySignal` 和 `ClaimCompleting` 的 SQL 条件分别在 `ai-engine/repository/query/await_binding.go:162` 与 `ai-engine/repository/query/await_binding.go:187`。

如果 binding 已 canceled，返回 noop，不会 resume task。

## 8. 当前取消能力缺口

现有 `WorkflowHandler.CancelTask`，见 `ai-engine/handler/workflow_handler.go:756`：

- 可将 root task 标记为 `TaskCanceled`。
- 可递归取消 child task。
- 可将可取消 NodeRuntime 改成 `NodeCanceled`。
- 有用户手动取消限制：pending 要等 1 分钟，suspended 要等 15 分钟且无活跃心跳。
- 没有看到同步取消 await binding。

现有 `taskRetryService.resetAwaitBindingsForRetry`，见 `ai-engine/service/task_retry_service.go:199`：

- 会列出 task 的 await bindings。
- 对非 terminal binding 写 `AwaitBindingCanceled`。
- 写 `CanceledAt`。
- 清理 `NextPollAt`、last event 字段。

V2.3 不应直接复用面向用户的 `CancelTask` 语义。应提炼一个内部取消原语：

```text
CancelRunForSupersededRevision(taskID, reason, idempotencyKey)
```

它不受 1 分钟/15 分钟用户手动取消限制，但必须有严格权限、状态与 capability policy 校验。

## 9. 内部取消原语要求

必须在一个一致性边界内完成：

```text
1. root task pending/running/suspended -> canceled
2. child task pending/running/suspended -> canceled
3. cancelable node runtime -> NodeCanceled
4. await binding pending/waiting -> canceled
5. await binding NextPollAt = nil
6. await binding CanceledAt = now
7. error_message/reason = superseded_by_revision
```

建议错误信息：

```text
superseded_by_revision
```

如果未来 task 增加 metadata/cancel_reason 字段：

```text
task.status = canceled
task.cancel_reason = superseded_by_revision
task.metadata.superseded_by_plan_id = new_plan_id
```

## 10. Observer 如何避免错误卡

当前 Observer 只处理：

```text
task_succeeded
task_failed
task_final_failed
generation_pipeline_stage
```

没有 `task_canceled` 事件路径，也没有 superseded Activity 状态。若内部取消复用失败路径并发 `task_failed`，Observer 会 append error_card，用户看到“创作失败”。

V2.3 需要：

- 取消旧运行时不要发布 `task_failed` / `task_final_failed`。
- 或新增 `task_canceled` / `task_superseded` 事件并让 Observer 写 superseded Activity。
- `finalize` 不应把 superseded 走 `StageFailed`。

第一版建议由 capability 在 agent 事务中直接更新 Activity 为 superseded，并清理 pending。Engine 侧只落 canceled 状态，不发 failed 终态事件。

## 11. 如何确保旧 awaiting Task 不会再次执行或恢复

必须同时满足：

1. `tasks.status=canceled`：Worker 不会 claim，Engine 后续写终态会被 `ErrTaskCanceled` 阻止。
2. `await_bindings.status=canceled`：Signal、poll、timeout 都不再命中。
3. `node_runtime.state=NodeCanceled`：Recovery 对残留节点不会当作可继续节点。
4. `AgentState.PendingMessageID` 清空或切到新 PlanCard：旧 ReviewCard 不是当前操作卡。
5. `TaskLink/Plan revision` 记录替代关系：后续 fork 来源和 UI 解释明确。

这是 V2.3 第一版正确性的红线。
