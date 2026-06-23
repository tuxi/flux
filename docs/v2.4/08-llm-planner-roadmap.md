# 08 · LLM Planner Roadmap

## 1. 问题定位

V2.4 第一版不做自由 LLM 工具调用，但必须为未来 LLM Planner 预留接口。

当前阶段裁决：

```text
第一版 Planner = deterministic planner + optional bounded LLM helper
未来 LLM Planner = structured proposal generator
永远不是 bypass contract 的自由执行器
```

LLM 可以帮助：

- 识别复杂自然语言目标。
- 给 Skill candidate 提供辅助信号。
- 生成 creative default candidate。
- 对用户模糊输入生成结构化摘要。

LLM 不允许：

- 发明 catalog 中不存在的 Skill。
- 绕过 Skill Contract。
- 直接创建 PlanCard。
- 直接调用 Workflow / Capability。
- 输出原始 CoT 给用户或持久化。

## 2. 分阶段路线

### Phase 0：No LLM Planner

V2.4 第一版默认：

- Deterministic Skill selection。
- Deterministic InputPlanningPolicy。
- 规则或映射表 creative default。
- Mock Planner 自动化测试。
- 完整 PlannerTrace。

目标是先把 Planner 架构和审计边界跑通。

### Phase 1：LLM Helper

LLM 只作为 helper：

```text
input summary
  -> LLM structured candidate
  -> schema parse
  -> deterministic validator
  -> ActionPlan proposal fields
```

适用场景：

- creative default 候选值。
- 模糊主题摘要。
- Skill candidate hints。

不允许 LLM 直接输出最终 `ActionPlan`。

### Phase 2：LLM ActionPlan Proposer

LLM 可以输出 `ActionPlanProposal`：

```go
type ActionPlanProposal struct {
    Goal          string
    SkillKey      string
    ProposedSlots map[string]any
    Assumptions   []PlannerAssumption
    Confidence    float64
}
```

但 proposal 必须经过：

```text
CompiledSkill exists
  -> Skill selection policy
  -> InputPlanningPolicy
  -> Contract validation
  -> Workflow validator
  -> ActionPlanValidator
```

只有通过后才能成为业务 `ActionPlan`。

### Phase 3：Bounded Tool Planning

未来可以让 LLM 在已注册 capability 空间内提出 action sequence。

第一版不做。进入该阶段前必须有：

- Capability schema。
- Idempotency key。
- Side effect policy。
- Cost guard。
- Max step limit。
- Human confirmation boundary。
- Full trace and rollback story。

### Phase 4：MCP / External Tools

MCP 和动态外部工具发现不是 V2.4 第一版范围。

进入前置条件：

- 内部 Skill / Capability contract 已稳定。
- PlannerTrace 可审计。
- Tool permission model 清楚。
- 用户授权与成本边界清楚。
- 外部工具结果不能直接成为业务事实，仍需 validator / adapter。

## 3. LLM 输入边界

LLM helper 输入必须是最小必要摘要：

```go
type LLMPlannerContext struct {
    UserInputSummary string
    Turn             TurnInterpretation
    Target           TargetResolution
    Operation        OperationIntent
    CandidateSkills  []SkillCandidateSummary
    AllowedFields    []InputFieldSummary
    ExistingSlots    map[string]ValueSummary
}
```

禁止输入：

- 完整 conversation raw log。
- system prompt / developer prompt。
- secrets / callback token。
- 大体积资产原文。
- 不必要的 PII。

## 4. LLM 输出边界

LLM 必须输出 schema JSON：

```go
type LLMPlannerOutput struct {
    Candidates  []LLMSkillCandidateHint
    SlotValues  []LLMSlotValueHint
    Assumptions []LLMAssumptionHint
    Refusal     *LLMRefusalHint
}
```

处理流程：

```text
parse JSON
  -> schema validation
  -> map to known field keys
  -> enum / type validation
  -> confidence threshold
  -> deterministic policy arbitration
```

parse 失败、字段未知、enum 不合法、confidence 低，都必须降级为规则或追问。

## 5. 性能边界

`creative_default` 如果使用 LLM，P99 目标不超过 1 秒。

策略：

- 优先规则表。
- LLM helper 只在规则无法高置信覆盖时调用。
- 设置短 timeout。
- timeout 降级为 `ask_user` 或保守默认。
- 不阻塞已有 deterministic PlanCard 路径。

不得为了“更聪明”让普通 PlanCard 生成显著变慢。

## 6. 安全边界

LLM 输出不可信。

必须防：

- hallucinated skill。
- invalid enum。
- prompt injection。
- 用户诱导绕过 cost confirmation。
- 输出“已确认”但用户没有确认。
- 输出伪 Trace。

规则：

- LLM 不写 DB。
- LLM 不调用 Capability。
- LLM 不决定 confirm boundary。
- LLM 不绕过 `system_injected`。
- LLM 不产生用户可见 CoT。

## 7. Trace 边界

LLM 调用可以记录：

- helper name。
- input summary hash。
- output parse status。
- candidate count。
- latency。
- validation failure code。

不能记录：

- raw reasoning。
- raw prompt。
- raw output。
- raw hidden instructions。
- secrets。

LLM helper 只能进入结构化 `PlannerTrace`。Trace event 记录 parse status、candidate summary、validation code 和 latency；不得记录 raw prompt、raw output、raw reasoning，也不得把这些内容落入 message、DB、ELK 或文件日志。

用户可见摘要只能来自结构化 event：

```text
Observation: used creative default helper
Assumption: recommended bgm_style
Validation: candidate passed enum check
```

## 8. 回归要求

未来接入 LLM 前必须覆盖：

- LLM 输出不存在 Skill 被拒绝。
- LLM 输出 invalid enum 被拒绝。
- LLM 输出 required 缺失被拦截。
- LLM timeout 降级。
- LLM parse failure 降级。
- LLM 试图绕过 cost confirmation 被拦截。
- LLM raw output 不进 message / DB。
- deterministic path 不依赖 LLM 仍可通过。

## 9. 裁决

V2.4 第一版不接自由 LLM Planner。

LLM 只能作为未来可插拔 helper 或 proposal source；所有输出必须经过 deterministic Planner policy、Skill Contract 和 Validator。PlannerTrace 展示结构化结果，不展示 LLM 原始思维链。
