# 10 · Implementation And Migration Plan

本计划只描述实施顺序。评审通过前不写 V2.3 业务实现代码。

## Phase 0 · 评审确认

必须确认：

- 是否接受 Review 修改第一版采用 `revise_review_by_fork`。
- 是否接受 `task.status=canceled + reason=superseded_by_revision` 表达 Engine 终止。
- Activity 是否新增 `superseded` 状态。
- Plan revision 来源记录放在 slots 扩展还是新增 relation/metadata 表。
- 内部取消原语是否允许绕过用户手动取消的 1 分钟/15 分钟限制。

## Phase 1 · 纯模型与 shadow

新增但不执行副作用：

- `ObjectRef`
- `ActiveObject`
- `ActiveObjectResolver`
- `TargetResolver`
- `OperationIntent`
- `OperationInterpreter`
- Capability registry
- CapabilityPolicy dry-run
- shadow 日志

目标：

```text
reviewing + 修改一下
  -> target=review_artifact
  -> operation=revise/missing_feedback
```

```text
pending collect_modification + 把风格改为电影风格
  -> target=review_artifact
  -> capability=revise_review_by_fork(dry-run)
```

## Phase 1.5 · OperationIntent 与 CapabilityPolicy dry-run

V2.3-2 只产出 `OperationIntent`：

```text
target=review_artifact
user=把风格改为电影风格
  -> operation=revise
  -> feedback=把风格改为电影风格
```

V2.3-3 只产出 `CapabilityPolicyDecision`：

```text
review_artifact + revise + empty feedback
  -> unavailable / missing_feedback

review_artifact + revise + feedback
  -> available / revise_review_by_fork

plan + update_field(aspect_ratio)
  -> available / modify_plan

plan|result + regenerate
  -> available / regenerate_plan
```

这两个阶段都不得调用 capability，不得触发 Engine 生命周期副作用。

## Phase 2 · PendingInteraction 迁移

- 给 `PendingInteraction` 增加 `Target *ObjectRef`。
- 写入 collect_modification 时保存 target。
- 读取旧数据时从 `TargetPlanID` 兼容派生 plan ref。
- 更新 invariant：pending target 必须可 JSON round-trip。
- 更新 repository tests。

## Phase 3 · 内部取消原语

新增内部 service，不接 Agent：

```text
CancelRunForSupersededRevision(taskID, reason, idempotencyKey)
```

必须覆盖：

- root task canceled
- child task canceled
- node runtime canceled
- await binding canceled
- 不发 failed/final_failed
- 幂等重复调用安全

优先复用：

- `WorkflowHandler.cancelChildTasksRecursive` 的 task/node 取消思路。
- `taskRetryService.resetAwaitBindingsForRetry` 的 await binding 取消思路。

但不要复用用户取消 API 的时间限制与文案。

## Phase 4 · Activity/ReviewCard stale 表达

- Activity 支持 `superseded` 或 content 扩展。
- 旧 ReviewCard 能被标记 stale/superseded，或由 state/pending 派生不可操作。
- Observer 不把 superseded task 显示成 failed。
- 客户端根据 `pending_message_id` 和/或 action_state 隐藏旧按钮。

## Phase 5 · CapabilityInvoker

实现 `revise_review_by_fork`：

```text
validate target
validate task/await binding
cancel old run
mark review/activity superseded
create revised plan
append PlanCard
return CapabilityResult
```

所有写操作必须具备幂等键。

## Phase 6 · V2.2 主链路接入

- `DialoguePolicy` 支持 `DirectiveInvokeCapability`。
- `DialogueDecisionBuilder` 支持 CapabilityResult -> Decision。
- Review 阶段修改优先走 V2.3。
- 普通 plan 修改继续走旧路径。

## Phase 7 · 回归与灰度

- 先 shadow。
- 再只对 `prompt_review_card` 开启。
- 再扩展到 `storyboard_review_card`。
- 最后考虑 result/task 层能力。

## Phase 8 · 后续优化

- Workflow 原地 revision 能力成熟后，替换 `revise_review_by_fork` 内部实现。
- 外层 Object/Capability 架构保持不变。
- 再评估 Function Calling / MCP。
