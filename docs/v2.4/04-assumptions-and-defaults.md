# 04 · Assumptions and Defaults

## 1. 问题定位

Planner 推荐默认值不是偷偷替用户做决定。

V2.4 必须把默认值和推断值结构化，让系统能回答：

```text
这个值来自哪里？
它是用户说的，系统默认的，mode 默认的，还是 Planner 推断的？
它影响费用吗？
它需要用户确认吗？
用户否定后如何覆盖？
置信度不够时如何降级？
```

核心模型是 `PlannerAssumption`，但不是所有默认值都是 assumption。

## 2. 默认值分类

| 类型 | 是否 Assumption | 是否默认展示 | 是否必须确认 |
| --- | --- | --- | --- |
| user value | 否 | 是 | 否 |
| user override | 否，但要记录 override | 是 | 否 |
| system injected | 否 | 通常否 | 否 |
| mode default | 否，可记录 Observation | 是 | 通常否 |
| system default | 否，可记录 Observation | 是 | 通常否 |
| creative default | 是 | 是 | 是 |
| cost affecting default | 是 | 是 | 是 |
| carryover from current plan | 否，可记录 Observation | 是 | 视字段而定 |

判断标准：

```text
Assumption = Planner 基于上下文做出的可争议推断或推荐。
Default = 系统、contract、mode 或 policy 提供的默认值。
Override = 用户明确修改、否定或替换过的值。
```

## 3. PlannerAssumption 模型

建议模型：

```go
type PlannerAssumption struct {
    ID          string  `json:"id"`
    Field       string  `json:"field"`
    Value       any     `json:"value"`
    Source      string  `json:"source"`
    Confidence  float64 `json:"confidence"`
    Reason      string  `json:"reason"`
    Confirmable bool    `json:"confirmable"`
    CostImpact  *CostImpactHint `json:"cost_impact,omitempty"`
    Supersedes  *AssumptionRef  `json:"supersedes,omitempty"`
}
```

`ID` 是稳定标识，必须在同一次规划和后续用户操作中可引用。用户 override、PlanCard 字段编辑、Trace 展开详情都应指向具体 assumption ID，而不是按 `field/value` 模糊匹配。

补充模型：

```go
type ValueSource string

const (
    ValueSourceUser              ValueSource = "user"
    ValueSourceUserOverride      ValueSource = "user_override"
    ValueSourceSystemInjected    ValueSource = "system_injected"
    ValueSourceModeDefault       ValueSource = "mode_default"
    ValueSourceSystemDefault     ValueSource = "system_default"
    ValueSourceCreativeDefault   ValueSource = "creative_default"
    ValueSourceCostDefault       ValueSource = "cost_affecting_default"
    ValueSourcePlanCarryover     ValueSource = "plan_carryover"
)
```

`PlannerAssumption.Source` 第一版只允许：

- `creative_default`
- `cost_affecting_default`

其他来源记录为 value source 或 `PlannerObservation`，不进入 `Assumptions` 列表，避免把所有默认值都包装成“AI 推断”。

## 4. Reason 写法

`Reason` 是用户可见解释的事实摘要，不是原始 CoT。

允许：

```text
赛博朋克视觉风格通常适合电子合成器和低频鼓点。
15 秒是当前视频生成的默认时长，成本和生成时间较低。
```

禁止：

```text
我首先想到用户可能喜欢霓虹城市，然后我推理出...
模型内心判断...
```

要求：

- 简短。
- 可审计。
- 可展示。
- 不包含原始 prompt、内部 chain、打分细节或不可验证臆测。

## 5. Confidence

`Confidence` 表示 Planner 对该推荐值适合当前上下文的置信度。

建议区间：

| 区间 | 行为 |
| --- | --- |
| `>= 0.85` | 高置信，可进入 PlanCard，仍需确认 |
| `0.70 - 0.85` | 中置信，可进入 PlanCard，但推荐文案应保守 |
| `0.50 - 0.70` | 低置信，不自动填入 required，降级追问 |
| `< 0.50` | 不生成 assumption，直接追问或忽略 |

第一版裁决：

```text
creative_default confidence_floor = 0.70
cost_affecting_default confidence = 1.0 only means default 来源确定，不代表用户偏好确定
```

注意：成本默认值的 confidence 不应伪装成“用户一定想要”。它只表示“这是系统推荐的保守默认值”。

## 6. Confirmable

`Confirmable` 表示用户是否需要在决策边界看到并可调整该值。

第一版规则：

| 来源 | Confirmable |
| --- | --- |
| creative_default | true |
| cost_affecting_default | true |
| system_default | false by default |
| mode_default | false by default |
| user override | false |
| plan carryover | false by default，若影响成本可 true |

即使 `Confirmable=false`，PlanCard 仍应展示最终有效值。区别是：

- confirmable value：PlanCard 需要明确“推荐/默认”的来源和可修改状态。
- non-confirmable default：PlanCard 作为最终参数展示即可。

## 7. 默认值来源

### 7.1 Mode Default

来源：

```text
ToolModeVersion.default_input_json
ToolModeVersion.default_model
ToolModeVersion.default_style
```

由 V2.1 contract resolver 合并。

规则：

- 优先级高于 planning default。
- 进入 `ProposedSlots` 前必须通过 validator。
- PlanCard 展示合并后的最终有效值。
- 一般不生成 `PlannerAssumption`。

### 7.2 System Default

来源：

```text
planning.inputs[field].policy = system_default
planning.inputs[field].default
```

典型：

```text
aspect_ratio = 9:16
quality = standard
resolution = 720p
```

规则：

- 用于技术默认值。
- 可直接使用。
- 记录 value source。
- PlanCard 展示最终值。

### 7.3 Creative Default

来源：

```text
planning.inputs[field].policy = creative_default
default_strategy = infer_from_theme
```

典型：

```text
bgm_style
visual_style
tone
color_mood
```

规则：

- 必须生成 `PlannerAssumption`。
- 必须有 `Reason`。
- 必须 `Confirmable=true`。
- 必须有 confidence。
- 必须通过 enum / validator。
- 用户 override 后不得重复推荐同值。

### 7.4 Cost Affecting Default

来源：

```text
planning.inputs[field].policy = cost_affecting_default
default = 15
```

典型：

```text
duration
shot_count
model_quality
generation_count
```

规则：

- 必须生成 `PlannerAssumption`。
- 必须 `Confirmable=true`。
- 必须标注成本、耗时或质量影响。
- `Reason` 必须解释成本、耗时或质量 tradeoff，不能伪装成用户审美偏好。例如“时长默认 15 秒，因为这是当前视频生成的低成本标准长度”，不要写成“你可能喜欢 15 秒视频”。
- 不允许无确认直接启动高成本执行。

### 7.5 Plan Carryover

来源：

```text
current plan
current result
review revision source plan
```

只在 V2.2/V2.3 判定为延续当前任务时启用：

- 修改。
- 再来一版。
- 回答 pending。
- review by fork。

规则：

- carryover 不等于新推断。
- 一般不生成 assumption。
- 如果用户本轮覆盖同一字段，以用户覆盖为准。
- Trace 应记录该值继承自哪个 plan / object。

## 8. 用户 Override

用户 override 指用户明确否定、替换或调整 Planner 推荐值。

示例：

```text
Planner: 音乐推荐 Synthwave。
用户: 不要电子乐，换成钢琴。
```

必须记录：

```go
type PlannerOverride struct {
    ConversationID      int64
    PlanID              *int64
    SkillKey            string
    Field              string
    PreviousValue      any
    NewValue           any
    SourceAssumptionID *string
    Reason             string
    CreatedAt          time.Time
}
```

运行规则：

- override 高于 creative default、cost default、system default、mode default。
- override 只在同一目标 / plan lineage / 用户明确延续上下文时生效。
- override 默认绑定到 `conversation_id` / `plan_id` / `skill_key` / `field`，不自动升级为长期用户偏好。
- 新目标不应无条件继承旧 override，除非用户偏好被明确保存为长期偏好。
- 用户说“不要电子乐，换成钢琴”只覆盖当前 plan lineage；只有用户明确说“以后都不要电子乐”时，才可能进入长期偏好记忆。
- override 需要写入 `AgentMemorySnapshot.UserOverrides` 或等价结构。
- 下一轮 Planner 必须检查 override，避免重复推荐被否定值。

## 9. Override 识别

可由 V2.2/V2.3 先判断当前回合语义：

| 用户输入 | 语义 |
| --- | --- |
| “不要电子乐” | 否定当前 assumption |
| “换成钢琴” | 替换字段值 |
| “时长改 30 秒” | 覆盖 cost default |
| “横版吧” | 覆盖 aspect ratio |
| “就按你推荐的” | 接受 assumption |

Planner 不应仅靠全局关键词改字段。必须结合：

- 当前 PlanCard 中有哪些 confirmable assumptions。
- PendingInteraction 是否正在确认字段。
- TargetResolution 是否指向 current plan / PlanCard。
- OperationIntent 是否为 revise / confirm / answer。

## 10. Assumption 生命周期

推荐状态：

```go
type AssumptionStatus string

const (
    AssumptionProposed  AssumptionStatus = "proposed"
    AssumptionAccepted  AssumptionStatus = "accepted"
    AssumptionOverridden AssumptionStatus = "overridden"
    AssumptionExpired   AssumptionStatus = "expired"
)
```

生命周期：

```text
Planner recommends value
  -> proposed
PlanCard confirmed without change
  -> accepted
User edits value
  -> overridden
Plan superseded / contract changed
  -> expired
```

第一版可以不单独落表，但文档和 Trace 必须保留这些语义，避免未来迁移时丢状态。

## 11. Assumption 与 Trace

每个 `PlannerAssumption` 必须能追溯：

- `trace_id`
- `assumption_id`
- `field`
- `value`
- `source`
- `confidence`
- `reason`
- 是否进入 PlanCard
- 是否被用户接受或覆盖

用户可见 Planning Summary 只展示摘要：

```text
自动推荐：电子重低音 / Synthwave
```

展开详情可展示：

```text
字段：bgm_style
推荐值：synthwave_electronic_bass
依据：赛博朋克视觉风格通常适合电子合成器和低频鼓点
置信度：0.82
需要确认：是
```

不得展示原始 CoT。

## 12. PlanCard 展示要求

PlanCard 生成文档在 `06-plan-card-generation.md` 详细裁决。这里先定义数据要求：

PlanCard 必须能展示：

- final value。
- value source。
- confirmable 标记。
- creative recommendation reason。
- cost impact hint。
- user override 后的新值。

示例：

```text
音乐：电子重低音 / Synthwave
说明：根据赛博朋克主题自动推荐，可修改。

时长：15 秒
说明：默认时长，影响生成费用和等待时间，可修改。
```

## 13. LLM 生成默认值边界

第一版优先使用确定性策略：

- theme mapping。
- manifest examples。
- style options。
- mode defaults。

如果后续使用 LLM：

- 输入只给必要摘要，不给完整隐私上下文。
- 输出必须是结构化 JSON。
- 禁止持久化原始 LLM 输出为业务事实。
- 禁止展示原始 reasoning。
- schema parse 失败降级。
- enum / validator 失败降级。
- P99 超过 1 秒时降级到规则或追问。

LLM 只负责候选值，不负责决定是否绕过确认。

## 14. 示例：接受推荐

Planner：

```text
bgm_style = synthwave_electronic_bass
AssumptionStatus = proposed
```

用户确认 PlanCard：

```text
AssumptionStatus = accepted
Task input uses synthwave_electronic_bass
```

Trace：

```text
Decision: user accepted ActionPlan assumptions
```

## 15. 示例：覆盖推荐

Planner 推荐：

```text
bgm_style = synthwave_electronic_bass
```

用户：

```text
不要电子乐，换成钢琴
```

下一轮：

```text
UserOverrides.bgm_style = soft_piano
ProposedSlots.bgm_style = soft_piano
PreviousAssumption.status = overridden
No new synthwave assumption
```

Trace：

```text
Observation: user overrode bgm_style from synthwave_electronic_bass to soft_piano
Decision: apply user override before creative default
```

## 16. 示例：低置信降级

用户：

```text
帮我做一个高级感视频
```

`bgm_style` 可推断空间过大：

```text
candidate = ambient_luxury
confidence = 0.58
confidence_floor = 0.70
```

输出：

```text
MissingInput.bgm_style
NextAction = ask_user
```

不要把低置信推荐塞进 PlanCard。

## 17. 回归要求

必须覆盖：

- creative default 生成 assumption。
- creative default 低置信降级追问。
- cost default 生成 confirmable assumption。
- system default 不生成 assumption 但展示 final value。
- mode default 优先于 planning default。
- user override 高于 creative default。
- user override 不泄漏到无关新目标。
- plan carryover 不被误写成新 assumption。
- enum / validator 拒绝 assumption 后降级。
- confirm PlanCard 后 assumption 进入 accepted 语义。
- contract 变化后 assumption expired。

## 18. 裁决

V2.4 第一版对 assumption 和默认值采用以下裁决：

- 只有 creative default 和 cost affecting default 进入 `PlannerAssumption`。
- 所有值都必须记录来源。
- creative default 默认 `Confirmable=true`，低于 confidence floor 降级追问。
- cost affecting default 默认 `Confirmable=true`，不得免确认直跑高成本任务。
- user override 优先级高于所有默认值。
- override 必须阻止同一上下文重复推荐被否定值。
- Assumption 可见摘要来自结构化字段，不来自原始 CoT。
