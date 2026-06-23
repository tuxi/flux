# 11 · Regression Matrix

## 1. Target / Operation

| ID | 初始状态 | 输入 | 预期 |
| --- | --- | --- | --- |
| T01 | `reviewing` + active `prompt_review_card` | 修改一下 | `target=review_artifact`，追问修改内容 |
| T02 | `reviewing` + active `storyboard_review_card` | 修改一下 | `target=review_artifact`，追问当前画面修改内容 |
| T03 | `awaiting_user` + `collect_modification(Target=review_artifact)` | 把风格改为电影风格 | `operation=revise`，feedback 保留原文 |
| T04 | `completed` + result_card | 改一下 | 目标为 latest result 或 current plan，不误指旧 review_card |
| T05 | no active object | 修改一下 | unknown/clarify，不调用 capability |

## 2. Capability Policy

| ID | 条件 | 预期 |
| --- | --- | --- |
| P01 | review_artifact + revise + feedback 非空 | `available / revise_review_by_fork` |
| P02 | review_artifact + revise + feedback 为空 | `unavailable / missing_feedback` |
| P03 | binding completed | reject `await_binding_not_waiting` |
| P04 | task canceled | reject `target_stale` |
| P05 | old review card 不是 pending card | reject `target_stale` |
| P06 | capability 未注册 | reject `capability_not_allowed` |
| P07 | plan + update_field(aspect_ratio) | `available / modify_plan` |
| P08 | plan/result + regenerate | `available / regenerate_plan` |
| P09 | unsupported target/operation | `unavailable / unsupported_capability` |

## 3. Review Revision By Fork

| ID | 场景 | 预期 |
| --- | --- | --- |
| R01 | 回答具体反馈 | 取消旧 task，取消 await binding，创建新版 PlanCard |
| R02 | 旧 ReviewCard 再次确认 | stale/rejected，不 resume 旧 task |
| R03 | 旧 Task recovery 扫描 | 不重新 enqueue，不恢复执行 |
| R04 | AwaitPollWorker 扫描 | canceled binding 不被 poll/timeout 处理 |
| R05 | 重复发送相同修改反馈 | 只创建一个新版 Plan，返回同一 capability result |
| R06 | 同一旧卡不同反馈并发 | 只有一个成功，另一个 target_stale/conflict |
| R07 | 取消旧 task 失败 | 不创建新版 Plan，返回明确错误 |
| R08 | 取消 await binding 失败 | 不创建新版 Plan，task 状态需可回滚或进入可恢复错误 |
| R09 | 新版 Plan 用户取消 | 不启动 Fork Task |
| R10 | 新版 Plan 用户确认 | 启动新 task，task_link `relation=fork` |

## 4. Task / Await / Worker

| ID | 初始状态 | 操作 | 预期 |
| --- | --- | --- | --- |
| W01 | task pending | superseded cancel | task canceled，queue worker 不执行 |
| W02 | task running | superseded cancel | task canceled，Engine 后续终态写入被 `ErrTaskCanceled` 阻止 |
| W03 | task suspended + binding waiting | superseded cancel | task canceled，binding canceled |
| W04 | child task pending/running/suspended | cancel parent | child tasks canceled |
| W05 | node running/awaiting | cancel | node state `NodeCanceled` |
| W06 | binding waiting + next_poll_at due | cancel | poll worker 不处理 |
| W07 | binding waiting + old signal | cancel 后 signal | FindWaitingBySignal 不命中 |

## 5. UI / Activity / Result

| ID | 场景 | 预期 |
| --- | --- | --- |
| U01 | Activity 等待 Review | 修改成功 | Activity 显示“已根据你的修改创建新版方案”，不是“创作失败” |
| U02 | 旧 ReviewCard | 修改成功后 | 按钮不可操作或 signal stale |
| U03 | 旧 task canceled | Observer 收到残留事件 | 不 append error_card/result_card |
| U04 | 新 PlanCard | 修改成功后 | `pending_message_id` 指向新版 PlanCard |
| U05 | conversation status | 修改成功后 | `awaiting_user` / confirming 投影，不显示 failed |

## 6. V2.2 兼容

| ID | 输入 | 预期 |
| --- | --- | --- |
| C01 | 普通 start_goal | 仍生成 PlanCard |
| C02 | plan confirming + 改成横屏 | 仍走普通 modify_plan |
| C03 | awaiting_user missing story brief | 回答后仍生成 Plan |
| C04 | smalltalk while pending | 保留 pending |
| C05 | cancel pending collect_modification | 清 pending，不取消非目标 task |

## 7. 数据一致性

| ID | 检查 | 预期 |
| --- | --- | --- |
| D01 | 新 Plan | 记录 revised_from plan/task/message 和 feedback |
| D02 | 旧 Plan | 标记 revised/superseded 或可通过 relation 查询 |
| D03 | TaskLink | 新 task link relation=fork，forked_from=旧 task |
| D04 | Idempotency | capability call 有稳定 dedup key |
| D05 | Outbox | confirm 新 Plan dedup `create_task:plan:{new_plan_id}` |
