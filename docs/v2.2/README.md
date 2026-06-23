# Agent First V2.2 文档

> V2.2 = Conversation Context Interpretation & Dialogue Policy。
> 本轮是 AgentRuntime 的会话语义架构升级：先完成文档与评审，再实施。

## 定位

V2.2 不再继续修补单点对话问题：

- "改一下" 不能直接污染 `user_prompt`。
- "做一个搞笑短剧" 不能在故事 brief 不足时直接生成 PlanCard。
- "都市爱情" 要能稳定归属上一轮追问。
- "好的" 要能承接上一轮建议。
- smalltalk 不应打断 pending。

这些问题拥有同一个根因：系统已经有 `ConversationContext` 数据，但缺少统一的 Context Semantics。

目标架构：

```text
ConversationContext
  -> TurnInterpreter
  -> TurnInterpretation
  -> SkillSufficiencyEvaluator
  -> DialoguePolicy
  -> Decision
```

## 阅读顺序

1. [00 · Context Semantics 总览](00-context-semantics-overview.md)
2. [01 · TurnInterpreter 契约](01-turn-interpreter.md)
3. [02 · DialoguePolicy 决策表](02-dialogue-policy.md)
4. [03 · PendingInteraction 与 AgentState](03-pending-interaction-and-agent-state.md)
5. [04 · 规则解释器案例](04-rule-interpreter-cases.md)
6. [05 · LLM Interpreter Roadmap](05-llm-interpreter-roadmap.md)
7. [06 · 实施与迁移计划](06-implementation-and-migration-plan.md)
8. [07 · 回归矩阵](07-regression-matrix.md)
9. [08 · 最终裁决与 V2.2-1 启动](08-final-decision-and-v2.2-1-kickoff.md)
10. [09 · Shadow Mode](09-shadow-mode.md)

## 评审门

V2.2 架构评审已通过，裁决见 [08](08-final-decision-and-v2.2-1-kickoff.md)。

从 V2.2-1 开始，不新增新的自然语言特判到：

- `ConversationService`
- `RuleIntentClassifier`
- `RuleSlotExtractor`
- `mergeSlots`
- `contextualFallback`

允许的工作只有：

- 文档评审与补充。
- 增加现状调查、测试用例清单。
- 标记必须由 V2.2 统一解决的行为缺口。

## 一句话结论

V2.2 的核心不是扩大最近消息窗口，而是把 AgentRuntime 从"带上下文的规则路由器"升级为"先解释本轮语义，再用确定性策略决策"。
