# AI Engine 可选 Usage 协议设计稿

日期：2026-04-26

状态：Draft

关联文档：

- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)
- [AI Engine Task Cost Trace 统一收口层设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-unified-recorder-design.md)
- [AI Engine Task Cost Usage Schema 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-usage-schema-design.md)
- [AI Engine Task Cost Pricing Resolver 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-pricing-resolver-design.md)

## 1. 背景

当前 `task_cost_trace` 主链已经成立：

1. 节点执行成功
2. `runDAG -> finalizeNode`
3. Engine 调用 `tryRecordNodeCost(...)`
4. recorder 识别 usage facts
5. 写入 `task_cost_traces`
6. 刷新 `task` 成本汇总

这条路径已经证明：

- 在 Engine 成功节点收口后统一记账是可行的
- `task_cost_trace` 适合作为资源用量明细账
- `tts / llm / vlm / image_generation / video_generation` 这几类资源已经可以接入

但当前的主问题仍然是：

**Engine 主要依赖“猜字段式 extractor”从节点 output 推断 usage。**

这会带来几个明显问题：

1. 规则隐式
只有熟悉实现的人才知道某个节点为什么被记账。

2. 容易双记或漏记
例如视频生成里，`submit` 和 `wait` 节点可能带有相似字段，但只有完成态节点才应该正式记账。

3. 工具接入成本高
每新增一种资源或一个新工具，就要继续改 extractor 规则。

4. 排障成本高
当某个工具没有记上成本时，很难快速判断：
- 工具没有产出 usage
- recorder 没识别
- 还是写库阶段失败

## 2. 核心结论

推荐后续重构方向为：

1. **不改 `tool.Tool` 主接口**
2. **新增一个可选的 Usage 协议**
3. **只有产生成本事实的工具才实现这个协议**
4. **Engine 在 `runDAG -> finalizeNode` 成功之后统一调用这个协议**
5. **usage facts 不混入普通业务 output**
6. **usage facts 由 Engine 统一写入 `checkpoint + task_cost_trace`**

一句话总结：

**工具通过可选协议提供 usage facts，Engine 在成功节点后统一收口记账。**

## 3. 设计目标

本设计希望解决以下问题：

1. 替代“猜字段式 extractor”作为长期主路径
2. 不污染业务 output
3. 不扩大 `tool.Tool` 主接口改动面
4. 保持 Engine 作为唯一正式记账入口
5. 让新工具接入成本体系时只需要实现一个明确协议
6. 为后续统一 pricing resolver 做好上游输入基础

## 4. 非目标

本设计不包含：

1. 一次性迁移所有已有工具
2. 立刻删除所有旧 extractor
3. 让工具直接输出权威价格
4. 完整 billing 扣费链路

本期重点是：定义“显式 usage 协议化”的长期演进方案。

## 5. 为什么不直接改 `tool.Tool` 主接口

当前主接口在 [event.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/tool/event.go) 中已经非常稳定：

- `InputSchema()`
- `OutputSchema()`
- `Execute(...)`

如果直接在 `tool.Tool` 上增加：

- `UsageSchema()`
- `BuildUsageFacts(...)`

会导致所有工具都要被动修改，即使它们本身没有任何成本相关能力。

这会带来：

1. 改动面过大
2. 大量无意义的空实现
3. 工具主协议被成本系统污染

因此，更合适的方式是：

**保持 `tool.Tool` 不变，新增一个可选能力协议。**

## 6. 推荐方案：可选 Usage 协议

建议新增一个可选接口，例如：

```go
type UsageAwareTool interface {
    UsageSchema() tool.DataSchema
    BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error)
}
```

也可以使用更明确的命名，例如：

```go
type CostUsageProvider interface {
    UsageSchema() tool.DataSchema
    BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error)
}
```

含义如下：

- 工具仍然实现原有 `tool.Tool`
- 只有有成本语义的工具才额外实现 `UsageAwareTool`
- Engine 在成功节点后判断当前工具是否实现这个协议
- 若实现，则调用它构造 usage facts

## 7. 为什么 usage 不应该放进普通 output

虽然把 `usage_facts` 混入 `OutputSchema` 也能实现，但这不是本设计推荐的主路径。

原因如下：

### 7.1 业务 output 会被污染

很多节点 output 是面向：

- 子工作流继续消费
- cache
- creative detail
- API 返回

如果把 usage facts 长期混在业务 output 里，会弱化业务 output 的语义边界。

### 7.2 更容易被业务链误透传

usage 是平台级运行元数据，不应该天然作为业务字段参与：

- merge
- output mapping
- creative detail
- client response

### 7.3 和“可选协议”思路不一致

既然希望 usage 成为独立能力，就更适合把它当作：

- 工具的附加协议产物
- Engine 的平台级副产物

而不是普通业务 output。

## 8. usage facts 最终存哪里

推荐双写两处：

### 8.1 正式明细账

写入：

- `task_cost_traces`

这是正式的持久化账本，用于：

- 成本汇总
- 查询展示
- 后续 billing / pricing

### 8.2 节点级副本

写入：

- `runtime.Checkpoint["usage_facts"]`

这是节点级运行副本，用于：

- 调试排障
- 节点级重放/reconcile
- 观察当前节点到底产出了什么 usage facts

### 8.3 不建议作为统一主路径写入普通 output

普通 output 仍然建议只承载业务结果。

## 9. 为什么 `checkpoint + task_cost_trace` 是最合适的存储落点

### 9.1 符合当前 Engine 结构

现在 Engine 在 [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go) 的 `tryRecordNodeCost(...)` 中，本来就会把识别后的事实写入：

- `task_cost_traces`
- `runtime.Checkpoint`

因此这条路径是已存在且已经验证过的。

### 9.2 便于排障

以后如果一个工具的成本没记上，可以直接查：

1. 工具有没有实现 `UsageAwareTool`
2. `BuildUsageFacts(...)` 是否返回了 usage
3. `runtime.Checkpoint["usage_facts"]` 有没有值
4. `task_cost_traces` 有没有写成功

这会比“猜字段”清晰很多。

### 9.3 不改变现有 output 语义

对业务链的侵入最小。

## 10. 对当前 Engine 的影响分析

### 10.1 成功记账时机保持不变

当前统一记账时机已经是正确的：

1. 节点执行成功
2. `finalizeNode` 成功
3. `tryRecordNodeCost(...)`

这个收口点能天然覆盖：

- 同步节点
- 异步恢复节点
- await / callback / replay 恢复节点

因此不需要改变 Engine 的执行主语义。

### 10.2 `SetNodeOutput(...)` 不需要改变

[SetNodeOutput](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflow/nodes/context.go) 目前负责：

- 根据 `OutputSchema` 校验业务 output
- 把业务 output 写入 runtime/output

由于本方案不要求 usage 混入 output，因此这层不需要做结构性调整。

### 10.3 主要变化发生在 `tryRecordNodeCost(...)`

当前 recorder 流程是：

1. 拿 `runtime.Output`
2. 通过 extractor 猜 usage
3. 写入 `task_cost_trace`

本方案下建议改成：

1. 先判断当前节点底层 tool 是否实现 `UsageAwareTool`
2. 若实现：
   - 调用 `BuildUsageFacts(input, output)`
   - 使用 `UsageSchema()` 校验
   - 写入 `task_cost_trace`
   - 写入 `runtime.Checkpoint["usage_facts"]`
3. 若未实现：
   - 短期可继续走旧 extractor 兼容
   - 长期逐步下线 extractor

## 11. 推荐协议方法设计

推荐保留两个方法：

### `UsageSchema()`

职责：

- 声明这个工具产出的 usage facts 结构
- 供 Engine 做统一校验

### `BuildUsageFacts(input, output)`

职责：

- 根据工具自己的输入/输出构建 usage facts
- 不负责写库
- 不负责算权威价格

这里推荐用 `BuildUsageFacts(...)` 而不是让工具直接返回 usage，原因是：

1. 不需要修改 `Execute(...)` 的签名
2. 不影响当前工具执行主流程
3. 更容易渐进迁移

## 12. 推荐 usage fact 结构

每条 usage fact 建议至少包含：

- `resource_type`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity`
- `usage_unit`
- `usage_breakdown`
- `billable`
- `billable_stage`

### `resource_type`

建议支持：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`
- `storage`
- `upload`
- `moderation`
- `other`

### `billable_stage`

建议至少支持：

- `submit`
- `completed`

其中正式入账默认只接受：

- `billable = true`
- `billable_stage = completed`

这样像视频生成的：

- `shot_submit`
- `shot_wait`

就可以自然区分：

- submit 事实
- completed 事实

从而避免双记。

## 13. 为什么这个方案优于当前 extractor

### 13.1 显式

记录规则来自工具声明，而不是 recorder 猜测。

### 13.2 可维护

接新资源时，只需要：

1. 工具实现协议
2. Engine 自动统一收口

### 13.3 可排障

问题定位链路会非常直接：

1. 工具有没有实现协议
2. usage facts 有没有构建出来
3. schema 校验是否通过
4. `task_cost_trace` 是否写入成功

### 13.4 更适合后续接 pricing

usage facts 和 pricing policy 可以保持彻底解耦。

## 14. 与主流工作流引擎模式的关系

主流工作流系统普遍有一个共同点：

- 业务结果和运行元数据分层
- 显式 metadata / artifact / side-channel
- 平台统一收口

本方案与这些模式一致：

- 不污染业务 output
- usage 通过独立协议产出
- Engine 统一持久化

这比继续使用“猜字段式 extractor”更接近平台化演进方向。

## 15. 迁移建议

推荐三阶段推进：

### 阶段一：并行兼容

1. 新增 `UsageAwareTool`
2. Engine 优先读取协议 usage
3. 旧 extractor 继续保留兜底

### 阶段二：优先迁移核心资源

优先迁移：

- `video_generation`
- `tts`
- `llm`
- `vlm`

因为这些是当前最核心的资源类型。

### 阶段三：收缩 extractor

1. 迁完主链工具
2. 将 extractor 降为 fallback
3. 最终下线猜字段主路径

## 16. 一句话结论

后续成本体系重构的最优方向是：

**保持 Engine 在成功节点后统一记账不变；新增一个可选的 Usage 协议，让产生成本的工具显式提供 usage facts；Engine 将这些 facts 写入 `runtime.Checkpoint["usage_facts"]` 和 `task_cost_traces`，而不是继续靠猜 output 字段。**
