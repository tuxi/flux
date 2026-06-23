# 06 · PlanCard Generation

## 1. 问题定位

V2.4 必须明确：

```text
PlanCard 不是 Planner。
PlanCard 是 ActionPlan 的 UI 投射。
```

Planner 输出 `ActionPlan`，经过 Contract / Policy / Validator 校验后，才允许生成 PlanCard。

错误做法：

- Planner 直接拼 PlanCard。
- PlanCard builder 自己决定缺参策略。
- PlanCard 展示未校验 slot。
- PlanCard 内部承载 PlannerTrace 或原始 LLM 输出。

正确链路：

```text
PlannerInput
  -> AgentPlanner
  -> ActionPlan
  -> ActionPlanValidator
  -> PlanCardAdapter
  -> plan_card message
```

## 2. 输入条件

PlanCardAdapter 只接受已校验的 `ActionPlan`。

前置条件：

- `ActionPlan.NextAction = create_plan_card`。
- `SkillKey` 存在且 enabled。
- required input 已覆盖，或 missing input 已明确作为 PlanCard 可确认项处理。
- `ProposedSlots` 已通过 Contract / Policy / Validator。
- `ToolModeVersionID` 和 `ContractSchemaHash` 已固定。
- `PlannerAssumption` 均有稳定 ID。
- cost affecting default 已标记。

如果任一条件失败，不生成 PlanCard，降级为 `ask_user` 或 `refuse_or_explain`。

## 3. PlanCard 数据模型

建议 UI payload：

```go
type PlanCardContent struct {
    PlanID             int64
    TraceID            string
    Goal               string
    SkillKey           string
    ToolModeVersionID  int64
    ContractSchemaHash string
    Fields             []PlanCardField
    Assumptions        []PlanCardAssumption
    CostHints          []PlanCardCostHint
    Actions            []PlanCardAction
}

type PlanCardField struct {
    Field       string
    Label       string
    Value       any
    Source      ValueSource
    Editable    bool
    Required    bool
    DisplayType string
}
```

PlanCard payload 是 UI 投射，不是 Planner 的源模型。它可以冗余携带展示字段，但不能成为 confirm 时的唯一事实来源。

## 4. 字段来源展示

PlanCard 必须区分 value source：

| Source | 展示方式 |
| --- | --- |
| `user` | 普通参数 |
| `user_override` | 展示为用户已修改值 |
| `mode_default` | 展示最终默认值 |
| `system_default` | 展示最终默认值 |
| `creative_default` | 展示推荐值和原因，可修改 |
| `cost_affecting_default` | 展示默认值、成本/耗时/质量影响，可修改 |
| `plan_carryover` | 展示为沿用当前方案 |
| `system_injected` | 不展示为可编辑字段 |

`system_injected` 不应出现在 PlanCard 可编辑项中。必要时只显示“系统已准备好执行上下文”，不显示 token 或内部 ID。

## 5. Assumption 展示

PlanCard 中的 assumption 必须来自 `PlannerAssumption`。

建议模型：

```go
type PlanCardAssumption struct {
    AssumptionID string
    Field        string
    Value        any
    Reason       string
    Confidence   float64
    Confirmable  bool
    Editable     bool
}
```

展示规则：

- `creative_default`：展示“根据主题推荐”，用户可改。
- `cost_affecting_default`：展示“默认值及成本/耗时影响”，用户可改。
- 低置信 assumption 不进入 PlanCard，应先追问。
- PlanCard 不展示原始 CoT。
- PlanCard 不展示完整 PlannerTrace JSON。

## 6. Cost Hint

成本相关字段必须显式展示。

建议模型：

```go
type PlanCardCostHint struct {
    Field       string
    Value       any
    ImpactType  string // cost | latency | quality | quota
    Description string
}
```

示例：

```text
时长：15 秒
说明：默认时长，成本和生成时间较低，可修改。
```

Reason 只能解释成本、耗时或质量 tradeoff，不能伪装成用户偏好。

## 7. Editable 规则

字段是否可编辑由 compiled manifest / contract / policy 决定。

这里的 `Editable` 是 Agent 自然语言修改边界，表示用户可以通过对话或卡片操作提出修改。它不要求客户端第一版必须实现完整表单 UI；客户端可以先用自然语言编辑入口承接，再由后端重新生成 `ActionPlan` / PlanCard。

默认规则：

| 字段类型 | Editable |
| --- | --- |
| user slot | true |
| creative_default | true |
| cost_affecting_default | true |
| system_default | 视 manifest 而定，默认 true for UX |
| mode_default | 视 manifest 而定 |
| system_injected | false |
| derived | false，除非 manifest 显式声明可编辑源字段 |

PlanCard 修改字段后，不能直接启动任务。必须重新走：

```text
edited PlanCard values
  -> ActionPlan update / override
  -> Contract / Policy / Validator
  -> refreshed PlanCard or confirmable Plan
```

## 8. Confirm 语义

用户确认 PlanCard 时，必须校验：

- plan still current。
- `ToolModeVersionID` 仍匹配当前或允许按固定旧版本执行。
- `ContractSchemaHash` 未过期，或按 V2.1 裁决提示重新生成。
- user edits 已重新校验。
- assumptions 状态从 `proposed` 进入 `accepted`。
- outbox dedup key 使用 plan id。

`confirm_plan` 不信任客户端传回的 PlanCard payload。客户端只能提交确认动作、plan/message 标识和用户编辑意图；服务端必须读取已保存的 `Plan` / `ActionPlan` / pinned `ToolModeVersionID` / `ContractSchemaHash` 作为事实来源，并重新校验后才能创建 task。

如果版本过期：

```text
这个方案使用的能力配置已更新，请重新生成方案。
```

不得静默用新 contract 执行旧 PlanCard。

## 9. MissingInput 与 PlanCard

通常存在阻塞 missing input 时不生成 PlanCard。

例外只允许：

- 字段已由 confirmable `creative_default` 覆盖。
- 字段已由 confirmable `cost_affecting_default` 覆盖。
- 字段是 PlanCard 内可编辑的可选偏好，不影响 contract required。

`requires_asset` missing 不能生成 PlanCard。必须先追问或让用户选择素材。

## 10. 与 PlannerTrace 的关系

PlanCard 可以携带 `trace_id`，用于展开“为什么这么规划”。

PlanCard 不直接内嵌完整 Trace。

```text
PlanCard.trace_id -> lazy load PlannerTrace detail
```

PlanCard 展示的 recommendation reason 来自 `PlannerAssumption.Reason`，不是从 Trace 自然语言中反向解析。

## 11. 与 ReviewCard / ResultCard 的边界

PlanCard 是执行前确认。

ReviewCard 是 Engine 中途等待用户确认 artifact。

ResultCard 是执行完成后的结果入口。

```text
PlanCard   -> confirm plan and create task
ReviewCard -> confirm / revise generated intermediate artifact
ResultCard -> view / regenerate / modify final artifact
```

PlannerTrace 可以解释为什么生成 PlanCard，但不替代 ReviewCard / ResultCard 的操作语义。

## 12. 示例：赛博朋克视频

ActionPlan：

```text
prompt = 赛博朋克视频, source=user
bgm_style = synthwave_electronic_bass, source=creative_default, assumption_id=a1
aspect_ratio = 9:16, source=system_default
duration = 15, source=cost_affecting_default, assumption_id=a2
```

PlanCard：

```text
已为你生成赛博朋克视频方案：

主题：赛博朋克视频
音乐：电子重低音 / Synthwave
说明：根据赛博朋克主题自动推荐，可修改。
画幅：9:16
时长：15 秒
说明：默认时长，成本和生成时间较低，可修改。

确认后开始创作。
```

## 13. 示例：图生图缺图

如果：

```text
source_image missing
Policy = requires_asset
```

则不生成 PlanCard。

输出：

```text
请先上传一张参考图，我再帮你改成动漫风。
```

## 14. 回归要求

必须覆盖：

- PlanCard 只从已校验 ActionPlan 生成。
- required missing 阻止 PlanCard。
- `requires_asset` missing 阻止 PlanCard。
- creative default 作为可编辑推荐展示。
- cost default 展示成本/耗时/质量 tradeoff。
- system injected 不展示为可编辑字段。
- 用户编辑 PlanCard 后重新校验。
- active version 变化后 confirm 阻止旧 PlanCard 静默执行。
- PlanCard 携带 `trace_id` 但不内嵌完整 Trace。
- `short_drama` 旧字段展示不回退。

## 15. 裁决

V2.4 第一版：

- PlanCard 由已校验 `ActionPlan` 投射生成。
- PlanCard 不作为 Planner 内部模型。
- PlanCard 不展示原始 CoT 或完整 Trace。
- PlanCard 必须展示最终有效值、value source、confirmable assumption 和 cost hint。
- PlanCard confirm 必须重新做版本固定和 validator 检查。
