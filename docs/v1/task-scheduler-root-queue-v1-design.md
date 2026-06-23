# TaskScheduler Root Queue V1 设计草案

日期：2026-05-22

状态：Draft

关联代码：

- `ai-engine/domain/task.go`
- `ai-engine/worker/worker.go`
- `ai-engine/worker/recovery_scanner.go`
- `ai-engine/engine/subworkflow.go`
- `ai-engine/workflows/goods/goods_video_pro_dsl.go`
- `ai-engine/workflows/goods/goods_shot_i2v_generate_dsl.go`

## 1. 背景

当前 `ai-engine` 的任务调度路径主要是：

```
CreateTask
  -> task.status = pending
  -> TaskRepository.Enqueue
  -> RedisQueue
  -> Worker.PopAndReserve
  -> Worker.TryClaimTask
  -> Engine.RunWithResult
```

这条路径可以保证任务被 worker 消费和执行，但缺少两个关键能力：

1. **每个用户同时执行的 root 任务数量限制**
2. **系统同时执行的 root 任务总数限制**

对于图片、视频生成场景，如果不做 root 任务准入控制，一个用户连续创建多个任务，就可能占满 worker、数据库、第三方生成容量，并影响其他用户。

同时，`goods_video_pro_v3` 这类复杂 DSL 内部已经存在 `NodeMap.parallel` 和 `NodeLoop` 的局部并发控制。例如：

- `analyze_product_images_multi` 使用 `parallel: 3`
- `clean_product_images_multi` 使用 `parallel: 2`
- `tts_segments_multi` 使用 `parallel: 3`
- `loop_generate_shots_v3` 是 loop 节点，天然一次推进一个 shot

因此 V1 不需要一开始构建复杂的 root/child 多层公平调度。更稳妥的边界是：

```
TaskScheduler 只负责 root task 准入
Map / Loop 继续使用 DSL 内部并发配置
Provider/model limiter 放在真实 provider submit tool 前
```

## 2. 目标

V1 目标：

1. 新增 `queued` 状态，表达“任务已创建，但还没有获得 root 调度资格”
2. `CreateTask` 默认创建 `queued` root task
3. 新增 `TaskScheduler`，将符合准入条件的 root task 从 `queued` 推进到 `pending`
4. `pending` 在 V1 中继续表示“已调度，等待 worker 消费”，暂不新增 `scheduled`
5. 支持配置：
   - `max_global_running_roots`
   - `max_running_roots_per_user`
6. 明确区分 `queued` 和 `suspended`
7. 明确失败任务不会被自动重新排队，只有用户手动恢复/重试才重新进入调度

## 3. 非目标

V1 不做：

1. 不做复杂的 root/child 加权公平队列
2. 不把所有子任务都纳入 user root slot 计算
3. 不在 TaskScheduler 阶段预测 provider/model
4. 不在 TaskScheduler 阶段控制第三方 API 真实并发
5. 不重写 NodeMap / Loop 的内部并发机制
6. 不把 `pending` 立即改名为 `scheduled`
7. 不让失败任务自动重新 queued

## 4. 状态定义

建议在现有状态基础上新增 `TaskQueued`：

```go
type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"    // 等待调度准入
	TaskPending   TaskStatus = "pending"   // 已准入，等待 worker 消费；V1 等价于 scheduled
	TaskRunning   TaskStatus = "running"
	TaskSuccess   TaskStatus = "success"
	TaskFailed    TaskStatus = "failed"
	TaskSuspended TaskStatus = "suspended"
	TaskCanceled  TaskStatus = "canceled"
)
```

状态语义：

| 状态 | 含义 | 是否已占用 root slot | 是否展示排队位置 |
|---|---|---:|---:|
| `queued` | 已创建，等待 root 调度准入 | 否 | 是 |
| `pending` | 已准入，等待 worker 消费 | 是 | 否 |
| `running` | worker 正在执行 workflow | 是 | 否 |
| `suspended` | workflow 主动挂起，等待子任务、await、webhook、poll 等 | 是 | 否 |
| `success` | 终态成功 | 否 | 否 |
| `failed` | 终态失败或当前 attempt 失败，取决于现有失败语义 | 否 | 否 |
| `canceled` | 已取消 | 否 | 否 |

`queued` 和 `suspended` 必须严格区分：

- `queued`：任务还没有开始跑，没有拿到 root slot
- `suspended`：任务已经开始跑，主 workflow 暂停等待，但子任务或第三方任务可能正在推进

前端不应把 `suspended` 展示为“排队中”。

## 5. 状态流转

建议 V1 状态流转：

```go
var AllowedTransitionsTasks = map[TaskStatus][]TaskStatus{
	TaskQueued: {
		TaskPending,
		TaskCanceled,
	},
	TaskPending: {
		TaskRunning,
		TaskQueued, // scheduled/pending 超时未被 worker 消费，退回排队
		TaskCanceled,
	},
	TaskRunning: {
		TaskSuspended,
		TaskSuccess,
		TaskFailed,
		TaskCanceled,
	},
	TaskSuspended: {
		TaskRunning,
		TaskFailed,
		TaskCanceled,
	},
	TaskFailed:   {},
	TaskSuccess:  {},
	TaskCanceled: {},
}
```

说明：

1. `pending -> queued` 只用于系统异常恢复，例如任务已调度但长时间没有被 worker 消费。
2. `failed -> queued` 不作为自动流转。用户手动重试/恢复时应走明确的业务入口。
3. `suspended -> running` 是 await、子任务完成、webhook、poll 等恢复路径。

## 6. Root Task 与 Child Task

当前 `Task` 已有：

```go
ParentID *int64
RootID   int64
```

V1 采用如下规则：

```text
ParentID == nil:
  root task，代表用户创建的一次顶层任务
  需要经过 TaskScheduler root 准入

ParentID != nil:
  child task，代表 subworkflow/map/loop 内部推进
  不额外占用用户 root slot
```

这样可以避免以下问题：

```
用户 A 创建 Root-1
Root-1 执行中创建 child tasks
用户 A 又创建 Root-2

期望：
  Root-1 的 child tasks 继续推进
  Root-2 继续 queued
```

如果 child task 和 root task 完全同级竞争用户 root slot，可能导致 Root-1 自己的子任务被 Root-2 挡住，使 Root-1 长时间 suspended。

V1 暂不引入 `max_children_per_root`。内部并发继续依赖：

- `NodeMap.Config["parallel"]`
- `NodeLoop` 顺序执行语义
- worker 层整体执行并发

## 7. CreateTask 行为

建议 `CreateTask` 统一创建 `queued` root task。

创建后不直接 `Enqueue` 到 worker ready queue，而是等待 `TaskScheduler` 准入：

```
POST /tools/tasks
  -> create task(status=queued)
  -> return queued response
  -> TaskScheduler async scan
  -> queued -> pending
  -> push RedisQueue
```

响应建议包含排队信息：

```json
{
  "task_id": "123",
  "status": "queued",
  "queue_position": 3,
  "running_roots": 1,
  "max_running_roots": 1,
  "estimated_wait_seconds": 180
}
```

如果调度很快发生，前端可以通过任务详情或 websocket 看到：

```
queued -> pending -> running
```

### 7.1 排队位置

V1 可以先用简单估算：

```
queue_position =
  count(root queued tasks for same user with priority higher or same and queued_at earlier)
```

如果产品希望展示全局排队位置，可以额外返回：

```json
{
  "user_queue_position": 3,
  "global_queue_position": 12
}
```

但推荐 V1 先展示用户维度的位置，更容易解释。

### 7.2 等待时间估算

`estimated_wait_seconds` V1 可以是粗估：

```
estimated_wait_seconds = queue_position * avg_root_task_duration_seconds / max_running_roots_per_user
```

如果没有稳定数据，可以返回 `null` 或不返回，避免误导。

## 8. TaskScheduler 设计

### 8.1 职责

`TaskScheduler` 只做准入，不执行 workflow。

职责：

1. 周期性扫描 `queued` root task
2. 按 `priority, queued_at` 选择候选任务
3. 判断是否满足：
   - 全局 running root 未满
   - 当前用户 running root 未满
4. 满足后将任务状态从 `queued` 改为 `pending`
5. push 到现有 `RedisQueue`

不负责：

1. 不执行 `Engine.RunWithResult`
2. 不判断 provider/model slot
3. 不展开 DSL
4. 不调度 map item 级别任务

### 8.2 伪代码

```go
func (s *TaskScheduler) RunOnce(ctx context.Context) error {
	tasks := s.taskRepo.FindQueuedRootTasks(ctx, s.batchSize)

	for _, task := range tasks {
		if !s.canAdmitGlobalRoot(ctx) {
			return nil
		}
		if !s.canAdmitUserRoot(ctx, task.UserID) {
			continue
		}

		ok, err := s.taskRepo.TryScheduleQueuedTask(ctx, task.ID)
		if err != nil || !ok {
			continue
		}

		if err := s.queue.Push(ctx, task.ID); err != nil {
			// push 失败需要把 pending 退回 queued，或者由 recovery 扫描兜底
			_ = s.taskRepo.MarkPendingScheduleFailed(ctx, task.ID)
			continue
		}
	}

	return nil
}
```

`TryScheduleQueuedTask` 必须是 CAS：

```sql
UPDATE workflow_tasks
SET status = 'pending', scheduled_at = now()
WHERE id = ?
  AND status = 'queued'
  AND parent_id IS NULL
```

### 8.3 运行中 root 计数

V1 可以先通过 DB 查询计算：

```sql
SELECT count(*)
FROM workflow_tasks
WHERE parent_id IS NULL
  AND status IN ('pending', 'running', 'suspended')
```

用户维度：

```sql
SELECT count(*)
FROM workflow_tasks
WHERE parent_id IS NULL
  AND user_id = ?
  AND status IN ('pending', 'running', 'suspended')
```

说明：

1. `pending` 已经占用 root slot，因为已经被准入。
2. `suspended` 也占用 root slot，因为任务已经开始执行，只是在等待子任务或外部事件。
3. `failed/success/canceled` 不占用 root slot。

DB count 实现简单，适合 V1。后续如性能不足，可以替换为 Redis lease counter / semaphore。

## 9. Worker 行为

Worker V1 仍消费现有 RedisQueue，但只应消费已调度的任务：

```
RedisQueue = workflow_ready_queue
```

Worker 行为：

1. `PopAndReserve`
2. `TryClaimTask`
3. 加载 task
4. 如果 task 不是 `pending`，跳过或 ack
5. `pending -> running`
6. 执行 engine
7. 根据结果进入 `success / suspended / failed`

Worker 不做：

1. 不扫描 `queued`
2. 不判断用户 root 并发
3. 不判断全局 root 并发

## 10. Provider/Model 并发限制

V1 不在 TaskScheduler 阶段做 provider/model 限制。

原因：

1. `goods_video_pro_v3` 中真实 provider/model 由 `provider_router` 执行后确定
2. cache hit 可能直接返回，不一定会调用 provider
3. 不同 workflow 的 provider 字段可能来自不同节点
4. 子工作流 `goods_shot_i2v_generate` 中真正提交发生在 `shot_submit` / `shot_submit_kling`

建议 provider/model limiter 放在真实外部 submit tool 前，例如：

```text
goods_shot_i2v_submit
goods_shot_kling_i2v_submit
TTS submit tool
image generation submit tool
```

这一层后续可以使用 Redis lease semaphore：

```text
provider:volcengine
provider:kling
model:seedance_1_0
model:kling_omi
```

对于第三方“同时生成任务数”限制，slot 应绑定到：

```text
task_id
node_runtime_id
provider_job_id
provider
model
lease_until
```

submit 成功后进入 await 时，本地 worker 可以释放，但 provider/model slot 不一定释放。若第三方限制的是“同时生成中任务数”，slot 应保持到 webhook/poll 完成。

## 11. Recovery 策略

V1 recovery 需要区分系统异常和业务失败。

### 11.1 pending 超时未被 worker 消费

场景：

```
TaskScheduler: queued -> pending
TaskScheduler: push RedisQueue 成功或失败不确定
Worker 没有消费
```

处理：

```
pending 超过 N 分钟且没有 worker_id / started_at
  -> queued
```

这类恢复可以释放 root slot，让 scheduler 重新准入。

### 11.2 running worker 崩溃

沿用现有 `RecoveryScanner` 思路，通过 node heartbeat 判断 crash。

注意：

1. 如果任务已经提交第三方 job，不能盲目重提。
2. 应优先通过 await binding / provider job id 做恢复确认。
3. 对 `TaskFailed` 不做自动恢复，避免计费和重复调用第三方风险。

### 11.3 suspended

`suspended` 通常是正常等待状态，不应被当成排队或卡死。

只有在满足明确超时策略时才处理，例如：

1. await binding 超时
2. fallback poll 超过最大次数
3. provider job 明确失败

### 11.4 failed 不自动重新排队

业务失败后：

```
TaskFailed
```

不应自动转回 `queued`。

只有用户手动重试/恢复时，才通过明确 API 重新创建任务或恢复任务，并重新走计费/冻结点数流程。

## 12. 前端展示建议

展示状态建议：

| TaskStatus | 展示 |
|---|---|
| `queued` | 排队中 |
| `pending` | 准备中 |
| `running` | 生成中 |
| `suspended` | 处理中 / 等待 AI 生成结果 / 正在处理分镜 |
| `success` | 已完成 |
| `failed` | 失败 |
| `canceled` | 已取消 |

`suspended` 的细分文案可以来自后续新增字段：

```text
suspend_reason = child_tasks | third_party | webhook | manual | retry
```

V1 如果暂时没有字段，可以根据当前 running/awaiting node 或事件 meta 推断。

## 13. 配置建议

V1 配置：

```yaml
scheduler:
  enabled: true
  scan_interval: 1s
  batch_size: 50
  max_global_running_roots: 20
  max_running_roots_per_user: 1
  pending_timeout: 2m
```

后续 provider limiter 配置：

```yaml
providers:
  volcengine:
    max_concurrent_jobs: 5
  kling:
    max_concurrent_jobs: 3
models:
  seedance_1_0:
    max_concurrent_jobs: 5
  seedance_2_0:
    max_concurrent_jobs: 3
  kling_omi:
    max_concurrent_jobs: 3
```

## 14. 实施拆分

### Cut 1：状态和创建任务

1. 新增 `TaskQueued`
2. 更新状态流转表
3. `CreateTask` 创建 root task 时使用 `queued`
4. CreateTask response 增加：
   - `queue_position`
   - `running_roots`
   - `max_running_roots`
   - `estimated_wait_seconds`（可选）

### Cut 2：TaskScheduler

1. 新增 `TaskScheduler`
2. 新增 repository 查询：
   - `FindQueuedRootTasks`
   - `CountActiveRootTasks`
   - `CountActiveRootTasksByUser`
   - `TryScheduleQueuedTask`
3. Scheduler 将 `queued -> pending`
4. Scheduler push RedisQueue
5. server 启动 scheduler goroutine

### Cut 3：Worker 校验

1. Worker 只执行 `pending` task
2. Worker claim 后将 `pending -> running`
3. 非预期状态的 task 需要 ack 或退回，避免死循环

### Cut 4：Recovery

1. pending 超时未消费 -> queued
2. 保持 `TaskFailed` 不自动恢复
3. 明确 `suspended` 只通过 await/poll/recovery 语义恢复

### Cut 5：Provider Limiter（后续）

1. 在真实 provider submit tool 前加入 limiter
2. submit 成功后 slot 绑定 `node_runtime_id + provider_job_id`
3. webhook/poll 完成后释放 slot
4. recovery 扫描泄漏 slot

## 15. 最终边界

V1 的最终边界：

```text
CreateTask:
  创建 queued root task

TaskScheduler:
  控制 root task 准入
  max_running_roots_per_user
  max_global_running_roots

Worker:
  只执行 pending/ready queue

Map / Loop:
  继续使用 DSL 内部 parallel / 顺序语义

Provider Limiter:
  后续放在真实 submit tool 前

Recovery:
  修复系统异常
  不自动恢复 failed 业务失败
```

一句话：

```
V1 先把用户 root 任务排队和系统 root 总并发做实；
子任务继续跟随现有 workflow runtime；
provider/model 真实并发在 submit tool 层治理。
```
