# 05 · Planner Trace and Visible Thinking

## 1. 问题定位

V2.4 需要让用户感受到 Agent 正在规划，但不能展示原始 Chain-of-Thought。

正确方向：

```text
真实 Planner event
真实 Skill observation
真实 missing input 判断
真实 Assumption
真实 Decision
真实 Validation
真实 Engine / Capability observation
  -> 结构化 PlannerTrace
  -> 用户可见 Planning Summary
```

错误方向：

```text
LLM 原始思维链
token 级 thought stream
编造“AI 正在思考”的假事件
把原始 LLM 输出写进 message / DB / log
```

一句话：

```text
白盒感来自可审计的 PlannerTrace，不来自原始 CoT。
```

## 2. Trace 模型

建议模型：

```go
type PlannerTrace struct {
    TraceID        string
    ConversationID int64
    MessageID      *int64
    PlanID         *int64
    SkillKey       string
    Goal           string
    CatalogVersion string
    Events         []PlannerTraceEvent
    CreatedAt      time.Time
    ExpiresAt      *time.Time
}

type PlannerTraceEvent struct {
    ID        string
    Type      PlannerTraceEventType
    Level     PlannerTraceLevel
    Source    PlannerTraceSource
    Code      string
    Summary   string
    Payload   map[string]any
    CreatedAt time.Time
}
```

事件类型必须结构化：

```go
type PlannerTraceEventType string

const (
    TraceObservation PlannerTraceEventType = "observation"
    TraceAssumption  PlannerTraceEventType = "assumption"
    TraceDecision    PlannerTraceEventType = "decision"
    TraceAction      PlannerTraceEventType = "action"
    TraceValidation  PlannerTraceEventType = "validation"
    TraceEngineEvent PlannerTraceEventType = "engine_event"
)
```

禁止把 Trace 写成一组自然语言字符串。自然语言只能是结构化 event 的摘要字段。

## 3. Event 来源

`Source` 用来区分事件来源：

| Source | 来源 |
| --- | --- |
| `turn_interpreter` | V2.2 回合语义 |
| `target_resolver` | V2.3 目标对象 |
| `operation_interpreter` | V2.3 操作语义 |
| `skill_catalog` | V2.1 compiled catalog |
| `input_policy` | V2.4 缺参策略 |
| `assumption_policy` | V2.4 默认值和 assumption |
| `action_plan_validator` | Contract / Policy / Validator 校验 |
| `decision_builder` | Decision / PlanCard adapter |
| `capability_runtime` | V2.3 Capability 调用 |
| `engine_observer` | Workflow / Engine 真实事件 |

用户可见 Trace 必须能追溯到上述真实来源之一。

## 4. Trace 级别

建议级别：

| Level | 用途 | 默认用户可见 |
| --- | --- | --- |
| `summary` | 用户可见规划摘要 | 是 |
| `detail` | 展开详情 | 懒加载 |
| `debug` | 开发调试 | 否 |
| `internal` | 内部诊断，可能含敏感 payload 摘要 | 否 |

默认客户端只展示 `summary`。用户点击“展开详情”时，才懒加载 `detail`。`debug/internal` 不进普通客户端。

## 5. 用户可见摘要

PlannerActivity 由 `PlannerTrace` 生成。

示例：

```text
识别目标：赛博朋克视频
匹配能力：视频生成
检查输入：缺少音乐风格
自动推荐：电子重低音 / Synthwave
生成方案
```

对应事件：

```text
Observation: goal=create_video
Observation: selected_skill=video_gen
Observation: missing_field=bgm_style
Assumption: bgm_style=synthwave_electronic_bass
Decision: next_action=create_plan_card
```

摘要生成规则：

- 只使用 `summary/detail` 事件。
- 只使用结构化字段。
- 不展示原始 prompt、原始 LLM 输出或内部评分细节。
- 不展示 disabled skill 的内部原因，除非用户正在询问能力不可用。
- 不把 validator 错误堆栈展示给用户。

## 6. WS 渲染方式

V2.4 不新增 token 级 thought stream。

推荐沿用 conversation WS message upsert：

```text
PlannerActivity = 一条可变 message
message.id = stable planner activity message id
server upsert same message id
client replace same message id
```

建议 message content：

```json
{
  "kind": "planner_activity",
  "trace_id": "ptrace_...",
  "status": "planning",
  "summary_steps": [
    {"state": "done", "label": "识别目标：赛博朋克视频"},
    {"state": "done", "label": "匹配能力：视频生成"},
    {"state": "active", "label": "生成方案"}
  ],
  "expandable": true
}
```

PlannerActivity 终态：

| Status | 含义 |
| --- | --- |
| `planning` | Planner 正在产出或 upsert 摘要 |
| `completed` | 已生成可执行的下一步，如 PlanCard / ask_user / capability action |
| `failed` | Planner 或校验失败，已降级为可解释错误或追问 |
| `canceled` | 用户取消当前规划或上层 pending 被取消 |
| `expired` | 关联 Plan / Trace / contract 已过期，不能继续确认 |

第一版必须支持 `planning` / `completed` / `failed`；`canceled` / `expired` 可先复用终态样式，但语义必须保留。

完成后：

```json
{
  "kind": "planner_activity",
  "trace_id": "ptrace_...",
  "status": "completed",
  "summary_steps": [
    {"state": "done", "label": "识别目标：赛博朋克视频"},
    {"state": "done", "label": "匹配能力：视频生成"},
    {"state": "done", "label": "生成方案"}
  ],
  "linked_message_id": 12345
}
```

`linked_message_id` 可指向后续 PlanCard message。

## 7. 存储策略

PlannerTrace 不应污染普通会话消息流。

建议：

```text
普通 messages
  -> 用户可见 PlannerActivity 摘要、PlanCard、ReviewCard、ResultCard

planner_traces
  -> 完整结构化 Trace
  -> conversation_id / message_id / plan_id / trace_id 关联
```

查询方式：

- 按 `trace_id` 调试。
- 按 `plan_id` 找生成该 Plan 的规划依据。
- 按 `message_id` 展开某条 PlannerActivity。
- 按 `conversation_id` 审计一轮对话中的规划过程。

普通会话列表默认不拉完整 Trace。

## 8. TTL 与清理

第一版建议：

| Trace 级别 | TTL |
| --- | --- |
| `summary` | 跟随 message 生命周期 |
| `detail` | 30 天 |
| `debug/internal` | 7 天或更短 |

如果成本或隐私要求更高，可把 detail TTL 降到 7 天。

清理规则：

- 删除 detail/debug 不影响普通 message 展示。
- Plan 仍保留最终 `ActionPlan` 必要审计字段和 contract pin。
- 如果 trace 过期，展开详情返回“规划详情已过期”，不能伪造重建。

## 9. 隐私与脱敏

Trace payload 可能包含用户输入摘要、slot 值、资产引用、validator 错误。

规则：

- 存 `asset_id` / `object_ref`，不存大体积原始资产。
- 对敏感用户文本做摘要或截断。
- 不记录原始 LLM CoT。
- 不记录完整系统 prompt。
- 不记录 callback token、user token、secret。
- `system_injected` 字段只记录存在性或 hash，不展示值。

Payload 白名单：

- `trace_id`、`conversation_id`、`message_id`、`plan_id`。
- `skill_key`、`goal`、`next_action`。
- `field`、`value_source`、脱敏后的 `value_summary`。
- `assumption_id`、`confidence`、`confirmable`、用户可见 `reason`。
- `candidate_skill_keys`、候选分数摘要、仲裁原因。
- `validation_code`、脱敏后的校验失败原因。
- `asset_id`、`object_ref`、`task_id` 的引用 ID。

Payload 黑名单：

- 原始 CoT、原始 LLM reasoning、完整 LLM raw output。
- system prompt、developer prompt、tool credentials。
- callback token、access token、secret、authorization header。
- 未脱敏手机号、邮箱、身份证、支付信息等 PII。
- 大体积原始图片、视频、音频、文件内容。
- validator stack trace、SQL、内部路径、环境变量。

## 10. 假 Trace 禁止

每条用户可见规划过程必须来自真实 event。

禁止：

```text
为了显得智能，前端固定写“正在深度思考...”
Planner 没有做 Skill 仲裁，却展示“比较了多个 Skill”
没有 validator 检查，却展示“已完成校验”
Engine 没有开始生成图片，却展示“正在生成第 1 张”
```

如果某阶段太快，可以直接展示最终摘要，不需要人为延迟或伪造过程。

## 11. Engine / Capability Trace

PlannerTrace 只覆盖规划阶段。

执行阶段的 Tool / Capability Trace 来自真实 Engine / Workflow / Capability 事件：

```text
[Skill] video_gen
[Workflow] 创建任务
[Prompt Compiler] 生成分镜描述
[Image Generation] 生成第 1/9 张
[Video Generation] 生成第 1/8 段
[FFmpeg] 合成成片
```

这些事件可以在 UI 上与 PlannerActivity 组成连续体验，但来源必须区分：

```text
PlannerTrace.Source = planner
EngineTrace.Source = engine_observer
CapabilityTrace.Source = capability_runtime
```

Planner 不得编造执行进度。

## 12. Validator 失败展示

如果 `ActionPlan` 校验失败：

Trace 记录：

```text
Validation: rejected bgm_style because enum mismatch
Decision: ask_user for bgm_style
```

用户可见：

```text
我需要确认音乐风格后再生成方案。
```

不展示：

```text
validator stack trace
raw schema parse failure
internal Go error
```

## 13. 示例：赛博朋克视频

Trace：

```text
Observation(summary): 识别目标 create_video
Observation(summary): 匹配 Skill video_gen
Observation(detail): video_gen score=0.90, short_drama score=0.45
Observation(summary): bgm_style 缺失
Assumption(summary): 推荐 bgm_style=synthwave_electronic_bass
Validation(detail): required inputs covered
Decision(summary): create_plan_card
```

PlannerActivity：

```text
识别目标：赛博朋克视频
匹配能力：视频生成
检查输入：缺少音乐风格
自动推荐：电子重低音 / Synthwave
生成方案
```

PlanCard 只读取已校验 `ActionPlan`，不读取原始 trace 文本。

## 14. 回归要求

必须覆盖：

- PlannerActivity 使用同一 `message.id` upsert。
- Trace 不进入普通 message 正文。
- 完整 Trace 可按 `trace_id` 查询。
- PlanCard 可关联生成它的 `trace_id`。
- 原始 CoT 不落库、不推送。
- 假 Trace 不允许通过测试快照。
- validator 失败时用户可见摘要降级，不展示内部错误。
- Engine 事件与 Planner 事件来源可区分。
- Trace TTL 过期后不伪造重建。

## 15. 裁决

V2.4 第一版采用：

- 结构化 `PlannerTrace`。
- `PlannerActivity` 通过现有 WS message upsert 展示摘要。
- 完整 Trace 独立存储，默认懒加载。
- 禁止原始 CoT streaming。
- 禁止原始 CoT 持久化。
- 禁止假 Trace。
- PlannerTrace 与 Engine / Capability Trace 来源强区分。
