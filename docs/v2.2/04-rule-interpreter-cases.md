# 04 · 规则解释器案例

## 1. 目标

第一版 `RuleTurnInterpreter` 必须覆盖真实问题，而不是追求通用自然语言理解。

核心原则：

```text
上下文状态解释优先于孤立文本关键词分类。
```

## 2. 创作请求充分性

| 输入 | Interpretation | Sufficiency | Decision |
|------|----------------|-------------|----------|
| "做一个短剧" | `start_goal`, intent=`short_drama` | 缺 story brief | 追问故事点子 |
| "做一个搞笑的短剧吧" | `start_goal`, style=`搞笑` | 缺 story brief | 追问搞笑故事点子 |
| "帮我做一个一分钟短剧" | `start_goal`, duration_hint | 缺 story brief | 追问故事点子 |
| "帮我做一个一分钟都市爱情短剧" | `start_goal`, duration/style | 缺 story brief | 追问故事点子 |
| "做一个程序员第一天上班误删数据库的搞笑短剧" | `start_goal`, style + story brief | 足够 | PlanCard |

规则：

- 不得继续把完整技能命令直接保存为 `user_prompt`。
- `user_prompt` 必须更接近故事 brief，而不是"我要做一个短剧"这种操作壳。

## 3. 承接表达

### 3.1 回答故事追问

```text
Agent: 想讲一个什么样的故事？
User: 都市爱情
```

解释：

```text
Act = answer_question
AnsweredSlot = user_prompt
Target = pending
ExtractedSlots = {user_prompt: "都市爱情", style: "都市爱情"}
```

### 3.2 接受上一轮建议

```text
Agent: 要不要先做一个短剧？
User: 好的
```

解释：

```text
Act = start_goal 或 confirm
Intent = short_drama
Target = recent_agent_suggestion
```

如果缺 story brief，继续追问，不复读能力介绍。

## 4. 修改场景

| State | 输入 | Interpretation | Decision |
|-------|------|----------------|----------|
| completed | "改一下" | `request_modification` | 追问怎么改，不创建 Plan |
| confirming | "调整一下" | `request_modification` | 追问怎么改，不创建 Plan |
| completed | "再来一版" | `regenerate` | 生成新版 Plan |
| completed | "改成横屏" | `provide_modification`, aspect_ratio | 生成新版 Plan |
| confirming | "第二幕改成夜晚" | `provide_modification` | 生成新版 Plan |
| awaiting_user + collect_modification | "横屏" | `answer_question` + modification | 生成新版 Plan |

禁止：

```text
user_prompt = "旧 prompt；调整：改一下"
```

## 5. Smalltalk

```text
State = awaiting_user
PendingInteraction = collect_story_brief
User = "你是谁"
```

解释：

```text
Act = smalltalk
```

Policy：

- 回复身份介绍。
- 保留 pending。
- 附轻锚定："我还在等你的故事点子。"

## 6. Cancel

```text
State = awaiting_user
User = "算了"
```

解释：

```text
Act = cancel
```

Policy：

- 清 `PendingInteraction`。
- 清 `PendingMessageID`。
- 取消当前 collecting 目标。
- 不创建 Plan。

## 7. Unknown

Unknown 不是固定文案。

规则：

- 有 pending：回到 pending。
- 有 plan：提示确认或调整。
- 上一轮刚 fallback：换文案。
- 无上下文：给能力边界和下一步建议。
