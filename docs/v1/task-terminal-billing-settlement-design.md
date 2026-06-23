# Task Terminal Event Billing Settlement 设计

日期：2026-04-25

状态：Draft

关联代码：

- `ai-engine/worker/worker.go`
- `ai-engine/worker/await_poll_worker.go`
- `ai-engine/engine/engine.go`
- `ai-engine/engine/task_execution.go`
- `ai-engine/engine/await_complete.go`
- `ai-engine/engine/event_listen.go`
- `internal/service/billing_task_service.go`

## 1. 背景

当前任务计费结算主要挂在 `Worker.handle`：

1. `RunSuccess` 后调用 `ConsumeTask`
2. 达到自动重试上限后调用 `RefundTask`

这套模型在“任务一定由 `Worker` 启动并由 `Worker` 收口”的阶段可以工作，但随着 `await` 和 async runtime 落地，这个前提已经不成立。

当前仓库里，任务存在多条恢复执行路径：

1. `Worker` 从主队列拉起任务并执行
2. `AwaitPollWorker` 命中 `AwaitBinding` 后直接调用 `Engine.CompleteAwaitNode`
3. webhook / signal handler 也会直接调用 `Engine.CompleteAwaitNode`
4. `AsyncWorker` 发出 `node_complete_async` 事件后，由 `Engine` listener 调 `ResumeTask`

这些路径的共同点是：

1. 它们都可能推进任务进入最终状态
2. 但它们不一定会重新回到 `Worker`
3. 真正统一发出 `task_succeeded` / `task_failed` / `task_suspended` 的地方，其实是 `Engine`

因此，继续把结算绑定在 `Worker` 上，天然会漏掉 await / async / webhook 这类路径。

## 2. 当前问题

### 2.1 `Worker` 不再是唯一终态入口

在 await 场景下，当前链路是：

1. `Worker` 执行到 `await` 节点
2. `Engine.executeTask` 返回 `RunSuspended`
3. 后续由 `AwaitPollWorker` 或 webhook 触发 `CompleteAwaitNode`
4. `CompleteAwaitNode` 内部调用 `ResumeTask`
5. 引擎继续把任务跑完，并发出新的任务事件

这里真正让任务进入 `success` / `failed` 的，是引擎恢复执行过程，不是 `Worker`。

### 2.2 billing 放在 `Worker` 会产生路径不一致

如果结算只发生在 `Worker`：

1. 非 await 任务会结算
2. await 恢复后的成功可能不结算
3. await 恢复后的失败可能不退款
4. 不同入口的幂等语义会分散在多个 worker 中

### 2.3 当前 `task_failed` 还不是“稳定终态”

这点非常关键。

`Engine.executeTask` 在一次运行返回 `RunFailed` 时会立刻：

1. 将任务状态置为 `failed`
2. 发布 `task_failed`

但 `Worker.handle` 之后还会根据自动重试策略决定：

1. 是否 `PrepareTaskRetry`
2. 是否把任务重新置回 `pending`
3. 是否重新入队

这意味着：

1. 当前的 `task_failed` 表示“一次执行失败”
2. 它不一定表示“任务已经最终失败”

如果此时直接监听 `task_failed` 做退款，会导致：

1. 第一次失败就退款
2. 后续自动重试成功时出现“已退款后又成功”的账务错乱

所以“按 terminal event 结算”这个方向是对的，但必须先把失败事件语义收紧。

## 3. 目标

本设计目标：

1. 让计费结算脱离 `Worker` / `AwaitPollWorker` / `AsyncWorker` 等具体入口
2. 统一绑定到任务稳定终态
3. 保证结算幂等
4. 保持 AI Engine 核心执行逻辑不直接耦合 billing 规则

## 4. 设计原则

### 4.1 结算跟终态走，不跟入口走

谁触发了执行恢复并不重要，重要的是任务最终是否进入：

1. `succeeded`
2. `failed`

### 4.2 `task_suspended` 不是 billing terminal event

任务挂起只表示：

1. 当前执行段落结束
2. 后续需要 await / webhook / poll / signal 恢复

它不能触发扣点，也不能触发退款。

### 4.3 失败结算必须只绑定“最终失败”

自动重试窗口内的失败不应退款。

因此需要把：

1. 一次运行失败
2. 任务最终失败

明确区分开。

### 4.4 listener 必须幂等

计费 listener 不应依赖“事件只投递一次”。

它应只依赖：

1. `task_id`
2. `task_billing_records.status`

进行最终状态流转：

1. `frozen -> consumed`
2. `frozen -> refunded`

## 4.5 运行态不等于结算态

这是本设计里最容易被误用的一点。

工作流引擎里的运行状态，描述的是“任务当前跑到了哪里”；  
账务状态描述的是“这笔冻结资金当前应该如何处理”。

这两层状态有关联，但不是一一对应关系。

### 运行态 vs 结算态对照表

| 运行态 / 事件 | 运行语义 | 是否直接触发结算 | 推荐账务动作 |
| --- | --- | --- | --- |
| `task created` / 任务创建成功 | 任务已被系统接受，后续可调度 | 是 | `freeze` |
| `pending` | 待执行，可被 worker / resume / retry 拉起 | 否 | 不动账 |
| `running` | 当前正在执行 | 否 | 不动账 |
| `task_suspended` | 当前执行段结束，等待 await / webhook / signal / poll 恢复 | 否 | 不动账 |
| `task_failed` | 一次运行失败，可能还会自动重试或人工恢复 | 否 | 不动账 |
| `task_final_failed` | 自动重试窗口结束后的稳定失败 | 是 | `refund` |
| `task_succeeded` | 任务稳定成功，不会再回到 `pending/running` | 是 | `consume` |

### 为什么不能用 `pending -> freeze`

`pending` 只是调度状态，不是账务准入点。

问题在于一个任务生命周期里，可能多次回到 `pending`：

1. 自动重试后回到 `pending`
2. 人工 `resume` 后回到 `pending`
3. 某些恢复流程也可能重新置回 `pending`

如果监听 `pending` 直接冻结，就很容易出现重复冻结。

因此冻结应绑定：

1. 任务创建成功
2. billing quote / entitlement 校验通过
3. 初次进入可执行生命周期

也就是当前 `CreateTaskWithFreeze` 这类时机，而不是任意一次 `pending` 状态变化。

### 为什么不能用 `failed -> refund`

`failed` 在很多工作流引擎里只表示“一次 attempt 失败”，不表示“最终失败”。

在当前 AI Engine 里也是这样：

1. `Engine.executeTask` 返回 `RunFailed`
2. 会先写 `task_failed`
3. `Worker` 再决定是否自动重试
4. 若继续重试，任务还会重新回到 `pending`

如果这里直接退款，就会出现：

1. 第一次失败先退款
2. 后续自动重试成功又产出结果
3. 账务出现“已退款后又成功”的不一致

因此退款只能绑定：

1. `task_final_failed`
2. 或与其等价的“明确不再重试”的稳定终态

### 为什么 `task_succeeded -> consume` 可以成立

成功侧和失败侧不对称，这一点是合理的。

在当前 AI Engine 里：

1. `task_failed` 只是一次运行失败，后面可能继续自动重试
2. 但 `task_succeeded` 已经是稳定成功，不会再回到 `pending/running`

因此成功侧不需要再额外补一层 `final` 语义，可以直接使用：

1. `task_succeeded -> consume`

这样模型会更简洁：

1. freeze 绑定“任务被接受”
2. refund 绑定“最终失败”
3. consume 绑定“稳定成功”

## 5. 建议方案

## 5.1 新增稳定终态事件

建议保留当前已有事件语义不变：

1. `task_succeeded`
2. `task_failed`
3. `task_suspended`

同时新增一个用于结算的稳定失败事件：

1. `task_final_failed`

含义约束：

1. `task_final_failed`
   - 自动重试窗口已经结束
   - 任务已经稳定失败
   - 可以退款

为什么不直接复用现有 `task_failed`：

1. 现有 `task_failed` 已经被很多逻辑当成“本次执行失败事件”
2. 当前自动重试机制下，它不是 billing 级终态
3. 直接修改它的语义，改动面会很大

因此最小风险方案是：

1. 保留 `task_succeeded` 作为稳定成功事件
2. 保留现有 `task_failed` 做运行态观测
3. 额外增加 billing 真正依赖的稳定失败事件 `task_final_failed`

### 5.2 终态事件的生产位置

建议由“最终状态决策者”负责发布稳定终态事件，而不是由 `Engine.executeTask` 直接发布全部 billing terminal event。

具体建议：

1. `task_succeeded`
   - 继续由 `Engine.executeTask` 在 `RunSuccess` 后发布
   - 因为成功后当前任务不会再自动重试，它本身就是稳定成功事件
2. `task_final_failed`
   - 不由 `Engine.executeTask` 在每次 `RunFailed` 后直接发布
   - 改为由上层 retry policy 决策者在“确认不再自动重试”时发布

当前代码里，这个“最终失败决策者”就是 `Worker.handle`：

1. 小于最大自动重试次数时，重新 `PrepareTaskRetry + Push`
2. 达到最大自动重试次数时，才进入稳定失败

因此第一版建议：

1. success 稳定终态继续由 `Engine` 发 `task_succeeded`
2. final failed 稳定终态由 `Worker` 在放弃自动重试时发 `task_final_failed`

这看起来仍然有一小部分逻辑在 `Worker`，但 billing 已经不再绑定 `Worker` 执行入口，而是绑定稳定终态事件。

后续如果将 retry policy 进一步上提，也可以再把 `task_final_failed` 的生产位置继续收拢。

### 5.2.1 `task_final_failed` 应该在哪些代码点发

第一版建议只在“已经明确不会再自动重试”的地方发 `task_final_failed`。

结合当前代码，建议落在下面几类收口点：

#### A. `Worker.handle` 达到自动重试上限

文件：

- `ai-engine/worker/worker.go`

当前逻辑里：

1. `task.RetryCount++`
2. 若 `task.RetryCount >= maxAutoRetryCount`
3. 进入稳定失败收口

这里是最标准的 `task_final_failed` 生产点，因为：

1. retry policy 已经明确决定不再重试
2. 任务会进入 dead queue
3. 后续只剩人工 `resume` 或补偿流程

建议在这里发布：

1. `task_failed` 仍由 `Engine.executeTask` 保持原样
2. `task_final_failed` 由 `Worker` 在真正放弃自动重试后补发

#### B. `PrepareTaskRetry` 失败，导致无法继续自动重试

文件：

- `ai-engine/worker/worker.go`

当前链路里如果：

1. 任务本次执行失败
2. 原本还没达到自动重试上限
3. 但 `PrepareTaskRetry(...)` 自身失败

那就说明：

1. 自动重试策略虽然想继续
2. 但 runtime 清理/恢复准备失败
3. 当前任务实际上已经无法继续自动重试

这类也应视为稳定失败，应发布 `task_final_failed`。

#### C. `Worker` 前置准备失败，且当前策略不再继续重试

文件：

- `ai-engine/worker/worker.go`

例如：

1. workflow version 加载失败
2. workflow definition 反序列化失败

如果当前收口策略是：

1. 直接置 `failed`
2. 进 dead queue
3. 不再自动重试

那么这里也应发布 `task_final_failed`。

注意：

1. 如果未来对这类错误引入单独重试策略
2. 那就不应在第一次失败时直接发 `task_final_failed`

所以判断标准不是“这里报错了”，而是“这里是否真正结束了自动重试窗口”。

#### D. 人工取消 / 人工终止后走失败性终态

如果后续产品语义里存在：

1. 人工终止任务
2. 任务进入不可恢复终态
3. 需要自动退回冻结点数

那么这类也应统一落到 `task_final_failed` 或等价事件上。

是否单独拆成 `task_final_canceled`，取决于你们是否需要在业务上区分：

1. 系统执行失败
2. 用户主动取消

若账务语义都是“释放冻结”，第一版也可以都归到 `task_final_failed`，由 payload 里的 `final_reason` 区分。

### 5.2.2 哪些代码点不应该发 `task_final_failed`

以下位置不建议直接发：

#### A. `Engine.executeTask` 的 `RunFailed`

文件：

- `ai-engine/engine/task_execution.go`

原因：

1. 这里看到的是“一次执行失败”
2. 看不到上层是否还会自动重试
3. 直接在这里发 final failed，很容易提前退款

#### B. `AwaitPollWorker` / webhook / signal handler 的外层入口

文件：

- `ai-engine/worker/await_poll_worker.go`
- 各类 callback handler

原因：

1. 它们只是恢复入口，不是最终失败决策层
2. await 完成后任务还可能继续在引擎里走分支、继续运行、继续失败后再重试
3. 这些入口不应自行决定账务终态

#### C. 任意普通 `task_failed` 监听器

原因：

1. 现有 `task_failed` 不是稳定终态
2. 任何收到 `task_failed` 就直接退款的逻辑，都会和自动重试冲突

### 5.2.3 `task_final_failed` 建议 payload 字段

建议 payload 至少包含两层信息：

1. 事件公共字段
2. 失败终态上下文字段

#### 事件公共字段

建议沿用当前 `TaskEvent` 结构，至少包含：

| 字段 | 含义 |
| --- | --- |
| `task_id` | 当前任务 ID |
| `root_task_id` | 根任务 ID，便于串联子任务/分叉任务 |
| `step` | 固定为 `task` |
| `type` | 固定为 `task_final_failed` |
| `message` | 例如“任务最终失败” |
| `created_at` | 事件时间 |

#### `meta` 建议字段

建议在 `meta` 中至少带下面这些字段：

| 字段 | 是否必需 | 含义 |
| --- | --- | --- |
| `final_reason` | 是 | 最终失败原因类别 |
| `error_message` | 是 | 用户/日志可读的失败原因 |
| `retry_count` | 是 | 当前任务失败计数 |
| `retry_limit` | 是 | 当前自动重试上限 |
| `retry_exhausted` | 是 | 是否已达到自动重试上限 |
| `source` | 是 | 哪个收口点发出的 final failed |
| `last_run_status` | 否 | 最后一次执行结果，通常为 `failed` |
| `can_manual_resume` | 否 | 是否允许人工恢复 |
| `billing_action` | 否 | 建议固定为 `refund`，便于观测 |

#### `final_reason` 建议枚举

第一版建议统一成少量稳定枚举：

| 值 | 含义 |
| --- | --- |
| `retry_exhausted` | 达到自动重试上限 |
| `retry_prepare_failed` | 重试准备失败 |
| `worker_prepare_failed` | worker 前置准备失败且不再重试 |
| `canceled` | 人工或系统取消后进入释放冻结流程 |
| `non_retryable` | 明确不可恢复错误 |

这样 listener 可以：

1. 不依赖中文 message 判定
2. 更容易做指标聚合
3. 更容易做补偿和排查

### 5.2.4 `task_final_failed` 事件示例

```json
{
  "task_id": 2047005524536344576,
  "root_task_id": 2047005524536344576,
  "step": "task",
  "type": "task_final_failed",
  "message": "任务最终失败",
  "meta": {
    "final_reason": "retry_exhausted",
    "error_message": "prompt_enhance node status is failed, user_prompt is required",
    "retry_count": 3,
    "retry_limit": 3,
    "retry_exhausted": true,
    "source": "worker.retry_limit",
    "last_run_status": "failed",
    "can_manual_resume": true,
    "billing_action": "refund"
  },
  "created_at": "2026-04-25T20:41:30+08:00"
}
```

### 5.2.5 `task_final_failed` 的消费约束

`TaskBillingSettlementListener` 消费该事件时，不应再自行推导重试策略。

listener 只需要信任：

1. 事件类型已经是 `task_final_failed`
2. 当前 billing record 是否仍为 `frozen`

也就是：

1. 事件负责表达“运行时已经给出最终失败判定”
2. listener 负责做账务幂等流转

### 5.2.6 成功侧事件的约束

成功侧不再额外新增 `task_final_succeeded`。

因为在当前 AI Engine 中：

1. `task_succeeded` 已经是稳定成功事件
2. 成功后任务不会再自动重试，也不会再回到 `pending/running`
3. 因此没必要再补一层 success 的 final 语义

#### `task_succeeded` 应该在哪些代码点发

建议继续保持在：

- `ai-engine/engine/task_execution.go`

也就是 `Engine.executeTask` 返回 `RunSuccess` 的位置。

这能天然覆盖：

1. `Worker` 直接跑完的同步任务
2. await 恢复后跑完的任务
3. async 事件恢复后跑完的任务
4. webhook / signal 恢复后跑完的任务

#### 哪些位置不应该额外发成功结算事件

不建议在下面这些地方再补新的 success 结算事件：

1. `Worker.handle` 的 `RunSuccess` 分支
2. `AwaitPollWorker` / webhook / signal handler 外层
3. 任意普通 `task_succeeded` 监听器之外的业务代码

#### `task_succeeded` 的消费约束

`TaskBillingSettlementListener` 消费 `task_succeeded` 时，不需要再回头推导任务是不是成功完成。

listener 只需要信任：

1. 事件类型已经是 `task_succeeded`
2. 当前 billing record 是否仍为 `frozen`

## 5.2.7 成功侧与失败侧的统一规则

建议统一成下面的模型：

| 事件 | 谁负责发 | 什么时候发 | listener 动作 |
| --- | --- | --- | --- |
| `task_succeeded` | `Engine.executeTask` | 任务进入稳定成功 | `consume` |
| `task_final_failed` | retry policy 决策层，第一版为 `Worker` | 明确不再自动重试、进入稳定失败 | `refund` |

这样职责边界会非常清楚：

1. `Engine` 负责描述稳定成功
2. retry policy 层负责描述何时算真正失败
3. settlement listener 只负责做账务状态流转

### 5.3 新增 `TaskBillingSettlementListener`

建议新增专门 listener，监听：

1. `task_succeeded`
2. `task_final_failed`

建议文件位置：

- `ai-engine/service/task_billing_settlement_listener.go`

职责：

1. 订阅终态事件
2. 调用 `BillingTaskService`
3. 做结算幂等
4. 打日志和埋点

不负责：

1. 重试策略
2. task 状态流转
3. await / async / webhook 编排

### 5.4 listener 处理规则

#### `task_succeeded`

处理逻辑：

1. 根据 `task_id` 查 `TaskBillingRecord`
2. 若无 billing record
   - 记录 info 日志
   - 直接跳过
3. 若 `status == frozen`
   - 调 `ConsumeTask`
4. 若 `status == consumed`
   - 视为幂等成功
5. 若 `status == refunded/canceled`
   - 记录 error 日志并告警

#### `task_final_failed`

处理逻辑：

1. 根据 `task_id` 查 `TaskBillingRecord`
2. 若无 billing record
   - 记录 info 日志
   - 直接跳过
3. 若 `status == frozen`
   - 调 `RefundTask`
4. 若 `status == refunded/canceled`
   - 视为幂等成功
5. 若 `status == consumed`
   - 记录 error 日志并告警

## 6. 为什么不建议继续把 billing 放在 `Worker`

### 6.1 await / async 已经天然绕开 `Worker`

`AwaitPollWorker` 和 `AsyncWorker` 不是 bug，它们代表的是新的恢复执行入口。

真正的共性不在 `Worker`，而在：

1. 任务最终都由 `Engine` 或 retry policy 决策层收敛成稳定终态
2. 稳定终态都可以统一发布事件

### 6.2 绑定事件更容易做幂等

如果每个入口都各自结算：

1. `Worker` 一套
2. `AwaitPollWorker` 一套
3. webhook 一套
4. signal 一套

幂等就会散落。

如果只监听稳定终态：

1. 所有入口最终只会落到同一组事件
2. 幂等只需要守住 `TaskBillingRecord.status`

### 6.3 更容易观测和补偿

事件驱动方案下，可以更容易排查：

1. 哪个 task 发布了终态事件
2. listener 是否消费成功
3. billing record 是否流转成功

出问题时也更容易做 replay / 补偿。

## 7. 迁移方案

### 7.1 Phase 1：增加稳定终态事件，不改 billing 入口

先补：

1. `task_final_failed`

当前阶段 listener 只打日志，不真正结算。

目的：

1. 验证所有路径都能产出稳定终态事件
2. 对照现有 `Worker` 结算结果

### 7.2 Phase 2：listener shadow mode

新增 `TaskBillingSettlementListener`，但先只：

1. 记录“如果由 listener 结算，会做什么”
2. 不真正调用 `ConsumeTask/RefundTask`

观测重点：

1. 普通同步任务
2. await 恢复成功
3. await 恢复失败
4. async 节点恢复成功/失败
5. webhook / signal 恢复路径

### 7.3 Phase 3：切换到 listener 真正结算

切换后：

1. `Worker` 不再直接 `ConsumeTask`
2. `Worker` 不再直接 `RefundTask`
3. `TaskBillingSettlementListener` 成为唯一自动结算入口

此时 `Worker` 只负责：

1. 执行
2. 自动重试
3. 死信收口
4. 产出稳定终态事件

### 7.4 Phase 4：清理历史逻辑

删除或收敛：

1. `Worker` 中直接 billing 调用
2. 入口层重复的结算逻辑
3. 与“必须回到 Worker 才能结算”相关的假设

## 8. 代码改动建议

### 8.1 `ai-engine/engine/task_execution.go`

新增：

1. 保留 `task_succeeded` 作为稳定成功事件

保留：

1. `task_succeeded`
2. `task_failed`
3. `task_suspended`

注意：

1. 不要在这里直接发 `task_final_failed`
2. 因为这里看不到自动重试是否还会继续

### 8.2 `ai-engine/worker/worker.go`

调整：

1. 小于最大自动重试次数时
   - 继续 `PrepareTaskRetry + Push + Ack`
   - 不发布 `task_final_failed`
2. 达到最大自动重试次数时
   - 进入稳定失败
   - 发布 `task_final_failed`

同时逐步删除：

1. `ConsumeTask`
2. `RefundTask`

### 8.3 `ai-engine/service/task_billing_settlement_listener.go`

新增 listener：

1. 订阅 `task_succeeded`
2. 订阅 `task_final_failed`
3. 调用 `BillingTaskService`
4. 记录 settle success / skip / error

### 8.4 `internal/service/billing_task_service.go`

建议新增更清晰的对外方法：

1. `SettleTaskSuccess`
2. `SettleTaskFailure`

它们内部继续复用：

1. `ConsumeTask`
2. `RefundTask`

这样 listener 不需要理解底层 billing record 状态细节。

## 9. 测试建议

必须覆盖：

1. 普通同步任务成功
   - 发布 `task_succeeded`
   - listener consume
2. 普通同步任务失败后自动重试成功
   - 中间有 `task_failed`
   - 没有 `task_final_failed`
   - 最终只有 `task_succeeded`
3. 普通同步任务达到重试上限失败
   - 发布 `task_final_failed`
   - listener refund
4. await 任务先 `task_suspended`
   - 不 consume
   - 恢复成功后发布 `task_succeeded`
5. await 任务恢复失败
   - 若仍可继续自动重试，不发布 `task_final_failed`
   - 最终失败时才发布 `task_final_failed`
6. listener 重复消费同一事件
   - 结算保持幂等
7. 没有 billing record 的任务
   - listener 跳过，不报致命错误

## 10. 风险与注意事项

### 10.1 不要把“运行失败事件”和“最终失败事件”混用

这是本设计最容易踩坑的地方。

如果直接把当前 `task_failed` 当作结算事件，会导致提前退款。

### 10.2 listener 必须有足够观测

建议至少记录：

1. `task_id`
2. `event_type`
3. `billing_record_status_before`
4. `action`
5. `result`

### 10.3 先 shadow，再切主

这条链路涉及真实扣点，不建议一步切换。

## 11. 结论

结算统一挂在 task terminal event 上，是比“挂在 `Worker` 上”更稳定的边界。

但这里的前提不是直接复用当前所有 `task_failed`，而是先补一层“稳定终态事件”语义：

1. `task_succeeded`
2. `task_final_failed`

这样才能同时满足：

1. await / async / webhook / signal 多入口一致
2. 不把 billing 下沉到 Engine 核心 DAG 逻辑
3. 自动重试期间不提前退款
4. 最终结算幂等且易于补偿
