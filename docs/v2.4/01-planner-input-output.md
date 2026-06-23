# 01 · Planner Input / Output

## 1. 核心原则

`AgentPlanner` 不重复做 V2.1~V2.3 已经完成的判断。

它的输入必须来自已有语义层、契约层、对象层和记忆层：

```text
V2.1 CompiledSkill / Skill Contract
V2.2 ConversationContext / TurnInterpretation
V2.3 TargetResolution / OperationIntent / ActiveObject
AgentState / CurrentPlan / TaskLinks / conversation history
```

它的输出也不能直接等于 UI 卡片或 Engine 调用：

```text
AgentPlanner
  -> ActionPlan
  -> Contract / Policy / Validator checks
  -> Decision adapter
  -> PlanCard / ask_user / CapabilityCall / refuse_or_explain / cancel
```

`ActionPlan` 是 Planner 的业务语义输出。`PlanCard` 是后续 adapter 根据 `ActionPlan` 生成的用户确认界面。

## 2. PlannerInput

建议模型：

```go
type PlannerInput struct {
    Context        ConversationContext
    Turn           TurnInterpretation
    Target         TargetResolution
    Operation      OperationIntent
    SkillCatalog   []CompiledSkill
    ActiveObjects  []ActiveObject
    Memory         AgentMemorySnapshot
}
```

字段来源：

| 字段 | 来源 | 说明 |
| --- | --- | --- |
| `Context` | V2.2 `ConversationContext` | 当前 `AgentState`、最近消息、当前输入、当前 Plan |
| `Turn` | V2.2 `TurnInterpreter` | 当前回合语义，如 start_goal / answer_question / request_modification / cancel |
| `Target` | V2.3 `TargetResolver` | 用户动作指向的对象，如 current plan / review artifact / result |
| `Operation` | V2.3 `OperationInterpreter` | 对目标对象要做的操作，如 revise / confirm / regenerate / cancel |
| `SkillCatalog` | V2.1 compiled Skill registry | 当前可被 Agent 观察和选择的 Skill 契约快照 |
| `ActiveObjects` | V2.3 `ActiveObjectResolver` | 当前会话中可操作对象集合 |
| `Memory` | Agent state snapshot | 已收集 slots、pending、task links、history 摘要、当前 plan 等 |

`PlannerInput` 必须是结构化快照，不允许 Planner 在内部重新查散落状态后得出另一套事实。

## 3. runtime 拼装顺序

推荐主链路：

```text
ConversationService
  -> load ConversationContext
  -> AgentRuntime.Respond
  -> TurnInterpreter.Interpret
  -> ActiveObjectResolver.Resolve
  -> TargetResolver.Resolve
  -> OperationInterpreter.Interpret
  -> SkillRegistry.CompiledCatalogSnapshot
  -> AgentMemorySnapshotBuilder.Build
  -> PlannerInput
  -> AgentPlanner.Plan
  -> ActionPlan + PlannerTrace
  -> ActionPlanValidator
  -> DecisionBuilder
```

注意：

- `ConversationService` 仍不理解自然语言。
- `TurnInterpreter` 仍只解释当前回合语义，不创建 Plan。
- `TargetResolver` 和 `OperationInterpreter` 仍只解析对象与操作，不决定默认值。
- `AgentPlanner` 不直接写 DB，不直接发 WS，不直接创建 Task。
- `DecisionBuilder` 负责把已校验的 `ActionPlan` 转成现有可持久化 Decision。

## 4. SkillCatalog 观察

Planner 观察的是 `CompiledSkill`，不是手写自然语言 Skill 列表。

`CompiledSkill` 至少需要提供：

```go
type CompiledSkill struct {
    SkillKey                 string
    RouteKey                 string
    ModeKey                  string
    ToolModeVersionID        int64
    ContractSchemaHash       string
    InputContract            InputContract
    PlanningPolicy           PlanningPolicy
    CapabilityBindings       []CapabilityBinding
    Examples                 []SkillExample
    Status                   SkillStatus
}
```

其中：

- `ToolModeVersionID` 与 `ContractSchemaHash` 继承 V2.1 版本固定要求。
- `InputContract` 决定硬约束。
- `PlanningPolicy` 决定缺参如何处理。
- `CapabilityBindings` 决定哪些 V2.3 Capability 可被安全调用。
- `Status` 为 disabled 的 Skill 不进入候选集，但可产生可解释 observation。

`SkillCatalog` 第一版建议启动时编译加载，运行时取内存快照。后续如果支持 admin 发布 active version 后热更新，必须触发重新编译，并保证已生成 `ActionPlan` 的版本固定与过期检测。

## 5. AgentMemorySnapshot

建议模型：

```go
type AgentMemorySnapshot struct {
    State              *AgentState
    CurrentPlan        *PlanSnapshot
    Pending            *PendingInteraction
    TaskLinks          []TaskLinkSnapshot
    RecentMessages     []MessageSummary
    CollectedSlots     map[string]any
    UserOverrides      map[string]any
    LastPlannerTraceID *string
}
```

设计目标：

- 给 Planner 一个稳定、可测试的记忆输入。
- 区分用户已确认值、Planner 推荐值和用户覆盖值。
- 避免 Planner 直接扫描原始消息推断全部状态。
- 支持用户否定默认值后的 assumption override。

`UserOverrides` 用于记录用户明确否定或修改过的推荐值。例如 Planner 曾推荐 `bgm_style=synthwave_electronic_bass`，用户说“不要电子乐，换成钢琴”，下一轮 Planner 必须把该字段视为用户覆盖，而不是再次自动推荐 synthwave。

## 6. ActionPlan

建议模型：

```go
type ActionPlan struct {
    Goal          string
    SkillKey      string
    Target        *ObjectRef
    ProposedSlots map[string]any
    Missing       []MissingInput
    Assumptions   []PlannerAssumption
    NextAction    PlannerNextAction
    Trace         PlannerTrace
    Confidence    float64
}
```

`ActionPlan` 是 Planner 的核心输出。

字段含义：

| 字段 | 含义 |
| --- | --- |
| `Goal` | 结构化目标，如 `create_video` / `revise_review` / `regenerate_result` |
| `SkillKey` | 选中的 Skill；非 Skill 行为可为空或使用内部 action key |
| `Target` | 当前动作目标对象，来自 V2.3 |
| `ProposedSlots` | 已收集、默认合并、推断推荐后的候选输入 |
| `Missing` | 仍缺失且不能安全自动补齐的输入 |
| `Assumptions` | Planner 推荐默认值或推断值的可审计记录 |
| `NextAction` | 下一步动作 |
| `Trace` | 本次规划 Trace |
| `Confidence` | 整体规划置信度 |

`NextAction` 第一版枚举：

```go
type PlannerNextAction string

const (
    PlannerNextAskUser          PlannerNextAction = "ask_user"
    PlannerNextCreatePlanCard   PlannerNextAction = "create_plan_card"
    PlannerNextInvokeCapability PlannerNextAction = "invoke_capability"
    PlannerNextRefuseOrExplain  PlannerNextAction = "refuse_or_explain"
    PlannerNextCancel           PlannerNextAction = "cancel"
)
```

## 7. MissingInput

建议模型：

```go
type MissingInput struct {
    Field          string
    Required       bool
    Policy         InputPlanningPolicyKind
    Reason         string
    AskPrompt      string
    CandidateValue any
}
```

缺参处理由 `InputPlanningPolicy` 决定，而不是由 PlanCard builder 或旧 sufficiency evaluator 各自判断。

策略分类：

| Policy | 处理 |
| --- | --- |
| `ask_user` | 用户必须提供，Agent 不能猜 |
| `creative_default` | 可根据主题/风格/场景推断，必须形成 `PlannerAssumption` 并让用户确认 |
| `system_default` | 技术默认值，可直接使用，但应展示在 PlanCard |
| `cost_affecting_default` | 影响费用的默认值，可推荐，但必须在 PlanCard 明示 |
| `requires_asset` | 必须依赖用户上传或选择素材 |

关键规则：

```text
Skill Contract 是法律。
Planning Policy 是执行策略。
```

如果 Skill Contract 规定字段 required，Planner 不能忽略。Planner 只能决定该 required 字段如何被满足：用户提供、系统默认、创意默认、成本默认、素材要求。最终所有输出必须通过 Contract Validator。

## 8. PlannerAssumption

建议模型：

```go
type PlannerAssumption struct {
    Field       string  `json:"field"`
    Value       any     `json:"value"`
    Confidence  float64 `json:"confidence"`
    Reason      string  `json:"reason"`
    Confirmable bool    `json:"confirmable"`
}
```

示例：

```json
{
  "field": "bgm_style",
  "value": "synthwave_electronic_bass",
  "confidence": 0.82,
  "reason": "赛博朋克视觉风格通常适合电子合成器和低频鼓点",
  "confirmable": true
}
```

意义：

- 不是偷偷帮用户填默认值。
- 而是明确记录 Planner 基于什么理由推荐了什么。
- PlanCard 必须能展示 confirmable assumption。
- 用户否定时，下一轮必须写入 `UserOverrides` 或等价结构，避免重复推荐。

第一版建议：

- `creative_default` 必须 `Confirmable=true`。
- `cost_affecting_default` 必须 `Confirmable=true`，并在 PlanCard 中明示费用/耗时影响。
- `system_default` 可以 `Confirmable=false`，但仍应展示最终有效值。
- 低置信 assumption 不应直接进入 PlanCard，可降级为 `ask_user`。

具体阈值在 `04-assumptions-and-defaults.md` 裁决。

## 9. PlannerTrace

建议模型：

```go
type PlannerTrace struct {
    TraceID        string
    ConversationID int64
    PlanID         *int64
    Goal           string
    SkillKey       string
    Observations   []PlannerObservation
    Assumptions    []PlannerAssumption
    Decisions      []PlannerDecision
    Validations    []PlannerValidation
    CreatedAt      time.Time
}
```

Trace 类型必须强区分，禁止写成一堆自然语言字符串：

| 类型 | 含义 | 示例 |
| --- | --- | --- |
| `Observation` | 事实观察 | 识别到用户目标、匹配到 Skill、检测到缺失字段 |
| `Assumption` | 推断假设 | 根据赛博朋克推荐 synthwave 音乐 |
| `Decision` | 规划决策 | 生成 PlanCard，等待用户确认 |
| `Action` | 准备执行的动作 | `create_plan_card` / `ask_user` / `invoke_capability` |
| `Validation` | 契约校验结果 | `ActionPlan` 通过 Skill Contract 校验 |

用户可见 Planning Summary 由 `PlannerTrace` 转换而来。完整 Trace 不直接写入普通 message 文本。

## 10. ActionPlan 校验链路

Planner 输出 `ActionPlan` 后，必须经过校验：

```text
ActionPlan
  -> Skill exists and enabled
  -> Skill Contract required coverage
  -> PlanningPolicy validation
  -> slot type / enum / range validation where available
  -> workflow validator dry-run or equivalent validator check
  -> version pin check
  -> DecisionBuilder
```

校验失败时：

- 不生成 PlanCard。
- 不调用 Capability。
- 写入 `PlannerTrace.Validations`。
- 返回 `ask_user`、`refuse_or_explain` 或可恢复的 error decision。
- 不静默 fallback 到 legacy short_drama 分支。

如果未来 LLM Planner 输出脏 `ActionPlan`，也必须在这一层被拦截。

## 11. 输出到 PlanCard

`PlanCard` 生成只读取已校验 `ActionPlan`：

```text
ActionPlan.ProposedSlots
ActionPlan.Assumptions
ActionPlan.Missing
ActionPlan.SkillKey
ActionPlan.Goal
CompiledSkill plan display metadata
```

PlanCard 必须展示：

- 合并后的最终有效值。
- 可编辑或可确认字段。
- confirmable assumption 的推荐理由。
- cost affecting default 的费用/时长/质量影响。
- asset 类 slot 的缩略图或选择状态。

PlanCard 不展示：

- 原始 CoT。
- 原始 LLM 输出。
- 完整 `PlannerTrace` JSON。
- 未经过 validator 的候选 slot。

## 12. PlannerActivity 输出

PlannerActivity 是用户可见的规划摘要。

推荐 Decision 形态：

```text
message.kind = planner_activity
message.id = stable pending activity message id
content.summary_steps = derived from PlannerTrace
content.trace_id = PlannerTrace.TraceID
content.expandable = true
```

完整 Trace 建议独立存储，通过 `trace_id` / `plan_id` / `message_id` 关联。客户端默认只拉摘要，用户点击“展开详情”时懒加载完整 Trace。

第一版不得新增 token streaming channel。

## 13. 修改 / 取消 / 再来一版 / out_of_scope

Planner 入口必须尊重 V2.2 / V2.3 的回合语义，避免误触发新目标规划。

推荐规则：

| 回合语义 | Planner 行为 |
| --- | --- |
| `request_modification` | 使用 V2.3 target / operation，进入 revise 类 `ActionPlan`，不当作新目标 |
| `cancel` | 优先生成 `cancel` action 或交给 V2.3 capability，不重新匹配 Skill |
| `regenerate` / 再来一版 | 继承 current plan / result context，生成 regenerate 类 `ActionPlan` |
| `answer_question` | 填充 pending 所问字段，继续原 pending plan |
| `out_of_scope` | 锚回当前任务上下文，必要时 `refuse_or_explain`，不创建无关 Skill plan |
| `start_goal` | 才进入新目标 Skill selection |

这部分的详细边界在 `07-react-loop-boundary.md` 裁决。

## 14. short_drama 零回退

V2.4 不能破坏现有 `short_drama` 闭环。

文档和后续实现必须保证：

- 旧短剧 PlanCard 字段语义不回退。
- Review 阶段修改 by fork 的 V2.3 语义不回退。
- confirm_plan / outbox / task_link / observer / review gate 不回退。
- Planner 主路径出错时不静默切回旧硬编码路径。
- Mock Planner 回归必须覆盖短剧创建、追问、确认、review 修改、取消、再来一版。

## 15. 第一版示例

输入：

```text
帮我做一个赛博朋克视频
```

`PlannerInput` 摘要：

```text
Turn.Act = start_goal
Target = nil / new goal
Operation = create
SkillCatalog includes video_gen
Memory has no active blocking pending
```

`ActionPlan`：

```text
Goal = create_video
SkillKey = video_gen
ProposedSlots = {
  prompt: "赛博朋克视频",
  bgm_style: "synthwave_electronic_bass",
  aspect_ratio: "9:16",
  duration: 15
}
Missing = []
Assumptions = [
  bgm_style inferred from cyberpunk theme,
  duration default 15s,
  aspect_ratio default 9:16
]
NextAction = create_plan_card
```

`PlannerTrace`：

```text
Observation: user goal classified as create_video
Observation: matched skill video_gen
Observation: contract requires prompt, bgm_style, aspect_ratio, duration
Observation: user provided prompt
Observation: missing bgm_style, aspect_ratio, duration
Assumption: inferred bgm_style=synthwave_electronic_bass
Assumption: applied system default aspect_ratio=9:16
Assumption: applied cost affecting default duration=15
Validation: required inputs covered
Decision: create_plan_card
```

输出：

```text
PlannerActivity upsert
PlanCard from validated ActionPlan
```

没有原始 Thought streaming，没有原始 CoT 持久化，没有未校验 slot 进入 PlanCard。
