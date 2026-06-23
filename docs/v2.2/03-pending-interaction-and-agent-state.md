# 03 · PendingInteraction 与 AgentState

## 1. 为什么需要 PendingInteraction

当前 `AgentState` 已有：

- `Stage`
- `PendingMessageID`
- `MissingSlots`
- `CollectedSlots`

这些可以表达"正在等用户回应某张卡"，但不能稳定表达"这张卡在等什么语义"。

典型问题：

```text
Agent: 想怎么调整？
User: 横屏
```

如果只看 `MissingSlots`，无法知道这是在回答修改请求，而不是发起新需求。

V2.2 需要把 pending 的语义持久化。

## 2. 模型

建议第一版新增：

```go
type PendingInteraction struct {
    Kind            string
    AskedSlot       string
    TargetPlanID    *int64
    PromptMessageID *int64
}
```

第一版 Kind：

| Kind | 含义 |
|------|------|
| `collect_story_brief` | 等用户补故事 brief |
| `collect_slot` | 等用户补普通 slot |
| `collect_modification` | 等用户说明怎么修改 |
| `confirm_goal_switch` | 等用户确认是否切换目标 |

## 3. 存储策略

推荐新增 JSONB 字段：

```text
agent_states.pending_interaction jsonb
```

原因：

- Kind 未来会扩展，不适合第一版拆太多列。
- 与 `CollectedSlots` / `MissingSlots` 同属工作记忆。
- 迁移成本低。

兼容策略：

- 旧数据 `pending_interaction = null`。
- 如果 `stage=awaiting_user` 且 `missing_slots` 非空，可在 runtime 内派生 legacy pending。
- 新逻辑只写 `PendingInteraction`。

## 4. 与 PendingMessageID 的关系

`PendingMessageID` 仍然保留，它解决 UI 当前哪张卡可操作。

`PendingInteraction` 解决语义归属。

二者关系：

| 字段 | 解决问题 |
|------|----------|
| `PendingMessageID` | 客户端哪张消息显示操作按钮 |
| `PendingInteraction` | 用户下一句话应该归属到哪个等待语义 |

## 5. 状态更新规则

创建 clarify 时：

```text
stage = awaiting_user
pending_message_id = clarify.id
pending_interaction = {kind, asked_slot, target_plan_id, prompt_message_id}
```

用户回答 pending 后：

- 如果仍缺信息：更新 pending。
- 如果信息足够：清空 pending，生成 Plan。

取消时：

```text
pending_message_id = nil
pending_interaction = nil
```

smalltalk 时：

```text
pending_message_id 不变
pending_interaction 不变
```

## 6. DTO 与 API

前端第一版不一定需要完整暴露 `PendingInteraction`。

可选：

- API 暂不暴露，仅后端 runtime 使用。
- 调试环境暴露 `pending_interaction`，便于 QA 对话回放。

## 7. 迁移注意

涉及文件：

- `ai-engine/agent/domain/types.go`
- `ai-engine/agent/entity/conversation.go`
- `ai-engine/agent/repository/query/mapping.go`
- DDL / migration
- handler DTO 可选

实施时必须补：

- JSON round-trip test。
- 旧状态兼容 test。
- cancel 清理 pending test。
