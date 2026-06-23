# 05 · Capability Call, Result And Invoker

## 1. CapabilityCall

```go
type CapabilityCall struct {
    Name           string         `json:"name"`
    Target         ObjectRef      `json:"target"`
    Arguments      map[string]any `json:"arguments"`
    IdempotencyKey string         `json:"idempotency_key"`
}
```

`revise_review_by_fork` 示例：

```json
{
  "name": "revise_review_by_fork",
  "target": {
    "type": "review_artifact",
    "message_id": 2063058043737739264,
    "task_id": 2063057922778218496
  },
  "arguments": {
    "feedback": "把风格改为电影风格"
  },
  "idempotency_key": "cap:revise_review_by_fork:conv:1:review:2063058043737739264:feedback:sha256"
}
```

## 2. CapabilityResult

CapabilityResult 不能只有 `success=true`。它必须能驱动对话状态和 UI 更新。

```go
type CapabilityResultStatus string

const (
    CapabilityCompleted CapabilityResultStatus = "completed"
    CapabilityRejected  CapabilityResultStatus = "rejected"
    CapabilityFailed    CapabilityResultStatus = "failed"
    CapabilityNoop      CapabilityResultStatus = "noop"
)

type CapabilityResult struct {
    Status     CapabilityResultStatus `json:"status"`
    Summary    string                 `json:"summary"`
    Affected   []ObjectRef            `json:"affected,omitempty"`
    NewObjects []ObjectRef            `json:"new_objects,omitempty"`
    NextAction string                 `json:"next_action,omitempty"`
    ErrorCode  string                 `json:"error_code,omitempty"`
}
```

示例：

```json
{
  "status": "completed",
  "summary": "已停止当前运行，并根据你的反馈生成新版方案。",
  "affected": [
    {"type": "task", "task_id": 2063057922778218496},
    {"type": "review_artifact", "message_id": 2063058043737739264}
  ],
  "new_objects": [
    {"type": "plan", "plan_id": 2063059000000000000}
  ],
  "next_action": "await_plan_confirmation"
}
```

## 3. Invoker 事务边界

有副作用的 capability 必须满足：

- 调用前重新读取 target 当前状态。
- 权限、stage、task status、await binding status 全部重新校验。
- 参数校验通过后才进入事务。
- DB 副作用在单事务内完成，或通过 Outbox 做 post-commit。
- 幂等记录与副作用写入同事务。
- 失败时返回明确 error code，不得产生半个新版 Plan。

`revise_review_by_fork` 的第一版事务单元应包含：

```text
1. 锁定/幂等占位 capability call
2. 重新校验 ReviewCard/task/await binding
3. task -> canceled
4. 可取消 child task -> canceled
5. node runtime -> canceled
6. await binding -> canceled
7. AgentState 清理旧 pending review，进入 confirming 或 awaiting_user
8. Activity 写 superseded 语义
9. 旧 Plan 标记 revised/superseded
10. 创建新版 Plan
11. append 新版 PlanCard
12. 写 capability result
```

如果当前 UnitOfWork 无法覆盖 engine task 表与 agent 表，实施时必须新增跨库事务边界或拆成 Saga/Outbox，并保证失败可恢复。文档设计不假设现有仓储已满足。

## 4. 幂等键

建议：

```text
cap:{name}:conv:{conversation_id}:target:{target-stable-id}:input:{normalized-args-hash}
```

`revise_review_by_fork`：

```text
cap:revise_review_by_fork:conv:{conv_id}:review:{message_id}:task:{task_id}:feedback:{sha256(normalized_feedback)}
```

重复点击或重试：

- 参数完全相同：返回已完成的 CapabilityResult。
- 同一 target 但参数不同，且旧 target 已 superseded：返回 `target_stale`，提示基于最新 Plan 继续修改。
- 同一 target 并发：只能一个调用进入执行，另一个读取结果或得到 conflict。

## 5. Decision Builder 输出

CapabilityResult 应转成普通 `service.Decision`：

- `Status=completed` 且 `NewObjects` 含 plan：append PlanCard，`NextState.Stage=confirming`。
- `Status=rejected/noop`：append text，保留或清理 pending 取决于错误码。
- `Status=failed`：append text/error guidance，不创建 Plan，不取消 pending，或进入可重试状态。

CapabilityResult 是 runtime 内部结构，不直接暴露给客户端；客户端仍看 message/state/card。

