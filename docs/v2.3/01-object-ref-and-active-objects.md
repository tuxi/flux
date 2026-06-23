# 01 · ObjectRef And ActiveObjects

## 1. ObjectRef

`ObjectRef` 是对会话内对象的稳定引用，只保存可重新解析的标识，不复制完整对象数据。

建议模型：

```go
type ObjectType string

const (
    ObjectPlan           ObjectType = "plan"
    ObjectReviewArtifact ObjectType = "review_artifact"
    ObjectTask           ObjectType = "task"
    ObjectResult         ObjectType = "result"
    ObjectActivity       ObjectType = "activity"
)

type ObjectRef struct {
    Type      ObjectType `json:"type"`
    ID        string     `json:"id,omitempty"`
    MessageID *int64     `json:"message_id,omitempty"`
    PlanID    *int64     `json:"plan_id,omitempty"`
    TaskID    *int64     `json:"task_id,omitempty"`
}
```

第一版 `review_artifact` 可以由 `message_id + task_id` 定位：

- `message_id` 指向 `agent_conversation_messages.kind=review_card`。
- `task_id` 指向当前 conversation 最新 task link 的 task。
- `card_type`、`signal`、`payload` 从 message `content_json` 重新读取。
- await binding 通过 `task_id + signal/callback_token` 或 `task_id + node_name` 重新解析。

## 2. ActiveObject

`ActiveObject` 表示当前回合真正可操作的对象，不是会话历史里的全部对象。

建议模型：

```go
type ActiveObject struct {
    Ref          ObjectRef
    Stage        string
    State        string
    Capabilities []CapabilityDescriptor
}
```

每轮只构建这些候选：

| 来源 | 对象 |
| --- | --- |
| `PendingInteraction.Target` | 明确等待用户补充的对象 |
| `AgentState.Stage=reviewing` + `PendingMessageID` | 当前阻塞 Review Artifact |
| `AgentState.CurrentTaskID` 或最新 task link | 当前运行 Task |
| `Conversation.CurrentPlanID` / `AgentState.CurrentPlanID` | 当前 Plan |
| 最近 `result_card` | 最新 Result |
| `activity` message | 当前 Activity |

禁止把全部历史 Plan、Task、Card 交给 TargetResolver。历史对象可以在用户明确指代时按需检索，但不是默认活跃对象。

## 3. 当前代码中的对象映射

| ObjectType | 当前数据来源 | 第一版状态推导 |
| --- | --- | --- |
| `plan` | `agent_conversation_plans` + `current_plan_id` | `draft/confirmed/executing/done/revised` |
| `review_artifact` | `review_card` message + `pending_message_id` | `active/stale/superseded` |
| `task` | `agent_conversation_task_links` + `tasks` | `pending/running/suspended/success/failed/canceled` |
| `result` | `result_card` message + task output | `available/stale` |
| `activity` | `kind=activity` message | `running/waiting_user/completed/failed/superseded` |

## 4. PendingInteraction 目标感知升级

当前模型：

```go
type PendingInteraction struct {
    Kind            PendingInteractionKind `json:"kind"`
    AskedSlot       string                 `json:"asked_slot,omitempty"`
    TargetPlanID    *int64                 `json:"target_plan_id,omitempty"`
    PromptMessageID *int64                 `json:"prompt_message_id,omitempty"`
}
```

V2.3 建议演进：

```go
type PendingInteraction struct {
    Kind            PendingInteractionKind `json:"kind"`
    Target          *ObjectRef             `json:"target,omitempty"`
    AskedSlot       string                 `json:"asked_slot,omitempty"`
    PromptMessageID *int64                 `json:"prompt_message_id,omitempty"`

    // migration-only; retained while old code still reads it.
    TargetPlanID    *int64                 `json:"target_plan_id,omitempty"`
}
```

Review 修改追问示例：

```json
{
  "kind": "collect_modification",
  "target": {
    "type": "review_artifact",
    "message_id": 2063058043737739264,
    "task_id": 2063057922778218496
  },
  "prompt_message_id": 2063058050000000000
}
```

PendingInteraction 不保存完整 ReviewCard、Plan 或 Capability 列表。恢复时必须通过 `ObjectRef` 重新解析目标当前状态，避免旧能力在对象已失效后继续可用。

## 5. ReviewCard 如何绑定 Task / Plan / Stage

当前真实绑定链：

```text
Conversation.current_plan_id
  -> agent_conversation_task_links(plan_id, task_id, relation)
  -> tasks.id
  -> workflow emits pipeline event(card_type)
  -> Observer appends review_card message
  -> AgentState.stage = reviewing
  -> AgentState.pending_message_id = review_card.message_id
  -> await_bindings(task_id, signal_name, callback_token=task_id, status=waiting)
```

V2.3 的 `ActiveObjectResolver` 必须把这条链合并成一个 `review_artifact` ActiveObject。用户面对的不是抽象 plan，而是这个“当前等待审核的 artifact”。

