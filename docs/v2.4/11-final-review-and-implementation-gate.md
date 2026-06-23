# 11 · Final Review and Implementation Gate

## 1. 总体裁决

V2.4 文档主线冻结。

V2.4 的核心定义：

```text
V2.4 = Agent Planner + Planner Trace
```

它不是“更会聊天”，不是原始 Thought streaming，也不是自由工具调用。它是在 V2.1 / V2.2 / V2.3 之上新增正式 Planner 层：

```text
V2.1 Skill Contract
  -> V2.2 Conversation Semantics
  -> V2.3 Object Semantics & Capability Runtime
  -> V2.4 AgentPlanner / ActionPlan / PlannerTrace
```

V2.4 负责把现有语义输入组织成可校验、可解释、可审计的 `ActionPlan`，并通过 `PlannerTrace` 转成用户可见 Planning Summary。

## 2. 冻结文档

本轮冻结以下文档：

| 文档 | 状态 |
| --- | --- |
| [README.md](README.md) | frozen |
| [00-agent-planner-overview.md](00-agent-planner-overview.md) | frozen |
| [01-planner-input-output.md](01-planner-input-output.md) | frozen |
| [02-input-planning-policy.md](02-input-planning-policy.md) | frozen |
| [03-skill-selection-and-catalog-observation.md](03-skill-selection-and-catalog-observation.md) | frozen |
| [04-assumptions-and-defaults.md](04-assumptions-and-defaults.md) | frozen |
| [05-planner-trace-and-visible-thinking.md](05-planner-trace-and-visible-thinking.md) | frozen |
| [06-plan-card-generation.md](06-plan-card-generation.md) | frozen |
| [07-react-loop-boundary.md](07-react-loop-boundary.md) | frozen |
| [08-llm-planner-roadmap.md](08-llm-planner-roadmap.md) | frozen |
| [09-implementation-and-migration-plan.md](09-implementation-and-migration-plan.md) | frozen |
| [10-regression-matrix.md](10-regression-matrix.md) | frozen |

冻结后，除评审发现的阻断问题外，不再扩展 V2.4 第一版范围。

## 3. 不可变红线

实施阶段必须遵守：

- 不展示原始 Chain-of-Thought。
- 不 streaming 原始 Thought。
- 不持久化 raw prompt / raw output / raw reasoning。
- LLM helper 只记录结构化 Trace。
- `PlanCard` 不是 Planner 内部模型。
- `ActionPlan` 必须通过 Skill Contract / PlanningPolicy / Validator。
- `confirm_plan` 不信任客户端 PlanCard payload。
- `system_injected` 只由系统注入，Planner 只能观察。
- blocking context 下新 Skill 命中必须 `goal_switch_confirmation`。
- Planner 错误不静默回退 legacy。
- `short_drama` 闭环零回退。

## 4. PR-A 实施入口

PR-A 只允许实现纯 domain model / contract，不接 runtime 行为。

允许：

- 新增 `PlannerInput`。
- 新增 `ActionPlan`。
- 新增 `MissingInput`。
- 新增 `PlannerAssumption`。
- 新增 `PlannerTrace`。
- 新增 `SkillSelectionResult`。
- 新增 `InputPlanningPolicy`。
- 增加 JSON roundtrip / fixture tests。

不允许：

- 改 `AgentRuntime` 主路径。
- 改 `ConversationService` 行为。
- 改 PlanCard 生成逻辑。
- 改 task / outbox / observer 行为。
- 接 LLM。
- 接 WS PlannerActivity。

PR-A 的目标是证明模型可以表达 V2.4 文档，不改变用户结果。

## 5. PR-A 验收标准

必须满足：

- Domain models 与文档字段一致。
- 模型支持 JSON serialize / deserialize。
- `PlannerAssumption` 有稳定 `ID`。
- `PlannerTraceEvent` 有结构化 type / source / level。
- `InputPlanningPolicyKind` 覆盖五类策略。
- `SkillSelectionResult` 保留 candidates。
- `system_injected` 不进入可规划字段集合。
- 单测不依赖真实 DB。

PR-A 不要求：

- 真正选择 Skill。
- 真正生成 PlanCard。
- 真正存储 Trace。
- 真正切 runtime。

## 6. 后续实施门

PR-A 通过后，按 [09](09-implementation-and-migration-plan.md) 顺序推进：

```text
PR-A Domain Model / Contract
PR-B Compiled PlanningPolicy
PR-C Deterministic Planner Offline
PR-D PlannerTrace Store / Summary
PR-E PlanCard Adapter
PR-F Runtime Shadow
PR-G Controlled Cutover
```

不得跳过 Shadow 直接切主路径。

## 7. 回归门

进入 PR-F / PR-G 前，必须完成 [10](10-regression-matrix.md)：

- 所有 P0 case 必须全绿。
- `short_drama` P0 case 必须全绿。
- PlannerActivity failed 终态必须覆盖，失败后不能卡在 `planning`。
- raw CoT / raw LLM output 不落库、不推送。
- client tamper PlanCard payload 不能影响 confirm。

P1 case 原则上应在主路径切换前完成。P2 case 可作为后续增强，但不能阻塞 PR-A。

## 8. 最终裁决

V2.4 文档评审通过，可以进入 PR-A。

实施从纯模型开始，先建立可测试、可序列化、可审计的数据结构；在 PR-A 合并前，不进入 runtime 主路径改造。
