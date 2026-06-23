# 09 · Implementation and Migration Plan

## 1. 实施原则

V2.4 必须分阶段落地，不能大爆炸重写 `AgentRuntime`。

原则：

- 文档评审通过前不写业务实现。
- 先离线模型和 Mock Planner，再接 runtime。
- 先只读观察，再改变用户可见结果。
- 先保护 `short_drama` 零回退，再扩展新 Skill。
- Planner 主路径失败不静默回退 legacy。

## 2. 建议 PR 切分

### PR-A：Domain Model / Contract

新增纯 domain 模型：

- `PlannerInput`
- `ActionPlan`
- `MissingInput`
- `PlannerAssumption`
- `PlannerTrace`
- `SkillSelectionResult`
- `InputPlanningPolicy`

验收：

- 无 runtime 行为变化。
- 模型可 JSON roundtrip。
- 文档中的示例可表达。

### PR-B：Compiled PlanningPolicy

在 V2.1 `CompiledSkill` 上补 planning policy 编译。

验收：

- planning manifest 解析。
- required field 无 policy 时使用 conservative `ask_user` 并 auditor warning。
- 老 Skill 缺 planning policy 时不直接 fail。第一版按 conservative `ask_user` 补齐缺参策略并记录 warning，避免老 Skill 因尚未补 manifest planning 段而整体不可用。
- policy field 不存在时编译失败或 warning 分级。
- `system_injected` 不进入 Planner 可规划字段。

### PR-C：Deterministic Planner Offline

实现离线 Planner，不接真实 runtime。

验收：

- Mock `PlannerInput` -> `ActionPlan`。
- Skill selection 候选集可解释。
- InputPlanningPolicy 覆盖 required。
- Assumption / override 可测试。
- 不写 DB、不发 WS、不创建 Plan。

### PR-D：PlannerTrace Store / Summary

实现 Trace 结构、摘要生成和存储接口。

验收：

- Trace 可按 `trace_id` 查询。
- PlannerActivity summary 可由 Trace 生成。
- payload 脱敏白名单/黑名单测试。
- 原始 CoT 无落库入口。

### PR-E：PlanCard Adapter

实现 `ActionPlan -> PlanCard` adapter。

验收：

- PlanCard 只读取已校验 ActionPlan。
- confirm_plan 不信任客户端 payload。
- version pin / contract hash 校验。
- editable 为 Agent 修改边界，不要求完整表单。

### PR-F：Runtime Shadow

在真实 `AgentRuntime` 中旁路执行 Planner，但不改变用户结果。

验收：

- legacy decision 与 Planner decision diff 记录。
- Shadow 输出必须有简单可查入口，例如本地表/文件/调试 API 中的 `conversation_id`、`message_id`、legacy decision summary、planner decision summary、diff category、trace_id；不能只依赖复杂外部日志链路。
- 不影响真实用户响应。
- 重点覆盖 `short_drama` 创建、追问、确认、review 修改、取消、再来一版。

### PR-G：Controlled Cutover

按场景切换：

```text
new start_goal simple skills
  -> answer pending
  -> PlanCard generation
  -> review modify by fork
```

验收：

- 每步有 feature flag。
- failure 显式 error / ask_user，不静默回 legacy。
- 回滚开关只能整体回旧路径用于紧急止血，不能长期双轨。

## 3. 数据与 Migration

可能新增：

- `planner_traces` 独立表。
- Plan 上固定 `action_plan_json` 或等价快照。
- Plan 上固定 `tool_mode_version_id` / `contract_schema_hash`。
- message content 增加 `planner_activity` kind。

第一版兼容策略：

- 老 Plan 没有 `trace_id` 时，不能展开 PlannerTrace。
- 老 Plan 没有 `action_plan_json` 时，继续走旧 confirm 兼容逻辑，但新 Plan 必须写。
- 老 message 不迁移成 PlannerActivity。
- `short_drama` 老任务不补 Trace。

不做大规模历史 migration。

## 4. Runtime 接入点

目标链路：

```text
ConversationService
  -> load ConversationContext
  -> AgentRuntime.Respond
  -> TurnInterpreter
  -> ActiveObjectResolver
  -> TargetResolver
  -> OperationInterpreter
  -> SkillCatalogSnapshot
  -> AgentMemorySnapshot
  -> AgentPlanner
  -> ActionPlanValidator
  -> DecisionBuilder
```

职责：

- `ConversationService` 不理解自然语言。
- `AgentPlanner` 不写 DB、不发 WS、不创建 task。
- `DecisionBuilder` 负责持久化 decision 形态。
- `OutboxWorker` 仍负责 task launch。
- `Observer` 仍负责 engine event -> card / activity。

## 5. Feature Flags

建议开关：

| Flag | 含义 |
| --- | --- |
| `agent_planner_enabled` | 是否启用 V2.4 Planner 主路径 |
| `agent_planner_shadow` | 是否记录 Planner shadow diff |
| `planner_activity_enabled` | 是否发送 PlannerActivity |
| `planner_trace_store_enabled` | 是否保存完整 Trace |
| `action_plan_plan_card_enabled` | 是否用 ActionPlan adapter 生成 PlanCard |
| `llm_planner_helper_enabled` | 未来 LLM helper，默认 false |

开关不能导致长期双轨语义。切换期间必须有明确裁撤计划。

## 6. 失败处理

失败分类：

| 失败 | 处理 |
| --- | --- |
| Skill 不可用 | `refuse_or_explain` 或 ask_user 换能力 |
| required 无法满足 | `ask_user` |
| validator 拒绝 | 移除候选值并追问 |
| Trace 存储失败 | 可继续用户结果，但记录告警；不能影响 Plan 正确性 |
| PlanCard adapter 失败 | 不生成 PlanCard，返回可解释错误 |
| version pin 过期 | 提示重新生成 |

禁止：

- Planner 错误后静默走 legacy 短剧硬编码。
- validator fail 后仍生成 PlanCard。
- Trace fail 后编造 Trace。

## 7. short_drama 零回退门

切主路径前必须通过：

- 创建短剧 PlanCard。
- 信息不足追问。
- 回答追问后 PlanCard。
- confirm_plan -> outbox -> task。
- prompt ReviewCard。
- review 修改 by fork。
- cancel old task + await binding cleanup。
- ResultCard 再来一版。

任何一项失败，不能切换主路径。

## 8. 实施顺序裁决

```text
1. Domain models
2. PlanningPolicy compiler
3. Offline deterministic Planner
4. PlannerTrace storage and summary
5. ActionPlan -> PlanCard adapter
6. Runtime shadow
7. Controlled cutover
8. Cleanup legacy duplication
```

## 9. 非目标

本实施计划不包含：

- LLM自由工具调用。
- MCP Server。
- 外部工具发现。
- 长期用户偏好系统。
- 多 Skill 自动组合。
- 重写 Workflow Engine。

## 10. 裁决

V2.4 实施必须先证明离线 Planner 正确，再接 runtime；先 shadow，再切主路径；先保证 `short_drama` 零回退，再扩展更多 Skill。
