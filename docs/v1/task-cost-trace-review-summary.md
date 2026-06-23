# AI Engine Task Cost Trace 评审汇报版摘要

日期：2026-04-25

关联文档：

- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)
- [AI Engine Task Cost Trace 实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-task-breakdown.md)
- [AI Engine Task Cost Trace 统一收口层设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-unified-recorder-design.md)

## 一页结论

当前 AI Engine 已经有：

- `task`
- `node runtime`
- `event`
- `workflow output`

这些数据足够支撑运行态调试，但还不适合作为统一成本记账模型。

如果继续把成本信息塞进 `task / node runtime`，会让运行态与记账态职责混在一起；如果只为 `tts` 单独建账，又会导致后续 `llm / vlm / image / video` 再次重复建模。

本次建议立项，直接建设一套通用任务成本总账能力：

- 新建 `task_cost_trace` 作为统一成本明细主表
- `task` 只保留成本汇总字段
- `node runtime` 继续承担运行态和排障职责
- 成本统一在 Engine 成功收口点记账，而不是在 DSL tool 内部记账

同时，从第一天起统一支持以下资源类型：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`

本期先接 `tts`，但模型不做成 `tts` 专属。

## 为什么现在做

当前项目已经开始出现“成本能力缺口”：

1. 我们已经能在业务链路里拿到部分 usage 信息
例如 `tts_chars_total`、`tts_estimated_cost`。

2. 但系统还没有统一成本主表
导致这些数据只能留在 trace、cache、creative detail 里，不能形成正式可聚合成本口径。

3. 项目中的实际成本来源并不只有 TTS
一次生成可能同时消耗：

- `llm`
- `vlm`
- `tts`
- `image_generation`
- `video_generation`

如果现在只为 `tts` 建一套局部逻辑，后续很快会再次翻修。

4. 引擎已经有成熟的 sync / async / await 执行主链
这意味着现在正是把“运行态”和“记账态”职责拆清楚的好时机。

## 本期要做什么

本期建议做 5 件事：

1. 新建 `task_cost_trace`
作为统一成本明细主表。

2. 给 `task` 增加成本汇总字段
最少包括：

- `estimated_cost_total`
- `actual_cost_total`
- `cost_status`
- `cost_version`

3. 建立统一成本收口层
由 Engine 在节点成功 finalize 后统一写入成本明细。

4. 从第一天定义统一资源类型
包括：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`

5. 第一阶段只打通 `tts`
先让 `tts` 成为第一类真实入账的资源类型，再逐步扩到其他资源。

## 本期不做什么

- 不直接接供应商真实账单
- 不本期就落用户侧正式扣费
- 不一次性打通所有 provider 的真实成本
- 不把 `task_cost_trace` 做成财务总账系统
- 不在本期把所有资源都同时接入

本期重点是：先把“统一成本明细模型 + 正确收口位置”立起来。

## 建议方案

### 数据模型

统一引入：

- `task_cost_trace`

职责：

- 记录单条任务下的资源成本明细
- 支持多资源类型
- 支持估算成本和后续真实成本

推荐核心字段：

- `task_id`
- `root_task_id`
- `node_runtime_id`
- `node_name`
- `workflow_name`
- `resource_type`
- `provider`
- `model`
- `usage_quantity`
- `usage_unit`
- `estimated_cost`
- `actual_cost`
- `cost_status`
- `idempotency_key`
- `provider_request_id`
- `trace_payload`

其中：

- `vlm` 保持独立 `resource_type`
- `root_task_id` 用于子任务聚合

### 职责分层

`node runtime`

- 负责运行态
- 适合排障
- 不作为正式记账主数据

`task`

- 负责汇总
- 展示总成本
- 不存明细

`task_cost_trace`

- 负责资源级成本明细
- 作为正式记账主表

### 统一收口层

成本记账不建议挂在 DSL tool、worker 或 webhook handler。

最合理的挂点是：

- `Engine.runDAG(...)`
- 在节点 `finalizeNode(...)` 成功之后

原因是当前三条主链最终都会走到这里：

1. 同步 tool 节点
2. async worker 恢复节点
3. await webhook / poll_once / replay 恢复节点

所以推荐引入：

- `costRecorder.RecordNodeSuccess(...)`

由 Engine 在统一成功收口点调用。

## 为什么不建议把记账逻辑写进工具内部

工具内部最适合做的是：

- 产出 usage facts
- 产出 provider / model / request_id
- 产出 usage quantity / usage unit

不适合做的是：

- 正式记账
- 幂等去重
- 汇总回写
- 跨资源统一价格规则

如果把记账逻辑写进工具内部，会带来：

- 价格规则分散
- async / await 口径不一致
- 重试重复记账
- 子任务 / 父任务双记账
- 工具层职责污染

## 本期建议落地方式

### Phase 1

先落基础模型：

- `task_cost_trace` migration
- entity / repository / service
- `task` 成本汇总字段

### Phase 2

引入统一收口层：

- `NodeCostRecorder`
- `UsageExtractor`
- `TaskCostSummaryRefresher`

### Phase 3

先接 `tts`

当前 `tts` 已经有：

- `chars_total`
- `estimated_cost`
- `provider`
- `protocol`
- `fallback_chain`

因此最适合作为第一条真实成本写入链路。

### Phase 4

后续逐步接入：

- `llm`
- `vlm`
- `image_generation`
- `video_generation`

## 预期收益

业务收益：

- 一条任务到底花了多少钱可以说清楚
- 后续价格和积分模型不再拍脑袋
- 可以比较 `draft / publish` 成本差异

研发收益：

- 运行态与记账态职责分清
- 成本逻辑不再散落在 DSL tool 里
- sync / async / await 成本口径统一

运营收益：

- 能按任务、资源类型、provider、model 看成本
- 后续做日维度成本报表和资源占比统计更容易

工程收益：

- 第一阶段只接 `tts`，风险小
- 模型从第一天就为 `vlm / llm / image / video` 预留好了扩展位
- 后续接真实账单时不需要重做数据模型

## 主要风险

### 风险 1：记账点选错，导致重复入账

对策：

- 不在 DSL tool 内记账
- 不在 `AsyncWorker` / `CompleteAwaitNode` 内记账
- 统一挂在 `finalizeNode` 成功之后
- 使用 `idempotency_key`

### 风险 2：子任务与父任务双记账

对策：

- 谁真正调用 provider，谁写成本明细
- `root_task_id` 用于聚合，不让父节点重复补账

### 风险 3：本期只接 TTS，后续扩展又改模型

对策：

- 从第一天起将 `vlm / llm / image / video` 纳入统一 `resource_type`
- 本期只接 `tts`，但模型不做成 `tts` 专属

### 风险 4：记账失败影响主工作流

对策：

- recorder 失败不打断主流程
- 记日志和事件
- 任务终态时再做一次 reconcile

## 建议优先级

### P0

- `task_cost_trace` 表与实体
- repository / service
- `task` 汇总字段
- 统一收口层骨架
- `tts` 成本明细接入
- 查询接口与测试

### P1

- `llm / vlm` 成本明细
- `image_generation / video_generation` 成本明细
- 后台任务成本详情

### P2

- `actual_cost`
- 供应商账单对账
- 成本报表
- 与用户积分 / 定价联动

## 评审会需要拍板的事项

1. 是否确认 `task_cost_trace` 作为统一成本明细主表
2. 是否确认 `vlm` 保持独立 `resource_type`
3. 是否确认统一收口层挂在 `Engine finalize` 成功之后
4. 是否确认工具内部只产出 usage facts，不做正式记账
5. 是否确认 `task` 同步新增成本汇总字段
6. 是否确认本期先只落 `estimated_cost`

## 一句话结论

现在最合理的方向不是：

- 把成本继续塞进 `task / node runtime`
- 也不是只为 `tts` 单独建账

而是：

1. 建一张通用的 `task_cost_trace`
2. 用 Engine 的统一成功收口点来记账
3. 先从 `tts` 跑通第一条真实成本链路
4. 再逐步扩展到 `llm / vlm / image / video`

这样既不会污染运行态模型，也不会在后面接入更多资源类型时重复翻修成本体系。
