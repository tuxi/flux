# 00 · Object Semantics Overview

## 1. 问题定位

真实失败场景：

```text
Agent: 确认分镜脚本
用户: 修改一下
Agent: 想怎么调整？
用户: 把风格改为电影风格
Agent: 退回短剧入口兜底
```

V2.2 已经能识别 `request_modification` 与 `provide_modification`，但当前语义仍停在 plan 层：

- `TurnInterpretation.Target` 是 `TargetCurrentPlan`。
- `PendingInteraction` 只有 `TargetPlanID`。
- `DialoguePolicy` 的 `DirectiveModifyPlan` 只能创建新版 Plan。
- ReviewCard、Task、await binding、Activity 之间没有被建模成当前回合的可操作对象。

因此系统知道“用户在修改”，但不知道“用户在修改当前等待审核的 prompt review artifact”，也不知道“这个对象当前应调用 `revise_review_by_fork`”。

## 2. Planner 定位

V2.3 是 Planner 的地基。

```text
Agent = LLM / Reasoning + Planning + Memory + Tools
```

DreamAI 当前对应关系：

- Reasoning：`TurnInterpreter`、未来 LLM Interpreter、`OperationInterpreter`、Creative Planner。
- Planning：`DialoguePolicy`、`TargetResolver`、`OperationInterpreter`、`CapabilityPolicy`、未来 `ActionPlan`。
- Memory：`AgentState`、`PendingInteraction`、`CurrentPlan`、`TaskLinks`、conversation history。
- Tools：Skill、Workflow、Capability、Engine Task、Review Gate。

V2.1 的 Skill Contract 解决工具和技能注册边界；V2.2 的 Conversation Semantics 解决“用户这一轮在做什么”；V2.3 的 Object/Operation Semantics 解决“当前世界里有什么对象、用户动作作用于谁、动作意图是什么”。

所以 V2.3 不是为 ReviewCard 打补丁，而是在给 Planner 搭可感知世界和可执行动作空间。第一阶段必须允许系统先“看懂对象和操作”，再进入 capability 和 Engine 生命周期副作用。

## 3. 真实代码调查结论

当前代码事实：

| 机制 | 代码位置 | 结论 |
| --- | --- | --- |
| V2.2 主流程 | `ai-engine/agent/runtime/runtime.go`, `turn_interpreter.go`, `dialogue_policy.go`, `dialogue_decision_builder.go` | 默认主链路已是 `TurnInterpreter -> SkillSufficiencyEvaluator -> DialoguePolicy -> DialogueDecisionBuilder`。 |
| PendingInteraction | `ai-engine/agent/domain/types.go` | 当前仅有 `Kind`、`AskedSlot`、`TargetPlanID`、`PromptMessageID`，无法指向 review artifact/task/message。 |
| ReviewCard 生成 | `ai-engine/agent/observer/observer.go:257` | Observer 收到 gate event 后 append `review_card`，并把 `AgentState.Stage` 置为 `reviewing`，`PendingMessageID` 指向该卡。 |
| 短剧 prompt review | `ai-engine/workflows/short_drama/drama_storyboard_dsl.go:129` | `emit_prompt_review` 发 `card_type=prompt_review_card`；`await_prompt_review` 创建 `user_input/signal` await binding。 |
| Gate 声明 | `ai-engine/agent/skill/manifests/short_drama.yaml` | `prompt_review_card -> confirm_storyboard_prompt`，标题为“确认分镜脚本”。 |
| Signal 路由 | `ai-engine/agent/service/conversation_service.go:319`, `ai-engine/handler/await_handler.go:124` | `PostSignal` 只在 `StageReviewing` 转发 engine gate signal；await handler 只查 `status=waiting` 的 binding。 |
| Await 完成 | `ai-engine/engine/await_complete.go:34` | `CompleteAwaitNode` 只从 `waiting` 原子 claim 到 `completing`，然后 resume task。非 waiting 不会恢复。 |
| Task 状态 | `ai-engine/domain/task_status.go:3` | `pending/running/suspended` 可进入 `canceled`。`canceled` 是 terminal，不允许恢复到 running。 |
| Worker 抢占 | `ai-engine/repository/query/task.go:507`, `ai-engine/worker/worker.go:133` | Worker 只 claim pending 或超时 running；加载后发现 `TaskCanceled` 会 ack 丢弃。 |
| Recovery | `ai-engine/worker/recovery_scanner.go:98` | recovery 对 `TaskCanceled`/`cancelled` 只取消运行节点，不重试恢复任务。 |
| Await poll | `ai-engine/repository/query/await_binding.go:212` | `FindPollDue` / `FindTimeoutDue` 只扫描 `status=waiting`。 |
| 现有取消 API | `ai-engine/handler/workflow_handler.go:756` | 取消 task/node/children，但没有同步取消 await binding，并带用户手动取消等待限制。不能直接作为 V2.3 内部 capability。 |
| retry 清理 await | `ai-engine/service/task_retry_service.go:199` | 已有取消 in-flight await binding 的局部逻辑，可作为内部取消原语的参考。 |
| Activity | `ai-engine/agent/activity/activity.go`, `observer.go` | 只有 running/waiting_user/completed/failed，暂无 superseded 状态。失败会显示“创作失败”。 |
| Fork 启动 | `ai-engine/agent/service/conversation_service.go:442`, `agent/worker/outbox_worker.go:169` | 新 Plan 确认时 outbox dedup by `create_task:plan:{plan_id}`；如果已有当前 task，则 task_link `relation=fork`。 |

## 4. 第一版核心裁决

Review 阶段的“修改”不等于普通 plan 修改。它替代了一个已经进入 Engine await 的运行。

产品语义：

```text
old run = superseded
```

Engine 操作状态：

```text
old task.status = canceled
old await_binding.status = canceled
```

两者必须同时成立。`superseded` 负责 UI 与业务解释，`canceled` 负责阻止 Worker、AwaitPollWorker、signal 和 recovery 继续推进旧运行。

## 5. V2.3 新增边界

V2.3 新增四层：

1. Object Semantics：把 Plan、ReviewCard、Task、Result、Activity 建模为当前可操作对象。
2. Operation Semantics：把“把风格改为电影风格”解释为针对目标对象的修订意图。
3. Capability Policy：只暴露当前对象、当前 stage、当前状态允许的能力。
4. Capability Runtime：执行副作用、保证幂等、输出结构化结果并回写 Decision。

V2.2 保持职责边界：只判断这一轮用户在做什么，不继续承载对象能力逻辑。

## 6. 关键红线

必须能回答并落实：

```text
如何确保被新版替代的 awaiting Task 不会再次被 Worker 执行或恢复？
```

答案必须包含：

- root task 和相关可取消 child task 进入 `TaskCanceled`。
- 对应 waiting/pending await binding 进入 `AwaitBindingCanceled`，清理 `NextPollAt` 等恢复入口。
- `AgentState.PendingMessageID` 不再指向旧 ReviewCard。
- 旧 ReviewCard 的 signal 再次提交时无法命中 waiting binding，返回 stale/noop。
- Observer 不把 superseded 运行显示成 failed/error_card。
