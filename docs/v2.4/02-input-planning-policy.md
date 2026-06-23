# 02 · Input Planning Policy

## 1. 问题定位

V2.4 之前，缺参判断容易退化成：

```text
缺 required -> 追问
不缺 required -> PlanCard
```

这不足以支撑通用 Agent。

同样是缺 required input，真实处理方式可能完全不同：

- 缺 `source_image`：必须让用户上传或选择素材。
- 缺 `bgm_style`：可以根据主题推荐创意默认值，但必须让用户确认。
- 缺 `aspect_ratio`：可以使用系统默认值，但 PlanCard 要展示最终值。
- 缺 `duration`：可以推荐默认值，但它影响成本和生成时长，必须明示。

V2.4 的 `InputPlanningPolicy` 负责把这些情况结构化。

一句话：

```text
Skill Contract 决定“必须满足什么”。
InputPlanningPolicy 决定“缺失时如何满足”。
```

## 2. 权威边界

`InputPlanningPolicy` 不能取代 V2.1 Skill Contract。

| 层 | 负责 | 不负责 |
| --- | --- | --- |
| Skill Contract | 字段是否 required、类型、版本、可执行约束、validator 边界 | 不决定缺参时是否追问或推荐 |
| InputPlanningPolicy | 缺参策略、默认值来源、是否需要确认、降级方式 | 不绕过 required，不放宽 validator |
| ActionPlanValidator | 校验 Planner 输出是否覆盖契约并可执行 | 不生成创意默认值 |
| PlanCard adapter | 展示已校验的最终值和确认点 | 不决定 required 是否满足 |

硬规则：

- Contract required 未被满足时，不能生成 `create_plan_card`。
- Policy 只能决定 required 的满足来源，不能把 required 改成 optional。
- Planner 产出的所有 `ProposedSlots` 必须经过 Contract / Policy / Validator 校验。
- UI-required 不等于执行 required。V2.1 已裁决执行硬约束以 compiled contract / validator 为准。

## 3. Policy 模型

建议模型：

```go
type PlanningPolicy struct {
    Inputs map[string]InputPlanningRule
}

type InputPlanningRule struct {
    Field           string
    Policy          InputPlanningPolicyKind
    Default         any
    DefaultStrategy string
    ConfirmRequired bool
    ConfidenceFloor float64
    AskPrompt        string
    Display          PlanningDisplayRule
}

type InputPlanningPolicyKind string

const (
    InputPolicyAskUser              InputPlanningPolicyKind = "ask_user"
    InputPolicyCreativeDefault      InputPlanningPolicyKind = "creative_default"
    InputPolicySystemDefault        InputPlanningPolicyKind = "system_default"
    InputPolicyCostAffectingDefault InputPlanningPolicyKind = "cost_affecting_default"
    InputPolicyRequiresAsset        InputPlanningPolicyKind = "requires_asset"
)
```

`InputPlanningRule` 是 `CompiledSkill` 的一部分。Planner 运行时只读取 compiled policy，不直接解析原始 manifest。

## 4. Manifest 声明

Manifest 可扩展 planning 段：

```yaml
planning:
  inputs:
    bgm_style:
      policy: creative_default
      default_strategy: infer_from_theme
      confirm_required: true
      confidence_floor: 0.7
    source_image:
      policy: requires_asset
      ask_prompt: 请先上传一张参考图。
    aspect_ratio:
      policy: system_default
      default: "9:16"
      confirm_required: false
    duration:
      policy: cost_affecting_default
      default: 15
      confirm_required: true
```

编译规则：

- planning field 必须能映射到 contract field 或 slot field。
- required field 若没有 planning rule，则编译期使用保守默认 `ask_user`，并产生 auditor warning。
- `creative_default` 必须声明 `default_strategy`。
- `system_default` / `cost_affecting_default` 必须声明 `default` 或能从 mode default 推导。
- `requires_asset` 必须声明资产类型或能从 contract 类型推导。
- `confirm_required=false` 不允许用于 `creative_default` 和 `cost_affecting_default` 的第一版。

## 5. 值来源优先级

Planner 生成 `ProposedSlots` 时，必须记录每个字段的来源。

推荐优先级：

```text
1. system_injected                    # callback_token 等，用户不可覆盖
2. user override                      # 用户明确否定或改写过的值
3. current turn slot                  # 当前输入直接提供的值
4. pending answer                     # 回答上一轮追问
5. current plan carryover             # 修改 / 再来一版继承
6. collected memory slot              # AgentState 已收集值
7. mode default / default_input_json  # V2.1 contract resolver 合并
8. planning system_default
9. planning cost_affecting_default
10. planning creative_default
```

说明：

- `system_injected` 最高优先，且不进入用户可编辑字段。
- `system_injected` 只由系统注入。Planner 可以观察它是否存在，但不能生成、覆盖、推断、向用户解释为偏好，也不能展示成 PlanCard 可编辑项。典型字段包括 `callback_token`、`user_id`、`task_id`、`trace_id`。
- `user override` 高于任何默认值和 creative inference。
- `current plan carryover` 只在 V2.2/V2.3 判定为修改、再来一版、继续当前目标时启用。
- `creative_default` 优先级最低，因为它是推断，不应覆盖任何用户事实。
- 每个非用户来源的默认值都应产生 `PlannerObservation` 或 `PlannerAssumption`，便于审计。

## 6. EffectiveRequired 处理流程

推荐流程：

```text
for each field in EffectiveRequired:
  if value exists from user/system/carryover/default:
    add to ProposedSlots
    record value source
    continue

  rule = PlanningPolicy.inputs[field] or ask_user

  switch rule.policy:
    ask_user:
      add MissingInput
      NextAction = ask_user

    requires_asset:
      add MissingInput with asset requirement
      NextAction = ask_user

    system_default:
      apply default
      add ProposedSlots
      record Observation

    cost_affecting_default:
      apply default candidate
      add ProposedSlots
      add PlannerAssumption(confirmable=true)

    creative_default:
      infer candidate
      if confidence >= floor:
        add ProposedSlots
        add PlannerAssumption(confirmable=true)
      else:
        add MissingInput
        NextAction = ask_user

validate ProposedSlots against contract and validator
if MissingInput remains:
  NextAction = ask_user
else:
  NextAction = create_plan_card or invoke_capability
```

注意：当存在多个 missing fields 时，第一版默认最多追问 1 个阻塞字段，优先问阻塞性最高的字段。只有同类轻量偏好字段才允许聚合追问，且单轮最多 3 个字段。

示例：

- 缺 `source_image`：只问上传图片，不同时追问风格、音乐、尺寸。
- 缺 `bgm_style` + `narration_style`：可以合并为一个轻量偏好问题。
- 缺 `duration` + `model_quality`：如果都会影响成本，应进入 PlanCard 明示，而不是在聊天里一次塞给用户一串表单问题。

## 7. Policy 分类

### 7.1 ask_user

用于 Agent 不应猜测的字段。

典型字段：

- 明确的业务约束，如品牌名、具体产品、预算上限。
- 用户偏好强且无法从上下文可靠推断的字段。
- Validator 必填且没有安全默认值的字段。

输出：

```text
MissingInput.Policy = ask_user
NextAction = ask_user
```

不生成 `PlannerAssumption`。

### 7.2 requires_asset

用于必须依赖用户上传或选择素材的字段。

典型字段：

- `source_image`
- `reference_video`
- `voice_sample`
- `product_photo`

处理：

- 如果当前 turn 携带资产，绑定资产并记录 Observation。
- 如果会话 ActiveObjects 中存在可用资产，按 V2.3 target / recency / user reference 解析。
- 如果没有资产，生成 `MissingInput`，追问用户上传或选择。

`requires_asset` 不允许用文字描述伪造资产。

### 7.3 system_default

用于技术默认值。

典型字段：

- `aspect_ratio = 9:16`
- `resolution = 720p`
- `quality = standard`
- `enable_prompt_optimize = true`

处理：

- 可直接进入 `ProposedSlots`。
- 必须记录 value source。
- 必须展示在 PlanCard 的最终有效值中。
- 一般不需要 `PlannerAssumption`，但要有 `PlannerObservation`。

如果 system default 与 mode default 冲突，以 V2.1 default 合并裁决为准，planning default 只能作为缺口补充或显式覆盖并通过编译审计。

### 7.4 creative_default

用于可以从主题、风格、场景推断的创意字段。

典型字段：

- `bgm_style`
- `visual_style`
- `tone`
- `color_mood`
- `narration_style`

处理：

- 必须生成 `PlannerAssumption`。
- 必须 `Confirmable=true`。
- 必须记录 `Reason` 和 `Confidence`。
- 低于 `ConfidenceFloor` 时降级为 `ask_user`。
- 用户后续否定时，必须写入 override，不能再次推荐同一值。

第一版可先使用确定性规则或小型映射表，例如：

| 输入主题 | 推荐 |
| --- | --- |
| 赛博朋克 | `synthwave_electronic_bass` |
| 治愈系 | `soft_piano_ambient` |
| 搞笑短剧 | `upbeat_playful` |
| 古风 | `traditional_chinese_orchestral` |

若未来使用 LLM 生成 creative default，必须受限于：

- 只输出结构化 candidate，不输出原始 CoT。
- P99 目标不超过 1 秒，否则降级到规则默认或追问。
- 输出必须经过 enum / validator 校验。
- 低置信或 schema parse 失败时降级为 `ask_user`。

### 7.5 cost_affecting_default

用于影响成本、耗时、资源消耗或质量档位的默认值。

典型字段：

- `duration`
- `shot_count`
- `model_quality`
- `resolution`
- `generation_count`

处理：

- 可推荐默认值，但必须在 PlanCard 明示。
- 必须 `Confirmable=true`。
- 必须记录是否影响费用、耗时或质量。
- 不能在无确认情况下直接启动高成本任务。

第一版建议所有 `cost_affecting_default` 的 `NextAction` 都走 `create_plan_card`，不走免确认 `invoke_capability`。

## 8. MissingInput 排序

当多个字段缺失时，追问优先级：

```text
1. requires_asset
2. ask_user required
3. low confidence creative_default
4. cost_affecting_default needing explicit choice
5. optional preference
```

示例：

```text
image_to_image.source_image 缺失
style 缺失但可 creative_default
aspect_ratio 缺失但可 system_default
```

应先追问上传图片，而不是先问风格。

## 9. NextAction 决策

`InputPlanningPolicy` 不独自决定全部 `NextAction`，但它会对 Planner 的下一步形成约束：

| 条件 | 允许的 NextAction |
| --- | --- |
| 存在 `requires_asset` missing | `ask_user` |
| 存在 `ask_user` missing | `ask_user` |
| 存在低置信 creative missing | `ask_user` |
| required 已覆盖且存在 confirmable assumption | `create_plan_card` |
| required 已覆盖且无确认需求，Skill 允许免确认 | `invoke_capability` |
| contract / validator 失败 | `ask_user` 或 `refuse_or_explain` |

第一版建议保守：只要有 `creative_default` 或 `cost_affecting_default`，就进入 `create_plan_card`，让用户确认。

## 10. Validator 失败与降级

Planner 输出可能失败：

- creative default 不在 enum 中。
- system default 与 validator 白名单不一致。
- cost default 超出范围。
- LLM 输出字段类型错误。
- required 被错误判定为已覆盖。

降级规则：

```text
validator pass:
  continue

validator fail because candidate value invalid:
  remove candidate
  record Validation failure
  if field required:
    MissingInput -> ask_user
  else:
    drop optional value

validator fail because contract changed:
  refuse_or_explain / expired plan

validator fail because planner output malformed:
  ask_user or error decision
```

禁止静默回退到 legacy `short_drama` 分支。

## 11. 与 PlanCard 的关系

PlanCard 展示的是已校验 `ActionPlan` 的最终值。

PlanCard 必须能区分：

- 用户提供值。
- 系统默认值。
- 成本相关默认值。
- 创意推荐值。
- 用户覆盖值。

PlanCard 不负责决定这些值如何产生。它只展示 Planner 和 Validator 已经确认的结果。

## 12. 示例：赛博朋克视频

用户：

```text
帮我做一个赛博朋克视频
```

Contract required：

```text
prompt
bgm_style
aspect_ratio
duration
```

Policy：

```yaml
planning:
  inputs:
    bgm_style:
      policy: creative_default
      default_strategy: infer_from_theme
      confirm_required: true
    aspect_ratio:
      policy: system_default
      default: "9:16"
    duration:
      policy: cost_affecting_default
      default: 15
      confirm_required: true
```

输出：

```text
prompt = 用户提供
bgm_style = synthwave_electronic_bass, PlannerAssumption(confirmable=true)
aspect_ratio = 9:16, system_default
duration = 15, PlannerAssumption(confirmable=true, cost_affecting=true)
NextAction = create_plan_card
```

## 13. 示例：图生图缺图

用户：

```text
把这张改成动漫风
```

如果当前 turn 没有图片，ActiveObjects 也没有可引用图片：

```text
source_image missing
Policy = requires_asset
NextAction = ask_user
```

回复应锚定缺失素材：

```text
请先上传一张参考图，我再帮你改成动漫风。
```

不得用文字“这张图”伪造 `source_image`。

## 14. 回归要求

Mock Planner / Policy 单测必须覆盖：

- required + `ask_user`。
- required + `requires_asset`。
- required + `system_default`。
- required + `creative_default` 高置信。
- required + `creative_default` 低置信降级追问。
- required + `cost_affecting_default` 进入 PlanCard。
- 用户 override 高于 creative default。
- validator 拒绝默认值后降级。
- 修改 / 再来一版继承 current plan 值。
- `short_drama` 现有 PlanCard 字段不回退。

## 15. 裁决

V2.4 第一版采用保守策略：

- 缺 required 不允许绕过。
- `requires_asset` 和 `ask_user` 阻塞 PlanCard。
- `creative_default` 必须形成 confirmable assumption。
- `cost_affecting_default` 必须在 PlanCard 明示并确认。
- `system_default` 可直接使用，但必须展示最终有效值。
- 所有输出必须经过 Contract / Policy / Validator 校验。
