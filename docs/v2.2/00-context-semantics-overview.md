# 00 · Context Semantics 总览

## 1. 背景

当前 Conversation 层已经能够构建：

```go
type ConversationContext struct {
    State  *domain.AgentState
    Recent []*domain.Message
    Input  *domain.Message
    Plan   *domain.Plan
}
```

这说明系统已经拥有 Context Data：

- `AgentState`：当前 stage、skill、已收集 slots、missing slots、current plan。
- `Recent`：最近 N 条 persistent messages。
- `Input`：当前用户输入。
- `Plan`：当前方案快照。

但 AgentRuntime 仍主要围绕当前单句做判断。上下文逻辑分散在：

- `Respond`
- `RuleIntentClassifier`
- `RuleSlotExtractor`
- `iterate`
- `mergeSlots`
- `contextualFallback`
- stage 判断

这些函数各自看一点上下文，却没有形成统一结论。

## 2. 架构根因

系统缺失一个稳定的 Context Semantics 层。也就是没有统一回答：

```text
用户这一句话在当前目标中是什么动作？
它针对当前追问、当前 Plan，还是一个新目标？
用户提供的信息是否足够？
下一步应该追问、规划、修改、重生成、取消还是闲聊？
```

因此每遇到一个真实对话，就容易继续新增字符串特判。

## 3. V2.2 目标

将 AgentRuntime 升级为：

```text
ConversationContext
  -> TurnInterpreter
  -> TurnInterpretation
  -> SkillSufficiencyEvaluator
  -> DialoguePolicy
  -> Decision
```

职责：

- `TurnInterpreter`：解释当前回合在上下文中的语义。
- `SkillSufficiencyEvaluator`：判断当前 skill 的信息是否足够生成 Plan。
- `DialoguePolicy`：根据解释结果和状态，确定下一步行为。
- `ConversationService`：仍只负责读取上下文、调用 runtime、事务持久化 Decision。

## 4. 非目标

V2.2 不做：

- 不接 LLM fallback。
- 不重构 Workflow Engine / Outbox / Observer。
- 不把自然语言规则放进 `ConversationService`。
- 不让 LLM 直接决定是否创建 Plan 或启动 Workflow。
- 不扩展新 Skill。

## 5. 架构原则

### 5.1 Service 不理解自然语言

`ConversationService` 只负责：

- 读取 `Conversation` / `AgentState` / `Recent` / `Plan`。
- 构建 `ConversationContext`。
- 调用 `AgentRuntime`。
- CAS、事务、Outbox、Signal 持久化。

不得新增：

```go
if text == "改一下" {}
if text == "好的" {}
if text == "都市爱情" {}
```

### 5.2 Interpreter 负责理解，Policy 负责决定

`TurnInterpreter` 只输出语义解释，不创建 Plan，不改 State，不发消息。

`DialoguePolicy` 使用确定性规则决定：

- 回复文本。
- 追问 clarify。
- 创建 PlanCard。
- 修改或重生成 Plan。
- 取消 pending 或当前目标。

### 5.3 上下文优先于孤立文本

解释顺序：

1. 当前是否存在 `PendingInteraction`。
2. 当前是否存在 active goal / current plan。
3. 最近 Agent 是否提出问题或建议。
4. 用户是否明确表达新 skill / 新目标。
5. 最后才做全局 intent 分类。

原则：

```text
先判断用户正在延续什么，再判断用户新说了什么。
```

## 6. 与主流 Agent 的对应关系

Codex、Claude Code 这类 Agent 的核心不是简单保存聊天记录，而是维护工作状态、当前目标、待完成事项和下一步动作。V2.2 对应的是其中最核心的"本轮输入如何作用于当前任务状态"。

当前系统已经具备 Agent First 的持久状态、Plan、Outbox 和 Observer。V2.2 只补会话语义解释层，不推翻既有架构。
