# 02 · TargetResolver

## 1. 职责

TargetResolver 只回答：

```text
用户当前动作作用于哪个对象？
```

它不解释如何修改，不选择 capability，不执行副作用。

输入：

- `ConversationContext`
- `ActiveObjects`
- `TurnInterpretation`
- 用户当前文本

输出：

```go
type TargetResolution struct {
    Target     ObjectRef
    Confidence float64
    Reason     string
}
```

## 2. 优先级

第一版解析优先级：

1. `PendingInteraction.Target`
2. 当前阻塞 Review Artifact
3. 用户明确提到的对象
4. 当前 Plan
5. 最新 Result

## 3. Review 阶段默认目标

当 `AgentState.Stage=reviewing` 且 `PendingMessageID` 指向 `review_card`：

```text
用户: 修改一下
```

默认目标必须是当前 `review_artifact`，不是当前 plan。

理由：

- 用户当前视觉焦点是 ReviewCard。
- Engine 当前阻塞在 await binding。
- 旧运行不处理会继续占据 suspended/awaiting 状态。
- 普通 plan 修改不会取消旧 task，也不会清理 await binding。

## 4. 两轮修改链路

第一轮：

```text
state = reviewing
pending_message_id = prompt_review_card.id
user = 修改一下
```

解析：

```json
{
  "target": {
    "type": "review_artifact",
    "message_id": 2063058043737739264,
    "task_id": 2063057922778218496
  },
  "confidence": 0.95,
  "reason": "reviewing stage with active review_card"
}
```

Policy 因信息不足而追问，并写入：

```json
{
  "kind": "collect_modification",
  "target": {
    "type": "review_artifact",
    "message_id": 2063058043737739264,
    "task_id": 2063057922778218496
  }
}
```

第二轮：

```text
user = 把风格改为电影风格
```

TargetResolver 直接使用 `PendingInteraction.Target`，不重新落到 plan。

## 5. Stale 目标

每次解析后必须重新验证目标：

- `review_card.message_id` 仍是当前 `PendingMessageID`，或明确允许从 pending target 继续处理。
- conversation 当前 stage 仍允许该对象动作。
- task 仍是当前 active task，且状态允许修订。
- 对应 await binding 仍存在且处于 `waiting`。

如果验证失败：

```text
TargetResolution = stale
Decision = 文案提示“这张确认卡已失效，请以最新方案为准”
```

不得继续调用 capability。

