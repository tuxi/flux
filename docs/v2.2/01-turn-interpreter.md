# 01 · TurnInterpreter 契约

## 1. 定位

`TurnInterpreter` 负责解释用户当前回合在 `ConversationContext` 中的语义。

它不负责：

- 持久化。
- 创建 Plan。
- 启动 Workflow。
- 写 Outbox。
- 生成最终 UI 消息。

它只回答：

```text
用户这句话在当前上下文里是什么意思？
```

## 2. DialogueAct

第一版支持：

```go
type DialogueAct string

const (
    ActStartGoal           DialogueAct = "start_goal"
    ActAnswerQuestion      DialogueAct = "answer_question"
    ActConfirm             DialogueAct = "confirm"
    ActRequestModification DialogueAct = "request_modification"
    ActProvideModification DialogueAct = "provide_modification"
    ActRegenerate          DialogueAct = "regenerate"
    ActCancel              DialogueAct = "cancel"
    ActSmalltalk           DialogueAct = "smalltalk"
    ActSwitchGoal          DialogueAct = "switch_goal"
    ActUnknown             DialogueAct = "unknown"
)
```

语义：

| Act | 含义 |
|-----|------|
| `start_goal` | 用户发起新目标或承接上一轮建议启动目标 |
| `answer_question` | 用户正在回答 pending 追问 |
| `confirm` | 用户确认当前方案或建议 |
| `request_modification` | 用户表达想修改，但未给具体修改内容 |
| `provide_modification` | 用户提供了可执行修改内容 |
| `regenerate` | 用户要求再来一版 / 重生成 |
| `cancel` | 用户取消 pending 或当前目标 |
| `smalltalk` | 闲聊 / 身份 / 帮助，不打断当前目标 |
| `switch_goal` | 用户明确切换到另一个目标或 skill |
| `unknown` | 无法可靠解释 |

## 3. TurnInterpretation

建议模型：

```go
type TurnInterpretation struct {
    Act            DialogueAct
    Intent         string
    Target         GoalReference
    TargetPlanID   *int64
    ExtractedSlots map[string]any
    AnsweredSlot   string
    Modification   *ModificationIntent
    Confidence     float64
    Reason         string
}

type GoalReference string

const (
    TargetNone       GoalReference = "none"
    TargetPending    GoalReference = "pending"
    TargetCurrent    GoalReference = "current"
    TargetCurrentPlan GoalReference = "current_plan"
    TargetNewGoal    GoalReference = "new_goal"
)
```

说明：

- `Act` 是最重要字段，Policy 主要根据它分支。
- `Intent` 是候选 skill intent，如 `short_drama`。
- `Target` 表示本轮针对 pending、当前目标、当前 plan，还是新目标。
- `ExtractedSlots` 是本轮可确定的信息，不等于最终 merged slots。
- `AnsweredSlot` 只在回答 pending slot 时设置。
- `Confidence` 用于未来 LLM fallback 与低置信兜底。
- `Reason` 用于测试与日志，可解释为什么判成该 act。

## 4. 接口

```go
type TurnInterpreter interface {
    Interpret(ctx context.Context, c *service.ConversationContext) (TurnInterpretation, error)
}
```

第一版实现：

```text
RuleTurnInterpreter
```

未来实现：

```text
HybridTurnInterpreter = RuleTurnInterpreter + LLMTurnInterpreter fallback
```

## 5. 解释优先级

规则解释顺序必须固定：

1. `cancel`：任何状态下优先识别。
2. `PendingInteraction`：如果正在等用户回答，优先判断是否回答 pending。
3. 当前 plan / goal：判断 confirm、modify、regenerate、smalltalk。
4. 最近 Agent 建议或问题：处理"好的"、"可以"、"都市爱情"等承接表达。
5. 明确新目标：短剧、图片、带货视频等 skill intent。
6. smalltalk / identity / help。
7. unknown。

## 6. 与旧 ports 的关系

V2.2 后：

- `IntentClassifier` 不再作为 runtime 顶层入口。
- `SlotExtractor` 不再决定是否信息充分。
- 二者可下沉为 `RuleTurnInterpreter` 内部 helper。

迁移期可以保留接口，直到 `TurnInterpreter` 覆盖现有测试。
