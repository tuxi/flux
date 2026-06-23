# Await Poll Usage Gap Analysis

## 背景

当前成本链已经切换到“显式 usage 协议优先”：

1. 工具实现可选 usage 协议
2. Engine 在 `runDAG -> finalizeNode` 成功后统一调用 `tryRecordNodeCost(...)`
3. 由 recorder 写入 `task_cost_traces`

对于普通 `tool` 节点，这条链路已经工作正常。

但在带货视频镜头生成链路中，`await + fallback poll_once` 组合暴露出一个缺口：

- 任务成功
- 节点输出完整
- 但 `task_cost_traces` 中缺少 `video_generation`

## 具体问题

以带货镜头生成为例：

- `shot_submit` 是 `tool` 节点
- `shot_wait` 是 `await` 节点
- `goods_shot_i2v_poll_once` 是 fallback poll tool

工作流定义中：

- `shot_wait` 的类型是 `definition.NodeAwait`
- `fallback_poll.tool = goods_shot_i2v_poll_once`

这意味着：

1. `shot_wait` 节点本身不是一个 `tool step`
2. `goods_shot_i2v_poll_once` 不是在 DAG 内作为当前节点的 `step` 执行
3. 它是在 `AwaitPollWorker.executePollTool(...)` 中由 worker 直接调用
4. worker 只把 `tool.Result.Data` 传给 `CompleteAwaitNode(...)`
5. `ResumeTask(...)` 恢复后，`shot_wait` 这个节点的 `step` 仍然是 `AwaitStep`

## 为什么会漏记账

当前 `tryRecordNodeCost(...)` 的 usage 获取方式是：

1. 读取当前 `node.Step`
2. 判断它是否实现 `UsageAwareStep`
3. 若实现，则调用 `BuildUsageFacts(...)`

对于普通 `tool` 节点，这没有问题，因为：

- `node.Step` 就是 `ToolStepAdapter`
- 底层真实 tool 可以透传 usage 协议

但 `shot_wait` 属于 `await` 节点：

- `node.Step` 是 `AwaitStep`
- `AwaitStep` 当前不实现 `UsageAwareStep`

所以即使：

- `goods_shot_i2v_poll_once` 自己实现了 usage 协议
- poll_once 实际也执行成功了

`tryRecordNodeCost(...)` 仍然拿不到 usage facts，因为当前恢复后的节点 step 不是这个 tool。

## 现象

数据库里会看到：

1. `task_nodes.output_json`
   - `shot_wait.output_json` 有完整的：
     - `video_url`
     - `api_task_id`
     - `api_provider`
     - `model`
     - `duration`
     - `total_tokens`
     - `completion_tokens`

2. `task_nodes.checkpoint_json`
   - `shot_wait.checkpoint_json` 为空
   - 没有 `usage_facts`
   - 没有 `cost_facts`

3. `task_cost_traces`
   - 没有对应的 `video_generation`

## 根因结论

不是 `AwaitPollWorker` 没完成任务，也不是 worker 没升级。

真正根因是：

**`AwaitPollWorker` 执行了一个实现 usage 协议的 poll tool，但没有把该 tool 产生的 usage facts 一起带回 `CompleteAwaitNode -> ResumeTask -> tryRecordNodeCost` 这条收口链。**

换句话说：

- 结果数据回填了
- 用量事实没有回填

## 与 map / loop 的区别

`map / loop` 不存在这个问题，因为：

1. 它们本身只是编排节点，不承担资源消耗
2. 真正产生成本的是它们展开出来的子工具节点
3. 这些子工具节点在 DAG 内真实执行
4. `tryRecordNodeCost(...)` 可以直接拿到对应 `tool step`

而 `await` 的特殊点在于：

- 外部完成结果对应的是 `AwaitStep`
- 真正执行 fallback poll tool 的是 worker 外部调用
- 这导致 usage 协议没有自然进入当前节点的 step 语义

## 修复方向

推荐方案：

### 方案 A

在 `AwaitPollWorker.executePollTool(...)` 成功执行 poll tool 后：

1. 判断该 poll tool 是否实现 `tool.UsageAware`
2. 若实现，则调用：
   - `UsageSchema()`
   - `BuildUsageFacts(input, result.Data)`
3. 校验 usage facts
4. 将 usage facts 随完成事件一起传给：
   - `CompleteAwaitNode(...)`
   - `ResumeTask(...)`
5. 最终让 `tryRecordNodeCost(...)` 能消费这批 usage facts

这个方案最符合当前架构，因为：

- 真正知道如何产出 usage 的，是 poll tool 自己
- 不需要让 `AwaitStep` 理解具体业务工具
- 保持“工具提供 usage facts，engine 统一入账”的分层

### 方案 B

让 `AwaitStep` 通过 binding 中的 `fallback_poll_tool` 反查 tool，再代理构造 usage facts。

不推荐，原因：

- `AwaitStep` 会知道过多业务细节
- 让编排节点反向理解业务 tool，不利于维护

## 当前影响范围

当前受影响的主要是：

- 使用 `NodeAwait`
- 且通过 `fallback_poll.tool`
- 且该 poll tool 代表真正完成态资源结果

典型案例：

- `goods_shot_i2v_generate_dsl` 中的 `shot_wait`

## 当前状态建议

在修复完成前：

1. 普通 `tool` 节点的成本链继续按现有显式 usage 协议运行
2. `await + poll_once` 场景需要被视为“已知漏记账缺口”
3. 针对这类任务分析成本时，需特别检查：
   - `output_json` 是否有最终资源结果
   - `checkpoint_json` 是否为空
   - `task_cost_traces` 是否缺失对应资源类型
