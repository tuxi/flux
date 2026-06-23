# 08 · 最终裁决与 V2.2-1 启动

## 1. 评审结论

V2.2 架构评审通过。

正式接受以下架构方向：

```text
ConversationContext
  -> TurnInterpreter
  -> TurnInterpretation
  -> SkillSufficiencyEvaluator
  -> DialoguePolicy
  -> Decision
```

V2.2 不是继续修复若干对话 bug，而是 AgentRuntime 的会话语义升级。后续所有"用户这句话在当前上下文里是什么意思"的问题，都必须进入 V2.2 解释层，而不是继续散落在旧规则路径里。

## 2. 冻结旧语义路径

从本裁决生效起，禁止继续在以下位置堆积新的自然语言语义特判：

- `ConversationService`
- `RuleIntentClassifier`
- `RuleSlotExtractor`
- `mergeSlots`
- `contextualFallback`

允许的例外：

- 删除或迁移旧逻辑。
- 为保持编译或测试修复非语义 bug。
- 在 V2.2 切换完成前，保留旧路径作为兼容运行路径。

## 3. 立即启动：V2.2-1 Offline TurnInterpreter

V2.2-1 目标：

```text
在不切换 Runtime 主流程、不改持久化、不创建 Plan 的前提下，
先实现离线 TurnInterpreter，产出可测试的 TurnInterpretation。
```

范围：

- 新增 `DialogueAct` / `TurnInterpretation` / `GoalReference` / `ModificationIntent`。
- 新增 `TurnInterpreter` 接口。
- 新增 `RuleTurnInterpreter`。
- 增加表驱动单测，覆盖 V2.2 回归矩阵中解释层可判定的 cases。

明确不做：

- 不接 `AgentRuntime.Respond`。
- 不新增 `PendingInteraction` 持久化字段。
- 不实现 `DialoguePolicy`。
- 不改变线上会话行为。
- 不接 LLM fallback。

## 4. V2.2-1 验收标准

必须满足：

- `RuleTurnInterpreter` 不访问 repository。
- `RuleTurnInterpreter` 不创建 Plan、不返回 `service.Decision`。
- 单测覆盖：
  - 模糊短剧请求。
  - 上一轮建议后的"好的"。
  - pending story brief 下的"都市爱情"。
  - smalltalk / cancel。
  - "改一下" vs "改成横屏" vs "再来一版"。
- `go test ./ai-engine/agent/...` 通过。

## 4.1 V2.2-1 实施验收结果

状态：已完成。

已落地：

- `ai-engine/agent/runtime/turn_interpreter.go`
  - `DialogueAct`
  - `GoalReference`
  - `ModificationIntent`
  - `TurnInterpretation`
  - `TurnInterpreter`
- `ai-engine/agent/runtime/rule_turn_interpreter.go`
  - 离线 `RuleTurnInterpreter`
  - 不访问 repository
  - 不创建 Plan
  - 不返回 `service.Decision`
  - 不接入 `AgentRuntime.Respond`
- `ai-engine/agent/runtime/turn_interpreter_test.go`
  - 模糊短剧请求
  - style/duration-only 短剧请求
  - story brief present
  - pending story brief 下的"都市爱情"
  - smalltalk / cancel
  - 最近 Agent 建议后的"好的"
  - "改一下" / "再来一版" / "改成横屏"

验收命令：

```bash
go test ./ai-engine/agent/runtime -run 'TestRuleTurnInterpreter'
go test ./ai-engine/agent/...
```

结果：通过。

## 5. 后续阶段门

V2.2-1 验收通过后，才能进入：

1. V2.2-2 `PendingInteraction` 持久化。
2. V2.2-3 `SkillSufficiencyEvaluator`。
3. V2.2-4 `DialoguePolicy`。
4. V2.2-5 Runtime 主流程切换。
5. V2.2-6 清理旧分散语义规则。

任何阶段不得绕过 `TurnInterpretation` 直接在旧路径新增补丁。

## 5.1 V2.2-2 实施验收结果

状态：已完成。

已落地：

- `ai-engine/agent/domain/types.go`
  - `PendingInteractionKind`
  - `PendingInteraction`
  - `AgentState.PendingInteraction`
- `ai-engine/agent/entity/conversation.go`
  - `agent_states.pending_interaction` JSONB 字段
- `ai-engine/agent/repository/query/mapping.go`
  - typed JSON round-trip mapping
- `ai-engine/agent/repository/query/agent_state.go`
  - CAS 更新支持写入和清空 pending interaction
- `ai-engine/agent/repository/query/data_layer_test.go`
  - AutoMigrate 字段存在性
  - Init round-trip
  - CAS update and clear

本阶段边界：

- 未暴露 API DTO。
- 未切换 `AgentRuntime.Respond`。
- 未实现 `DialoguePolicy`。
- 未改旧语义路径。

验收命令：

```bash
go test ./ai-engine/agent/repository/query -run 'TestAgentState'
go test ./ai-engine/agent/...
```

结果：通过。

下一阶段门：V2.2-3 `SkillSufficiencyEvaluator`。在 V2.2-3 与 V2.2-4 完成前，不进入 Runtime 主流程切换。

## 5.2 V2.2-3 实施验收结果

状态：已完成。

已落地：

- `ai-engine/agent/runtime/sufficiency.go`
  - `SufficiencyResult`
  - `SkillSufficiencyEvaluator`
  - `RuleSkillSufficiencyEvaluator`
  - short_drama story brief 充分性判断
- `ai-engine/agent/runtime/sufficiency_test.go`
  - 模糊短剧请求不足
  - style-only 请求不足
  - duration + genre-only 请求不足
  - 带人物 / 场景 / 事件的 story brief 足够
  - pending story brief 回答足够
  - request modification 不足
  - regenerate / concrete modification 足够

本阶段边界：

- 未切换 `AgentRuntime.Respond`。
- 未实现 `DialoguePolicy`。
- 未创建 Plan。
- 未改旧语义路径。

验收命令：

```bash
go test ./ai-engine/agent/runtime -run 'TestRuleSkillSufficiency'
go test ./ai-engine/agent/...
```

结果：通过。

下一阶段门：V2.2-4 `DialoguePolicy` 离线决策表。在 V2.2-4 完成并验收前，不进入 Runtime 主流程切换。

## 5.3 V2.2-4 实施验收结果

状态：已完成。

已落地：

- `ai-engine/agent/runtime/dialogue_policy.go`
  - `DialogueDirectiveKind`
  - `DialogueDirective`
  - `MissingRequirement`
  - `DialoguePolicyInput`
  - `DialoguePolicy`
  - `PolicyError`
  - `RuleDialoguePolicy`
- `ai-engine/agent/runtime/dialogue_policy_test.go`
  - `start_goal + insufficient -> clarify + collect_story_brief`
  - `start_goal + sufficient -> create_plan`
  - `request_modification + insufficient -> clarify + collect_modification`
  - `provide_modification + sufficient -> modify_plan`
  - `answer_question + collect_modification + sufficient -> modify_plan`
  - `answer_question + collect_modification + insufficient -> clarify + preserve pending`
  - `regenerate -> regenerate_plan`
  - `smalltalk -> reply_smalltalk + preserve pending`
  - `cancel collect_story_brief -> idle`
  - `cancel collect_modification -> completed`
  - invalid combinations return typed policy errors

本阶段边界：

- 未切换 `AgentRuntime.Respond`。
- 未返回 `service.Decision`。
- 未创建 Plan / Message / Launch。
- 未访问 repository。
- 未改旧语义路径。

验收命令：

```bash
go test ./ai-engine/agent/runtime -run 'TestRuleDialoguePolicy'
go test ./ai-engine/agent/...
```

结果：通过。

下一阶段门：V2.2-5 Runtime 接入与 Directive -> `service.Decision` Builder。V2.2-5 才允许开始迁移真实运行路径，并逐步停用旧的 `iterate` / `contextualFallback` / stage 分支决策。

## 5.4 V2.2-5A 实施验收结果

状态：已完成。

已落地：

- `ai-engine/agent/runtime/dialogue_decision_builder.go`
  - `DialogueDecisionBuilder`
  - `AgentRuntime.Build(...)`
  - `DirectiveClarify -> service.Decision`
  - `DirectiveCreatePlan -> NewPlan + PlanCard`
  - `DirectiveModifyPlan -> merged slots + NewPlan + PlanCard`
  - `DirectiveRegeneratePlan -> copy current plan/state slots + NewPlan + PlanCard`
  - `DirectiveReplySmalltalk -> text reply, preserve pending when requested`
  - `DirectiveCancelPending -> clear pending and restore Policy stage`
- `ai-engine/agent/runtime/dialogue_decision_builder_test.go`
  - clarify preserves candidate slots
  - create_plan merges prior collected slots
  - modify_plan changes aspect ratio without polluting prompt
  - regenerate keeps current prompt and parameters
  - smalltalk preserves pending interaction
  - cancel clears pending and restores completed
- `ai-engine/agent/service/conversation_service.go`
  - after appending clarify, fills `PendingInteraction.PromptMessageID` with the real persisted message ID
- `ai-engine/agent/service/conversation_service_test.go`
  - verifies `PendingMessageID == PendingInteraction.PromptMessageID`

本阶段边界：

- 未切换 `AgentRuntime.Respond`。
- 未启用 Shadow Mode。
- 未按场景切流。
- 未删除旧决策路径。
- Builder 不重新解释用户文本、不判断 sufficiency、不访问 repository。

验收命令：

```bash
go test ./ai-engine/agent/runtime -run 'TestDialogueDecisionBuilder'
go test ./ai-engine/agent/service -run 'TestAdvanceTurnFillsPendingInteractionPromptMessageID'
go test ./ai-engine/agent/...
```

结果：通过。

下一阶段门：V2.2-5B Shadow Mode。Shadow Mode 只记录新旧决策差异，继续返回 legacy decision，不改变真实用户结果。

## 5.5 V2.2-5B 实施验收结果

状态：已完成。

已落地：

- `ai-engine/agent/runtime/shadow.go`
  - `ShadowConfig`
  - `ShadowMode`
  - `ShadowRecorder`
  - `ShadowReport`
  - `DecisionShape`
  - `ShadowDiffClass`
  - `NormalizeDecisionShape(...)`
  - `NormalizeDirectiveShape(...)`
  - `ClassifyShadowDiff(...)`
  - `AgentRuntime.EvaluateShadow(...)`
- `ai-engine/agent/runtime/runtime.go`
  - `AgentRuntime.Respond(...)` 仍然先执行 legacy 决策。
  - legacy 成功后按配置旁路运行 Shadow。
  - Shadow 记录不影响返回给用户的 legacy decision。
- `ai-engine/agent/runtime/shadow_test.go`
  - Shadow 开启与关闭不改变 legacy 返回。
  - Shadow 错误不影响用户。
  - Shadow 前后 `ConversationContext` / `AgentState` / `Plan.Slots` 不变。
  - 已知预期改进分类正确。
  - regenerate / concrete modification 语义等价分类正确。
  - policy unsupported 与 builder/interpreter error 分类正确。
- `ai-engine/docs/v2.2/09-shadow-mode.md`
  - 固化 Shadow Mode 契约、隐私边界、延迟指标、diff class 与 5C 解锁条件。

本阶段边界：

- 未把 V2.2 decision 返回给用户。
- 未写 `AgentState.PendingInteraction`。
- 未创建持久 Plan。
- 未写 Outbox。
- 未切换任何 DialogueAct 的真实路径。
- 未删除 legacy 方法。
- 未接 LLM。

验收命令：

```bash
GOCACHE=/private/tmp/dream-ai-go-cache go test ./ai-engine/agent/runtime
GOCACHE=/private/tmp/dream-ai-go-cache go test ./ai-engine/agent/...
```

结果：通过。

下一阶段门：V2.2-5C Runtime 主流程接管。当前 Agent 尚未上线，处于早期开发阶段，不再要求 Shadow 审查或分批灰度切流。

## 5.6 V2.2-5C 实施验收结果

状态：已完成。

已落地：

- `ai-engine/agent/runtime/runtime.go`
  - `AgentRuntime.Respond(...)` 默认切到 V2.2 主流程。
  - 主流程为 `TurnInterpreter -> SkillSufficiencyEvaluator -> DialoguePolicy -> DialogueDecisionBuilder`。
  - 新路径出错直接返回错误，不静默回退 legacy。
  - `SetUseLegacyDecision(true)` 仅作为短期全局调试开关。
- `ai-engine/agent/runtime/dialogue_policy.go`
  - `ActUnknown -> reply_text`，由 V2.2 Policy 自己处理兜底引导。
  - pending 下 `ActConfirm` 不吞掉 pending，而是轻提示继续补充。
- `ai-engine/agent/runtime/dialogue_decision_builder.go`
  - `DirectiveReplyText` 转换为文本回复。
  - V2.2 文本回复支持 pending anchor 与非复读兜底。
- `ai-engine/agent/domain/state_invariant.go`
  - `ValidateAgentState(...)` 统一校验状态不变量。
- `ai-engine/agent/runtime/state_invariant.go`
  - runtime 出口校验 `Decision.NextState` 与 Plan / Launch 的关系。
- `ai-engine/agent/service/conversation_service.go`
  - 持久化前校验最终 `AgentState`。
  - `PostMessage` / signal 迭代 / confirm 后 reload conversation，修复响应中的 `conversation.status` 与 `agent_state.stage` 不一致。
- `config/config.go`
  - 新增 `ai_engine.agent_use_legacy_runtime`。

集中回归：

- "你好啊，今天" -> "我想做一个1分钟的" -> "好的"
- "帮我做一个短剧" -> "你是谁" -> "都市爱情"
- "帮我做一个一分钟短剧" -> "都市爱情"
- "改一下" -> clarify collect_modification
- "再来一版" -> regenerate plan
- "算了" -> clear pending
- `conversation.status == MapStageToStatus(agent_state.stage)` in returned turn result

本阶段边界：

- Shadow 保留但默认关闭。
- legacy 只允许整体调试切换，不做按场景 fallback。
- 旧 `iterate` / `contextualFallback` / stage 分支真实路径已被停用；后续 5D 再清理代码。

验收命令：

```bash
GOCACHE=/private/tmp/dream-ai-go-cache go test ./ai-engine/agent/domain ./ai-engine/agent/runtime ./ai-engine/agent/service
GOCACHE=/private/tmp/dream-ai-go-cache go test ./ai-engine/agent/... ./ai-engine/server ./config
```

结果：通过。

## 6. 裁决摘要

V2.2 的第一块砖不是更聪明的回复文案，而是可被测试、可被解释、可被未来 LLM fallback 复用的统一回合解释模型。
