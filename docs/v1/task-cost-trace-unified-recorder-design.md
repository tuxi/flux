# AI Engine Task Cost Trace 统一收口层设计

日期：2026-04-25

状态：Draft

关联文档：

- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)
- [AI Engine Task Cost Trace 实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-task-breakdown.md)
- [Engine Await Runtime V1 PRD](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/engine-await-runtime-v1-prd.md)

## 1. 背景

当前我们已经明确：

- `task_cost_trace` 应作为统一成本明细主表
- `task` 负责成本汇总
- `node runtime` 保持运行态与排障职责

但要真正落地 `task_cost_trace`，还需要回答一个关键问题：

**成本明细到底应该在什么地方统一计算并写入？**

尤其当前引擎同时存在以下执行路径：

1. 普通同步 `tool` 节点
2. `tool.AsyncExecution` 异步节点
3. `await` 节点
4. webhook / poll_once / replay 驱动的恢复执行
5. subworkflow / map / loop 等 fanout/fanin 路径

如果收口点选错，会出现以下问题：

- 计费逻辑分散在工具内部
- async / await 与 sync 口径不一致
- 容易重复记账
- 子任务和父任务之间发生双记账
- 成本逻辑污染 DSL 与业务工具

## 2. 设计目标

本设计目标：

1. 为同步、异步、await 恢复三类节点找到统一成本收口点
2. 保证成本明细写入具备幂等性
3. 不把正式记账逻辑塞进 DSL tool 内部
4. 不把记账逻辑散落在 worker / webhook handler / replay handler
5. 为 `tts / llm / vlm / image_generation / video_generation` 提供统一扩展路径

## 3. 结论

明确结论如下：

### 主挂点

`task_cost_trace` 的统一收口层，最适合挂在：

- `Engine.runDAG(...)`
- 节点 `finalizeNode(...)` 成功之后

也就是：

- 普通节点执行成功并 finalize 后
- 已处于 `success_pending_edges` 的异步/await 节点在恢复后 finalize 后

统一调用一个新的：

- `costRecorder.RecordNodeSuccess(...)`

### 辅助挂点

任务级汇总适合在两个时机刷新：

1. 每次成功写入 `task_cost_trace` 后，刷新当前任务汇总
2. `executeTask(...)` 进入任务终态时，再做一次 reconcile

### 不建议挂的地方

- 不挂在工作流 DSL 工具内部
- 不挂在 `ToolStepAdapter.Run(...)`
- 不挂在 `AsyncWorker`
- 不挂在 `CompleteAwaitNode(...)`
- 不挂在 webhook / poll_once / replay handler

## 4. 为什么主挂点应放在 Engine finalize 后

当前引擎执行主链大致如下。

### 同步节点

```text
runDAG
  -> executeNode
  -> runNodeWithHeartbeat
  -> SetNodeOutput
  -> finalizeNode
```

### 异步 tool 节点

```text
runDAG
  -> executeNode
  -> scheduleAsyncActivity
  -> AsyncWorker 执行
  -> eventBus node_complete_async
  -> completeAsyncNode
  -> ResumeTask
  -> runDAG
  -> finalizeNode
```

### await 节点

```text
executeAwaitNode
  -> binding waiting
  -> webhook/poll/replay
  -> CompleteAwaitNode
  -> AttemptCompletePendingEdges
  -> ResumeTask
  -> runDAG
  -> finalizeNode
```

可以看到：

- sync 最终会进 `finalizeNode`
- async 最终也会进 `finalizeNode`
- await 恢复后最终也会进 `finalizeNode`

所以从统一性看，`finalizeNode` 之后是当前引擎里最天然的统一成功收口点。

## 5. 当前代码链路分析

### 5.1 节点执行入口

节点统一执行入口在：

- [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go:17)

这里会做：

- build input
- validate
- 写 `ResolvedInput`
- 执行 sync / async / await / subworkflow
- 写 `runtime.Output`

这说明：

- 这里已经具备成本提取所需的 `ResolvedInput`
- 也具备节点最终 `Output`

但这里还不是最佳记账点，因为：

- async / await 尚未真正完成
- 节点是否最终成功，还没有统一收口

### 5.2 DAG 调度与 finalize

统一 finalize 在：

- [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go:671)

而调用点在：

- [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go:574)
- [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go:690)

这意味着：

- 节点真正成功时，都会在这里转为 `NodeSuccess`
- `ActivatedEdges` 也已计算完成
- 失败路径也会在这里统一关闭边

因此这里最适合作为“正式入账前的最终成功确认点”。

### 5.3 Async worker 路径

异步 tool 完成后会发送：

- `node_complete_async`

位置在：

- [async_worker.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/worker/async_worker.go:55)

引擎监听位置在：

- [event_listen.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/event_listen.go:11)

它的逻辑是：

1. `completeAsyncNode(...)`
2. `ResumeTask(...)`
3. 再回到 `runDAG(...)`

因此不应在 worker 里直接记账，否则会和后面的统一收口层冲突。

### 5.4 Await 恢复路径

`await` 恢复统一入口在：

- [await_complete.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/await_complete.go:13)

这里会：

1. claim binding
2. `AttemptCompletePendingEdges(...)`
3. `ResumeTask(...)`

而 `AttemptCompletePendingEdges(...)` 只是把 runtime 置为：

- `success_pending_edges`
- 或 `failed_pending_edges`

位置在：

- [node.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/repository/query/node.go:346)

这还不是最终成功态，因此也不适合作为正式记账点。

## 6. 为什么不建议把成本写在工具内部

### 6.1 工具内部只适合产出 usage facts

工具最适合输出：

- `provider`
- `model`
- `usage_quantity`
- `usage_unit`
- `provider_request_id`
- `trace_payload`

例如：

- `tts` 输出字符数与预估成本基础字段
- `llm / vlm` 输出 token
- `image_generation` 输出图片数量
- `video_generation` 输出 jobs 或 seconds

这些是“资源使用事实”，不是正式账务动作。

### 6.2 工具内部不适合承担正式记账

原因：

1. 价格规则应该统一管理，不能散在工具里
2. 工具层很难正确处理重试幂等
3. async / await 会造成同一资源在不同入口完成
4. 子任务 / 父任务链路下容易双记账
5. DSL tool 会被成本逻辑污染

## 7. 统一收口层推荐架构

建议新增一层：

```text
ai-engine/cost/
  types.go
  extractor.go
  recorder.go
  summary.go
  extractors/
    tts_extractor.go
    llm_extractor.go
    vlm_extractor.go
    image_extractor.go
    video_extractor.go
```

### 7.1 `UsageExtractor`

职责：

- 从节点 `ResolvedInput + Output + node definition + runtime` 中提取成本事实

输出统一结构：

- `CostTraceDraft`

### 7.2 `TaskCostTraceRecorder`

职责：

- 将 `CostTraceDraft` 幂等写入 `task_cost_trace`

负责：

- 生成 `idempotency_key`
- 写库
- 记录错误

### 7.3 `TaskCostSummaryRefresher`

职责：

- 聚合当前任务下的成本明细
- 回写 `task.estimated_cost_total`
- 回写 `task.actual_cost_total`
- 回写 `task.cost_status`

## 8. 推荐的 Engine 接入方式

建议在 `Engine` 上新增：

- `costRecorder`

示意：

```go
type NodeCostRecorder interface {
    RecordNodeSuccess(runCtx *nodes.Context, node nodes.Node, runtime *domain.NodeRuntime) error
    ReconcileTaskCost(ctx context.Context, taskID int64) error
}
```

`Engine` 中新增字段：

```go
costRecorder NodeCostRecorder
```

推荐新增一个非阻断方法：

```go
func (e *Engine) tryRecordNodeCost(
    runCtx *nodes.Context,
    node nodes.Node,
    runtime *domain.NodeRuntime,
) {
    if e.costRecorder == nil || runCtx == nil || runtime == nil {
        return
    }
    if err := e.costRecorder.RecordNodeSuccess(runCtx, node, runtime); err != nil {
        // 只记日志/事件，不中断主工作流
    }
}
```

## 9. 具体挂点建议

### 9.1 主挂点

建议挂在 `runDAG(...)` 内部，在：

- `finalizeNode(...)` 成功之后

伪代码：

```go
if err := e.finalizeNode(runCtx, name, nil, nil, dag); err != nil {
    return RunResult{Status: RunFailed, Err: err}
}
e.tryRecordNodeCost(runCtx, node, runtime)
```

对于 `success_pending_edges` 分支也一样：

```go
if err := e.finalizeNode(...); err != nil {
    ...
}
e.tryRecordNodeCost(runCtx, node, runtime)
```

这样可同时覆盖：

- sync
- async resume
- await resume

### 9.2 任务汇总挂点

建议在两个位置刷新任务成本汇总：

1. `RecordNodeSuccess(...)` 成功后，立即刷新当前任务汇总
2. `executeTask(...)` 在任务进入终态时，再调用一次 `ReconcileTaskCost(...)`

位置在：

- [task_execution.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/task_execution.go:40)

这样可以兼顾：

- 运行中查看大致成本
- 任务结束时兜底校准

## 10. 幂等策略

统一收口层必须保证：

- 同一真实资源消耗只能写一次

推荐 `idempotency_key`：

```text
task_id + node_name + resource_type + provider_request_id
```

如果没有 `provider_request_id`，则退化为：

```text
task_id + node_name + resource_type + runtime_output_hash
```

说明：

- `provider_request_id` 适合 `tts / image / video`
- `runtime_output_hash` 可作为没有供应商 request id 时的退化方案

## 11. 子任务与 root task 关系

### 原则

- 谁真正调用了 provider，谁写成本明细
- 父节点不要因为拿到了子任务最终结果，再重复写同类成本

### 推荐字段

`task_cost_trace` 除 `task_id` 外，建议增加：

- `root_task_id`

这样：

- 子任务各自记账
- root task 成本通过 `root_task_id` 聚合

避免：

- subworkflow 双记账
- map / loop fanout 结果回收时重复记账

## 12. 需要补的基础模型

为了让统一收口层更顺滑，建议补两个字段。

### 12.1 `domain.NodeRuntime` 增加 `ID`

当前 domain 层的 `NodeRuntime` 还没有实体主键字段。

如果 `task_cost_trace` 要关联 `node_runtime_id`，建议：

- `domain.NodeRuntime` 增加 `ID`
- repository query 层把 `task_nodes.id` 映射回来

### 12.2 `task` 增加成本汇总字段

建议在 `task` 上新增：

- `estimated_cost_total`
- `actual_cost_total`
- `cost_status`
- `cost_version`

用于任务级快速展示与汇总查询。

## 13. 各资源类型如何接入

统一收口层不需要一次性写死所有资源逻辑，而是通过 extractor 扩展。

### `tts`

从节点输出提取：

- `chars_total`
- `estimated_cost`
- `provider`
- `voice / model`
- `fallback_chain`

### `llm`

从统一 LLM 调用输出提取：

- `prompt_tokens`
- `completion_tokens`
- `total_tokens`

### `vlm`

与 `llm` 分开：

- `resource_type = vlm`
- token 单独入账

### `image_generation`

提取：

- `image_count`
- `provider`
- `model`

### `video_generation`

提取：

- `jobs`
- 或 `seconds`

## 14. 本期推荐实现顺序

### Step 1

先引入 `NodeCostRecorder` 接口和空实现

### Step 2

在 `runDAG(...)` 的 `finalizeNode` 成功路径后插入 `tryRecordNodeCost(...)`

### Step 3

只实现 `tts` extractor

### Step 4

完成 `task_cost_trace` 写库与任务汇总回写

### Step 5

后续再逐步接 `llm / vlm / image / video`

## 15. 评审会需要拍板的事项

1. 是否确认 `Engine finalize 后` 为统一成本收口主挂点
2. 是否确认工具内部只产出 usage facts，不做正式记账
3. 是否确认 `CompleteAwaitNode / AsyncWorker / webhook handler` 不直接记账
4. 是否确认 `root_task_id` 进入 `task_cost_trace`
5. 是否确认 `domain.NodeRuntime` 需要补 `ID`

## 16. 结论

当前引擎架构下，最合理的成本统一收口方案是：

1. 工具层只产出资源使用事实
2. Engine 在 `runDAG -> finalizeNode` 成功后统一调用 `costRecorder`
3. `task` 只保留汇总
4. `task_cost_trace` 负责正式成本明细
5. async / await / sync 全部走同一条记账主路径

这样可以最大限度复用现有执行状态机，又能避免记账逻辑四处分散、重复入账和运行态模型污染。
