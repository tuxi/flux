# 06 · Review Revision By Fork

## 1. 产品语义

当用户在 Review 阶段要求修改时，当前运行已经被新的修改意图替代。

业务语义：

```text
superseded
```

Engine 操作状态：

```text
canceled
```

用户不应看到“创作失败”。应看到：

```text
已根据你的修改要求创建新版方案，本次运行已停止。
```

## 2. 目标真实对话

```text
Agent: 确认分镜脚本
用户: 修改一下
Agent: 想怎么修改当前分镜脚本？
用户: 把风格改为电影风格
```

解释结果：

```text
DialogueAct = provide_modification
TargetObject = 当前 prompt_review_card / review_artifact
Operation = revise
Feedback = 把风格改为电影风格
Capability = revise_review_by_fork
```

## 3. 当前短剧 Review 绑定

真实链路：

```text
drama_storyboard_dsl.emit_prompt_review
  (ai-engine/workflows/short_drama/drama_storyboard_dsl.go:129)
  -> emit_pipeline_event(event_type="await_user_action",
                         card_type="prompt_review_card",
                         stage="awaiting_prompt_review",
                         payload=storyboard)
  -> Observer.handleReviewGate
     (ai-engine/agent/observer/observer.go:257)
  -> append review_card(kind=review_card, card_type=prompt_review_card, signal=confirm_storyboard_prompt)
  -> AgentState.stage=reviewing
  -> AgentState.pending_message_id=review_card.id

drama_storyboard_dsl.await_prompt_review
  (ai-engine/workflows/short_drama/drama_storyboard_dsl.go:145)
  -> await_bindings(task_id, node_name="await_prompt_review",
                    await_type=user_input,
                    source=signal,
                    signal_name=confirm_storyboard_prompt,
                    callback_token=task_id,
                    status=waiting)
```

`callback_token` 由 manifest 的 `input.callback_token` 注入，当前 `ConversationService.routeEngineSignal` 使用最新 task id 作为 token。

## 4. 第一版完整执行流程

```text
1. TargetResolver
   当前目标 = prompt_review_card / review_artifact

2. OperationInterpreter
   操作 = revise
   feedback = 把风格改为电影风格

3. CapabilityPolicy
   允许 revise_review_by_fork

4. CapabilityInvoker
   验证目标 Review 仍有效
   验证 Task 当前确实处于 awaiting/reviewing 对应状态
   取消当前 Task
   取消可取消 child Task
   取消等待/运行中的 NodeRuntime
   取消 await binding
   清理旧 pending_message_id
   使旧 ReviewCard stale
   更新 Activity 为 superseded 语义
   基于当前 Plan + feedback 创建新版 Plan
   返回新版 PlanCard

5. 用户确认新版 Plan
   走现有 confirm_plan
   outbox dedup = create_task:plan:{new_plan_id}
   因 conversation 已有旧 task，task_link relation=fork
```

## 5. 新版 Plan 如何记录反馈来源

当前 `ConversationPlanModel` 只有 `SlotsJSON`，暂无 metadata 字段。第一版可在 slots 中采用保守扩展字段，实施前需要产品评审确认字段名：

```json
{
  "user_prompt": "...",
  "style": "电影风格",
  "_revision": {
    "kind": "review_feedback",
    "feedback": "把风格改为电影风格",
    "revised_from_plan_id": "old_plan_id",
    "revised_from_task_id": "old_task_id",
    "revised_from_message_id": "review_card_message_id"
  }
}
```

更推荐后续新增 plan metadata 或 relation 表：

```text
plan_revision_links(
  new_plan_id,
  revised_from_plan_id,
  revised_from_task_id,
  revised_from_message_id,
  feedback,
  reason
)
```

V2.3 文档不要求现在改表，但实施计划必须选定一种落地方式。

## 6. Activity 如何表达 superseded

当前 activity 只有：

```text
running / waiting_user / completed / failed
```

第一版需要新增业务展示状态或 content 扩展：

```json
{
  "status": "superseded",
  "current_step": "review_prompt",
  "superseded_by_plan_id": "new_plan_id",
  "summary": "已根据你的修改要求创建新版方案，本次运行已停止。"
}
```

`HeadlineText()` 应返回：

```text
已创建新版方案
```

不要调用 `Activity.Fail()`，不要 append `error_card`。

## 7. 旧 ReviewCard 如何失效

旧卡失效由三层共同保证：

1. `AgentState.PendingMessageID` 不再指向旧 review card。
2. `await_bindings.status` 从 `waiting` 变为 `canceled`。
3. card content 可追加/派生 `action_state=stale/superseded`，客户端隐藏按钮。

即便客户端重复提交旧 signal：

- `ConversationService.routeEngineSignal` 应先校验 ref_message_id 是否当前 pending。
- `AwaitHandler.FindWaitingBySignal` 只查 waiting binding，canceled 不会命中。
- `CompleteAwaitNode.ClaimCompleting` 只允许 waiting，重复恢复为 noop。

## 8. 确认后如何启动 Fork

现有 confirm 机制已经支持 fork：

- `ConversationService.confirmPlan` 在确认新 Plan 时读取当前最新 task。
- 若存在旧 task，则 outbox payload 写 `forked_from`，见 `ai-engine/agent/service/conversation_service.go:442`。
- `OutboxWorker.processCreateTask` 创建 `TaskLink{Relation:"fork", ForkedFromTaskID:&from}`，见 `ai-engine/agent/worker/outbox_worker.go:169`。
- launcher 创建的新 task 应保留 fork 关系。

V2.3 需要保证新版 Plan 确认时，旧 canceled task 仍是 latest task link 或显式作为 `forked_from` 来源。若后续引入多 task 并发，不能只依赖“最新一条 link”，需要在 plan revision relation 中明确 `forked_from_task_id`。
