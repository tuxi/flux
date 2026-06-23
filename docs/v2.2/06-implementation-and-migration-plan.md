# 06 · 实施与迁移计划

## 1. 阶段原则

V2.2 必须分阶段实施，不直接大爆炸重写 runtime。

实施顺序：

```text
0. 文档评审
1. Offline TurnInterpreter
2. PendingInteraction 持久化
3. SkillSufficiencyEvaluator
4. DialoguePolicy
5. Runtime 接入
6. 回归矩阵全绿
7. 清理旧分散规则
```

## 2. Phase 0：文档评审

产出：

- 本目录全部文档。
- 行为回归矩阵。
- 需要改动的代码清单。

冻结：

- 不继续在旧 classifier / extractor / fallback 增加新语义特判。

## 3. Phase 1：Offline TurnInterpreter

新增 runtime 内部模型与离线规则解释器：

- `DialogueAct`
- `TurnInterpretation`
- `GoalReference`
- `ModificationIntent`
- `TurnInterpreter`
- `RuleTurnInterpreter`

验收：

- 纯模型编译。
- 无业务行为变化。
- 表驱动单测覆盖 [04](04-rule-interpreter-cases.md)。

## 4. Phase 2：PendingInteraction 持久化

新增：

- domain 字段。
- entity 字段。
- mapping。
- migration / AutoMigrate 兼容。

验收：

- JSON round-trip。
- smalltalk 保留 pending。
- cancel 清 pending。
- pending modification 回答归属正确。

## 5. Phase 3：SkillSufficiencyEvaluator

为 short_drama 建立第一版 story brief 判断。

新增：

- `SufficiencyResult`
- `SkillSufficiencyEvaluator`
- `RuleSkillSufficiencyEvaluator`

足够：

- 包含人物 / 场景 / 事件 / 冲突 / 反转中至少一种明确故事信息。

不足：

- 只有 skill 命令。
- 只有风格。
- 只有时长。
- 只有画幅。
- 只有"做一个短剧"类操作壳。

验收：

- "做一个搞笑短剧" 不创建 Plan。
- "程序员第一天上班误删数据库的搞笑短剧" 创建 Plan。

## 6. Phase 4：DialoguePolicy

把现有 runtime 主流程迁为：

```text
Interpret -> Evaluate Sufficiency -> Policy Decide
```

验收：

- Policy 纯单测覆盖决策表。
- 仍不切换 Runtime 主入口。

## 7. Phase 5：Runtime 接入

Phase 5 必须拆成渐进子阶段，禁止 Big Bang：

```text
5A. DialogueDirective -> service.Decision Builder
5B. Shadow Mode 对比新旧决策
5C. V2.2 主流程集中接管 Respond
5D. 清理旧决策中心
```

最终把现有 runtime 主流程迁为：

```text
Interpret -> Evaluate Sufficiency -> Policy Decide
```

迁移时保留：

- `ConfirmPlan`
- `buildWorkflowInput`
- `buildPlanCard`
- Outbox / Observer 边界

5A 只允许新增 Builder 和离线测试，不允许切换 `AgentRuntime.Respond`。

5B 只允许启用 Shadow Mode：

- 真实路径继续返回 legacy decision。
- Shadow 使用 `ConversationContext` 深拷贝。
- Shadow 输出 `DecisionShape` 与 `ShadowDiffClass`。
- Shadow 错误只记录，不影响用户。
- 不写库、不创建持久 Plan、不写 Outbox、不改变 HTTP / WS 响应。

5C 当前裁决：

- Agent 尚未上线，处于早期开发阶段，不需要继续执行 Shadow 审查或分批灰度切流。
- `AgentRuntime.Respond` 默认由 `TurnInterpreter -> SkillSufficiencyEvaluator -> DialoguePolicy -> DialogueDecisionBuilder` 接管。
- 只保留短期全局开关 `ai_engine.agent_use_legacy_runtime`，用于整体切回 legacy 调试。
- 禁止按场景长期双轨运行。
- 禁止 V2.2 主路径出错后静默回退 legacy。
- `ConversationService` 持久化前必须校验 `AgentState` 不变量。
- turn 返回体中的 `conversation.status` 必须 reload 后与 `agent_state.stage` 投影一致。

验收：

- 现有 `go test ./ai-engine/agent/...` 全绿。
- 新回归矩阵全绿。

## 8. Phase 6：旧规则收敛

迁移完成后：

- `RuleIntentClassifier` 不再承载上下文优先级。
- `RuleSlotExtractor` 不再判断信息充分性。
- `mergeSlots` 只处理已解释后的 merge，不识别用户 act。
- `contextualFallback` 由 Policy 接管。

## 9. 风险

| 风险 | 应对 |
|------|------|
| 行为变化影响旧短剧流程 | 回归矩阵先行，逐项迁移 |
| sufficiency 过严导致多追问 | 第一版只对明显壳请求拦截 |
| pending JSON 迁移影响旧会话 | null 兼容 + legacy derive |
| 规则膨胀 | 规则只产 Interpretation，Policy 决策集中 |
| 未来多 Skill 冲突 | `SkillSufficiencyEvaluator` 按 skill 插拔 |

## 10. 完成定义

V2.2 完成条件：

- Runtime 主入口使用 `TurnInterpreter + DialoguePolicy`。
- `PendingInteraction` 持久化。
- "改一下" 不污染 prompt。
- 模糊短剧请求追问 story brief。
- smalltalk 不打断 pending。
- cancel 清 pending。
- fallback 不复读。
- agent 包测试全绿。
