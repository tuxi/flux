# 07 · 回归矩阵

## 1. 目标

V2.2 回归矩阵用于锁定上下文语义，而不是只验证字符串回复。

每个 case 至少断言：

- `DialogueAct`
- `Stage`
- `PendingInteraction`
- 是否创建 Plan
- slots 是否正确
- 文案是否不复读 / 不打断 pending

## 2. 创作请求

| Case | 输入 | 期望 |
|------|------|------|
| C01 | "做一个短剧" | 追问 story brief，不创建 Plan |
| C02 | "做一个搞笑的短剧吧" | 提取 style=搞笑，追问 story brief，不创建 Plan |
| C03 | "帮我做一个一分钟短剧" | 记录 duration_hint，追问 story brief，不创建 Plan |
| C04 | "帮我做一个一分钟都市爱情短剧" | 记录 duration/style，追问 story brief，不创建 Plan |
| C05 | "做一个程序员第一天上班误删数据库的搞笑短剧" | 信息充分，创建 PlanCard |

## 3. Pending 回答

| Case | 对话 | 期望 |
|------|------|------|
| P01 | Agent 追问 story brief -> "都市爱情" | answer_question，填 user_prompt/style，PlanCard |
| P02 | Agent 追问 story brief -> "你是谁" | smalltalk，pending 保留 |
| P03 | Agent 追问 story brief -> "算了" | cancel，pending 清空 |
| P04 | Agent 建议短剧 -> "好的" | 承接建议，不复读能力介绍 |

## 4. 修改

| Case | State | 输入 | 期望 |
|------|-------|------|------|
| M01 | completed | "改一下" | request_modification，追问怎么改，不创建 Plan |
| M02 | confirming | "调整一下" | request_modification，追问怎么改，不创建 Plan |
| M03 | completed | "再来一版" | regenerate，创建新版 Plan |
| M04 | completed | "改成横屏" | provide_modification，aspect_ratio=16:9，新版 Plan |
| M05 | awaiting_user collect_modification | "横屏" | answer_question，归属 modification，新版 Plan |
| M06 | completed | "第二幕改成夜晚" | provide_modification，剧情修改，新版 Plan |

## 5. Fallback

| Case | 对话 | 期望 |
|------|------|------|
| F01 | unknown -> unknown | 第二次 fallback 不得复读 |
| F02 | awaiting_user -> unknown | 提醒 pending 问题 |
| F03 | confirming -> unknown | 提醒可确认或调整 |

## 6. 真实对话回归

### R01

```text
用户：你好啊，今天
用户：我想做一个1分钟的
用户：好的
```

期望：

- 第一轮可引导短剧。
- 第二轮记录 duration_hint，进入 story brief 追问。
- 第三轮不复读兜底，不创建 Plan，继续锚定故事点子。

### R02

```text
用户：帮我做一个短剧
用户：你是谁
用户：都市爱情
```

期望：

- 第一轮追问 story brief。
- 第二轮身份回复，pending 保留。
- 第三轮归属 story brief，创建 Plan。

### R03

```text
用户：帮我做一个一分钟短剧
用户：都市爱情
```

期望：

- 第一轮记录 duration_hint，追问 story brief。
- 第二轮归属 story brief，保留 duration，创建 Plan。

### R04

```text
用户：做一个搞笑的短剧吧
用户：改一下
```

期望：

- 第一轮追问搞笑故事点子。
- 第二轮不能把"改一下"写入 `user_prompt`；应说明仍需要故事点子或先完成 brief。

## 7. 测试分层

| 层级 | 测试 |
|------|------|
| Unit | `RuleTurnInterpreter` 表驱动 |
| Unit | `SkillSufficiencyEvaluator` |
| Unit | `DialoguePolicy` |
| Repository | `PendingInteraction` JSON round-trip |
| Integration | `ConversationService` 端到端对话 |
| Regression | 本文真实对话矩阵 |
