# 02 · DialoguePolicy 决策表

## 1. 定位

`DialoguePolicy` 负责把 `TurnInterpretation` 转成 `Decision`。

V2.2-4 的离线形态先输出 `DialogueDirective`，不直接输出 `service.Decision`。它是确定性的，不调用 LLM，不直接访问数据库。

输入：

```text
ConversationContext + TurnInterpretation + SkillSufficiencyResult
```

V2.2-4 输出：

```text
DialogueDirective
```

V2.2-5 Runtime 接入时再由 adapter / builder 转换为：

```text
service.Decision
```

## 2. 接口

```go
type DialoguePolicy interface {
    Decide(ctx context.Context, in DialoguePolicyInput) (DialogueDirective, error)
}
```

`DialoguePolicyInput`：

```go
type DialoguePolicyInput struct {
    Context        *service.ConversationContext
    Interpretation TurnInterpretation
    Sufficiency    *SufficiencyResult
}
```

`smalltalk` / `cancel` / `regenerate` / `confirm` 可以不要求 sufficiency；`start_goal` / `answer_question` / `request_modification` / `provide_modification` 必须提供 sufficiency。

## 3. Sufficiency

信息充分性必须独立于通用 act 解释。

```go
type SufficiencyResult struct {
    Sufficient   bool
    MissingInfo  []string
    MissingSlots []string
    Reason       string
}

type SkillSufficiencyEvaluator interface {
    Evaluate(ctx context.Context, m *skill.Manifest, it TurnInterpretation) SufficiencyResult
}
```

原因：同一句话对不同 skill 的充分性不同。
同时同一组 slots 在不同 `DialogueAct` 下含义也不同，例如"都市爱情"作为冷启动请求可能只是类型，但作为 `answer_question` 且 `AnsweredSlot=user_prompt` 时，是对当前追问的有效回答。

示例：

| 输入 | short_drama | text_to_image |
|------|-------------|---------------|
| "搞笑风格" | 不足，缺故事 brief | 可能不足，缺画面主体 |
| "都市爱情" | 不足，只有类型 | 可能可作为风格/主题 |
| "程序员第一天上班误删数据库" | 足够 | 可能足够生成画面 |

## 4. 第一版决策表

| State | Act | Sufficiency | Policy |
|-------|-----|-------------|--------|
| any | `cancel` | - | 清 pending；若有 active goal 则取消当前目标；回 text |
| any | `smalltalk` | - | 回 smalltalk；保留 stage / pending / current plan |
| idle | `start_goal` | insufficient | 写 collecting/awaiting_user；出 clarify |
| idle | `start_goal` | sufficient | 生成 Plan；进入 confirming |
| awaiting_user | `answer_question` | insufficient | 合并已答信息；继续 clarify |
| awaiting_user | `answer_question` | sufficient | 合并 slots；生成 Plan；进入 confirming |
| awaiting_user | `smalltalk` | - | 回复 smalltalk + pending anchor；保留 pending |
| confirming | `confirm` | - | ConfirmPlan 仍走 signal；文本确认可视情况转 confirm 或提示点按钮 |
| confirming | `request_modification` | insufficient | 追问怎么调整；设置 pending modification |
| confirming | `provide_modification` | sufficient | 合并修改；生成新版 Plan |
| confirming | `regenerate` | sufficient | 基于当前 Plan 生成新版 Plan |
| completed | `regenerate` | sufficient | 基于当前 Plan 生成新版 Plan |
| completed | `request_modification` | insufficient | 追问具体修改；不创建 Plan |
| completed | `provide_modification` | sufficient | 合并修改；生成新版 Plan |
| executing/reviewing | `request_modification` | - | 不打断执行；提示当前任务进行中，可完成后修改 |
| any | `unknown` | - | 基于上下文推进，不复读固定 fallback |

## 5. Plan 创建规则

只有满足以下条件才能创建 Plan：

1. 已确定唯一 Skill。
2. `SufficiencyResult.Sufficient == true`。
3. 必填 slot 满足 manifest。
4. 当前 act 允许创建或更新 Plan。

不得因为 `SlotExtractor` 给出了 `user_prompt` 就直接创建 Plan。

## 6. Modification 规则

`request_modification` 与 `provide_modification` 必须区分。

示例：

| 输入 | Act | 处理 |
|------|-----|------|
| "改一下" | `request_modification` | 追问怎么改，不创建 Plan |
| "调整一下" | `request_modification` | 追问怎么改，不创建 Plan |
| "改成横屏" | `provide_modification` | `aspect_ratio=16:9`，生成新版 Plan |
| "第二幕改成夜晚" | `provide_modification` | 追加剧情修改，生成新版 Plan |
| "再来一版" | `regenerate` | 保留 slots，生成新版 Plan |

## 7. Fallback 规则

Fallback 必须看上下文：

- 如果有 pending：提醒 pending 问题。
- 如果有 current plan：提示可确认或调整。
- 如果刚说过同一句 fallback：换推进式文案。
- 如果完全未知：给能力边界和一个下一步。

禁止连续两次同一句兜底。
