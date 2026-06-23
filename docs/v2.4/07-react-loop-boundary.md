# 07 · ReAct Loop Boundary

## 1. 问题定位

V2.4 引入 Planner，但第一版不能变成自由 autonomous agent。

它必须保护 V2.2/V2.3 已经建立的语义边界：

- 修改不是新目标。
- 取消不是 Skill selection。
- 再来一版不是全局重路由。
- out_of_scope 不创建无关 Skill。
- review 修改必须沿用 V2.3 review artifact / capability 语义。

一句话：

```text
V2.4 Planner 负责规划下一步，但不接管所有对话状态机。
```

## 2. 第一版 ReAct 边界

第一版不做：

- 自由 LLM 工具调用。
- MCP Server。
- 动态外部工具发现。
- 跨 Skill 自动组合。
- 长时间 autonomous loop。
- 多轮无用户确认自动执行。
- 原始 Thought streaming。

第一版只做受控循环：

```text
Observe structured context
  -> Plan one next action
  -> Validate
  -> Emit Decision
  -> Wait for user or engine event
```

每轮 Planner 最多输出一个 `NextAction`。

“一个 Decision”在 V2.4 中指一个主业务决策。一次响应可以包含说明消息 + 卡片，例如先解释为什么需要切换确认，再生成 `goal_switch_confirmation` pending 卡片；但不能在同一轮同时推进两个互相独立的业务动作。

## 3. Planner 入口矩阵

| Turn / Operation | Planner 行为 |
| --- | --- |
| `start_goal` | 可进入 Skill selection 和输入规划 |
| `answer_question` | 只填 pending 字段，延续原 Skill / plan |
| `request_modification` | 进入 V2.3 target / operation，不全局重选 Skill |
| `provide_modification` | 延续当前修改请求，不当新目标 |
| `cancel` | 输出 cancel action 或调用 cancel capability，不选 Skill |
| `regenerate` | 继承 current plan / result，规划 regenerate |
| `confirm` | 走 confirm plan / review / result action，不重新规划 |
| `chitchat` | 回复文本或轻量引导，不进入 Skill selection |
| `out_of_scope` | 锚回当前任务或拒绝解释 |
| `goal_switch_confirmation` | 当前有 blocking task 且命中新 Skill 时，先等待用户确认是否切换 |

## 4. Blocking Context

blocking context 指用户离开当前任务会造成状态混乱或资源浪费的场景：

- `PendingInteraction` 正在等待回答。
- `StageReviewing` 正在等待 ReviewCard 确认。
- task 正在 executing / suspended / awaiting。
- PlanCard 已生成但未确认。
- 当前 ResultCard 支持继续修改或再来一版。

如果 blocking context 存在，而用户输入命中新 Skill，Planner 不能静默切换。

必须先问：

```text
当前任务还没完成。你是想切换到新任务，还是继续当前任务？
```

此时应创建 `PendingInteraction.Kind = goal_switch_confirmation` 或等价 pending 语义，记录：

- current target / task / plan。
- proposed new skill / goal。
- 用户可选动作：继续当前任务、切换到新任务、取消当前任务后切换。

只有用户明确切换后，才取消、暂停或保留当前任务并进入新 Skill selection。具体任务处理由 V2.3 capability / task lifecycle 裁决。

## 5. 修改链路

Review 阶段修改：

```text
ReviewCard waiting
  -> user request modification
  -> TargetResolver = current review artifact
  -> OperationIntent = revise
  -> Planner does not global skill selection
  -> CapabilityPolicy = revise_review_by_fork
  -> old task canceled / await binding canceled
  -> replacement PlanCard or ReviewCard flow
```

Plan 阶段修改：

```text
PlanCard waiting
  -> user edits or says "改成横版"
  -> apply user override
  -> validate ActionPlan
  -> refresh PlanCard
```

Result 阶段修改：

```text
ResultCard
  -> user says "把这个改成..."
  -> TargetResolver = current result object
  -> OperationIntent = revise/regenerate
  -> use object capability if registered
```

这些都不是新目标 Skill selection。

## 6. 取消链路

取消优先处理当前 pending / task。

```text
user cancel
  -> if pending: clear pending
  -> if PlanCard waiting: mark plan canceled/stale
  -> if ReviewCard waiting: use V2.3 task cancel + await cleanup
  -> if executing: call cancel capability / task lifecycle
  -> do not run Skill selection
```

取消完成后，Planner 可输出解释或状态更新，但不能自动创建新 Plan。

## 7. 再来一版

“再来一版”必须继承当前上下文：

```text
current result / plan / skill
  -> copy safe slots
  -> preserve user overrides
  -> expire old assumptions if no longer valid
  -> create regenerate ActionPlan
```

不得把“再来一版”当作全局新目标。

如果用户补充了新要求：

```text
再来一版，音乐换成钢琴
```

则：

```text
plan carryover + user override(bgm_style=soft_piano)
```

## 8. out_of_scope

out_of_scope 不进入新 Skill selection。

当前有任务时：

```text
用户: 今天天气怎么样？
Agent: 我现在不能查询天气。当前短剧脚本还在等你确认，要继续修改或确认吗？
```

当前无任务时：

```text
Agent: 我现在主要支持图片、视频和短剧创作。你想创建哪一种？
```

不得选择不存在的 weather Skill，不得自由调用外部工具。

## 9. Goal Switch

如果用户明确要切换任务：

```text
用户: 不管刚才的短剧了，我想做一张图
```

流程：

```text
Detect possible switch
  -> if current task is blocking, ask confirm unless user explicitly abandoned
  -> if confirmed, apply cancel/stale policy to current context
  -> start new Skill selection
```

显式放弃语句可以减少确认，但仍必须保证旧 task / await / pending 状态不会悬挂。

## 10. Planner Loop 限制

每个用户回合：

- 最多一次全局 Skill selection。
- 最多一次 InputPlanningPolicy pass。
- 最多输出一个主业务决策，可包含说明消息 + 卡片。
- 不自动连续调用多个 Capability。
- 不在同一回合里“追问又自动执行”。

如果 validator 失败：

```text
record Trace
  -> ask_user / refuse_or_explain
  -> stop
```

不得反复自我修正进入不可控循环。

## 11. Capability 调用边界

`NextAction=invoke_capability` 只允许用于 V2.3 已注册能力：

- target object 明确。
- operation 明确。
- capability binding 存在。
- policy 允许。
- idempotency key 可构造。
- side effect 边界清晰。

Planner 不直接调用 Workflow 或 Engine。它输出 ActionPlan，由 runtime adapter / capability invoker 执行。

## 12. 与 PlannerTrace 的关系

ReAct 边界必须写入 Trace：

```text
Observation: current stage=reviewing
Observation: user input matched possible new skill image_gen
Decision: ask user to confirm goal switch because review task is blocking
```

这类 Trace 用于解释为什么 Agent 没有立即切换任务。

## 13. short_drama 零回退

必须保护：

- 短剧创建 PlanCard。
- 短剧 prompt review。
- Review 修改 by fork。
- task cancel + await cleanup。
- ResultCard 再来一版。
- outbox dedup。

任何 V2.4 Planner 错误都不能静默回退到旧硬编码分支，也不能让旧 awaiting task 被继续推进。

## 14. 示例：Review 中突然做图

当前：

```text
Stage = reviewing
CurrentSkill = short_drama
ReviewCard waiting
```

用户：

```text
我想做一张图
```

输出：

```text
NextAction = ask_user
Question = 当前短剧还在等待你确认分镜。你是想先暂停这个短剧，改做图片，还是继续处理当前短剧？
```

不直接选择 `text_to_image`。

## 15. 示例：取消

当前：

```text
PendingInteraction = asking bgm_style
```

用户：

```text
算了
```

输出：

```text
NextAction = cancel
Clear pending
No Skill selection
```

## 16. 回归要求

必须覆盖：

- start_goal 进入 Skill selection。
- answer_question 延续 pending。
- PlanCard 修改刷新 ActionPlan，不新建 Skill。
- Review 修改走 V2.3 capability。
- cancel 不进入 Skill selection。
- regenerate 继承 current plan/result。
- out_of_scope 锚回当前任务。
- blocking context 下新 Skill 命中先追问是否切换。
- 明确 abandon 后允许新目标，但旧 task 状态被正确处理。
- 每轮最多一个 user-facing Decision。
- validator 失败停止，不进入自循环。
- `short_drama` 闭环零回退。

## 17. 裁决

V2.4 第一版采用受控单步 Planner：

- 每轮只规划一个 next action。
- 不自由调用工具。
- 不跨 Skill 自动组合。
- blocking context 下不静默切换任务。
- 修改、取消、再来一版优先尊重 V2.2/V2.3 语义。
- Planner 错误不静默回退 legacy。
