# 04 · Capability Registry And Policy

## 1. CapabilityDescriptor

Capability 必须由对象类型、对象状态、Agent stage、用户权限与 Skill contract 共同决定。

建议模型：

```go
type ApprovalPolicy string

const (
    ApprovalNone     ApprovalPolicy = "none"
    ApprovalExplicit ApprovalPolicy = "explicit"
)

type CapabilityDescriptor struct {
    Name          string         `json:"name"`
    Description   string         `json:"description"`
    InputSchema   any            `json:"input_schema"`
    TargetTypes   []ObjectType   `json:"target_types"`
    AllowedStages []string       `json:"allowed_stages"`
    Approval      ApprovalPolicy `json:"approval"`
}
```

禁止向 Agent 暴露全局工具列表。Agent 只能看到当前 ActiveObject 允许的能力。

## 2. 第一批内部能力

第一版注册这些通用能力：

| Capability | Target | 副作用 | 第一版用途 |
| --- | --- | --- | --- |
| `inspect_object` | all | 否 | 调试和解释当前对象状态 |
| `confirm_review` | review_artifact | 是 | 等价当前确认 ReviewCard signal |
| `revise_review_by_fork` | review_artifact | 是 | Review 修改的核心路径 |
| `abandon_current_run` | task/activity | 是 | 放弃当前运行 |
| `modify_plan` | plan | 是 | 普通 plan 修改 |
| `regenerate_plan` | plan/result | 是 | 重新生成方案 |
| `cancel_task` | task | 是 | 普通任务取消 |

本阶段最关键的是 `revise_review_by_fork`。

## 3. CapabilityPolicy 输入

```go
type CapabilityPolicyInput struct {
    Context     *ConversationContext
    Target     TargetResolution
    Operation  OperationIntent
    Active     ActiveObject
    UserID     int64
}
```

输出：

```go
type CapabilityAvailability string
const (
    CapabilityAvailable   CapabilityAvailability = "available"
    CapabilityUnavailable CapabilityAvailability = "unavailable"
)

type CapabilityPolicyDecision struct {
    Availability CapabilityAvailability
    Capability   string
    Target       ObjectRef
    Operation    OperationKind
    Arguments    map[string]any
    ReasonCode   string
    Reason       string
}
```

V2.3-3 只输出这个 dry-run decision，不执行 `CapabilityCall`，不触发任何业务副作用。

## 4. `revise_review_by_fork` 允许条件

必须全部满足：

- Target type 为 `review_artifact`。
- conversation 属于当前 user。
- `AgentState.Stage=reviewing` 或 pending 为 `collect_modification` 且 target 指向该 review artifact。
- ReviewCard 仍是当前可操作卡，或 pending target 来源于该卡。
- task 属于当前 conversation 最新 active task。
- task 当前为 `suspended` 或可证明处于 review awaiting 状态。
- await binding 存在，`status=waiting`，`await_type=user_input`，`source=signal`。
- card_type/signal 与 Skill manifest gate 匹配。
- feedback 非空。
- IdempotencyKey 可计算。

## 5. 禁止条件

任一条件满足时禁止调用：

- target stale。
- await binding 已 completed/failed/timed_out/canceled。
- task 已 success/failed/canceled。
- ReviewCard 不是当前 pending card，且没有 matching pending target。
- 当前用户无权访问 conversation/task。
- capability 未注册。
- arguments 不符合 schema。
- 重复请求命中已完成幂等记录，应返回已有结果而不是再次执行。

## 6. 错误码

第一版建议错误码：

| ErrorCode | 含义 |
| --- | --- |
| `target_stale` | 目标对象已失效 |
| `capability_not_allowed` | 当前对象/状态不允许该能力 |
| `missing_feedback` | 缺少修改反馈 |
| `await_binding_not_waiting` | await binding 不在 waiting |
| `task_not_cancelable` | task 当前状态不能安全取消 |
| `cancel_failed` | 取消旧运行失败 |
| `revision_plan_create_failed` | 新版 Plan 创建失败 |
| `idempotency_conflict` | 幂等键参数冲突 |

## 7. V2.3-3 Dry-Run Mapping

第一版只做无副作用映射：

| Target | Operation | 条件 | Decision |
| --- | --- | --- | --- |
| `review_artifact` | `revise` | feedback 为空 | `unavailable / missing_feedback` |
| `review_artifact` | `revise` | feedback 非空 | `available / revise_review_by_fork` |
| `plan` | `update_field` | `path=aspect_ratio` | `available / modify_plan` |
| `plan` | `regenerate` | - | `available / regenerate_plan` |
| `result` | `regenerate` | - | `available / regenerate_plan` |
| 其它 | 其它 | - | `unavailable / unsupported_capability` |

V2.3-3 仍然禁止：

- 调用 `revise_review_by_fork`
- 创建或修改 Plan
- 取消 Task
- 清理 await binding
- 更新 Activity
- 标记 ReviewCard stale

完成 V2.3-3 后，系统只具备无副作用规划链路：

```text
DialogueAct -> TargetResolution -> OperationIntent -> CapabilityPolicyDecision
```
