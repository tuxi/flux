# AI Engine 可选 Usage 协议评审摘要

日期：2026-04-26

状态：Draft

关联文档：

- [AI Engine 可选 Usage 协议设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-optional-usage-protocol-design.md)
- [AI Engine 可选 Usage 协议实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-optional-usage-protocol-task-breakdown.md)

## 1. 一页结论

当前 `task_cost_trace` 已经证明：

- Engine 在成功节点后统一记账这条路径是可行的
- `tts / llm / vlm / image_generation / video_generation` 已有第一版接入

但当前主路径仍然依赖“猜字段式 extractor”，长期维护成本高，也容易出现双记、漏记和排障困难。

本次建议的重构方向是：

1. **不改 `tool.Tool` 主接口**
2. **新增一个可选 Usage 协议**
3. **只有有成本语义的工具才实现这个协议**
4. **Engine 继续在 `runDAG -> finalizeNode` 成功后统一读取 usage**
5. **usage facts 不进入普通业务 output**
6. **usage 统一落到 `runtime.Checkpoint["usage_facts"] + task_cost_traces`**

一句话总结：

**工具显式提供 usage facts，Engine 统一收口记账，逐步淘汰猜字段。**

## 2. 为什么现在做

当前问题已经比较明确：

1. 规则隐式
只有熟悉实现的人才知道某个节点为什么会被计费。

2. 容易误记
例如 `video_generation` 曾经出现过 `submit / wait` 双记。

3. 新工具接入成本高
新增工具时，需要继续补 extractor 规则。

4. 排障效率低
某个工具没记上账时，很难快速判断到底是工具没产出，还是 recorder 没识别。

如果继续沿用猜字段主路径，后面接更多资源时，系统会越来越依赖少数人理解这套隐式规则。

## 3. 本期建议方案

### 方案核心

新增一个可选接口，例如：

```go
type UsageAwareTool interface {
    UsageSchema() tool.DataSchema
    BuildUsageFacts(input map[string]any, output map[string]any) ([]map[string]any, error)
}
```

语义如下：

- 普通工具：不实现，Engine 忽略
- 产生成本的工具：实现这个协议
- Engine 在成功节点后统一调用协议取 usage
- Engine 统一校验、入账、汇总

### 存储方式

- 正式账本：`task_cost_traces`
- 节点级副本：`runtime.Checkpoint["usage_facts"]`
- 不建议作为统一主路径写入普通业务 output

### 记账时机

继续使用当前已验证过的收口点：

- `runDAG -> finalizeNode` 成功之后

这条路径可以天然覆盖：

- 同步节点
- 异步恢复节点
- await / callback / replay 恢复节点

## 4. 为什么不直接改 `tool.Tool` 主接口

原因很直接：

1. 当前 `tool.Tool` 接口已经非常稳定
2. 大量工具并不需要成本能力
3. 如果主接口加 `UsageSchema()`，会带来大量空实现
4. 会把成本系统的职责强行塞进所有工具

因此更稳的方式是：

- **主接口保持不变**
- **通过可选协议扩展能力**

## 5. 为什么不把 usage 长期放进普通 output

虽然技术上可以把 `usage_facts` 放进业务 output，但不推荐作为统一主路径。

原因包括：

1. 污染业务 output 语义
2. 容易被 merge/cache/client response 误透传
3. 不符合“usage 是平台级运行元数据”的定位

因此更推荐：

- 工具通过可选协议提供 usage
- Engine 统一把 usage 写入 checkpoint 和 `task_cost_trace`

## 6. 对当前 Engine 的影响

### 不需要变的

- `runDAG` 主执行模型
- `finalizeNode` 时机
- `SetNodeOutput(...)` 业务 output 持久化逻辑

### 需要变的

1. 增加可选 usage 协议
2. 让 Engine 能识别当前工具是否实现这个协议
3. 在 `tryRecordNodeCost(...)` 中优先调用显式 usage 协议
4. 将 usage 写入：
   - `runtime.Checkpoint["usage_facts"]`
   - `task_cost_traces`
5. 旧 extractor 先保留兼容，后续逐步下线

结论是：

**对 Engine 的执行主链影响小，主要变化集中在“成功节点后如何读取 usage”这一步。**

## 7. 推荐迁移顺序

### P0

- 新增可选 usage 协议
- Engine 优先读取 usage 协议
- 标准化 `usage_facts` checkpoint
- 保留旧 extractor 兼容

### P1

优先迁移核心资源：

- `video_generation`
- `tts`
- `llm`
- `vlm`
- `image_generation`

### P2

- extractor 降级为 fallback
- 沉淀成新工具接入规范
- 节点级调试展示 `usage_facts`

## 8. 预期收益

完成后，成本体系会有这些明显提升：

1. 规则显式化
2. 新工具接入更简单
3. submit/completed 语义更清楚
4. 排障更直接
5. 更利于后续接 pricing resolver

## 9. 风险与注意事项

1. 迁移期会存在“双路径”
即：
- 新协议路径
- 旧 extractor fallback 路径

需要控制优先级，避免重复入账。

2. step/tool adapter 层需要提供识别能力
Engine 现在拿到的是 `node.Step`，不是直接拿到原始 tool，需要把 usage 协议能力透出来。

3. 不是所有工具都要一次性迁移
应该优先迁主成本资源，不必追求一轮改完。

## 10. 需要拍板的事项

本次评审建议重点确认以下几点：

1. 是否同意“显式 usage 协议”作为长期主路径
2. 是否同意不改 `tool.Tool` 主接口，只增加可选协议
3. 是否同意 usage 不作为统一主路径进入普通业务 output
4. 是否同意继续使用 `runDAG -> finalizeNode` 成功后作为统一记账点
5. 是否同意按 `video_generation -> tts -> llm -> vlm -> image_generation` 的顺序迁移

## 11. 一句话总结

本次方案的本质是：

**把成本采集从“Engine 猜 output 字段”升级成“工具显式声明 usage，Engine 统一收口入账”，同时尽量不破坏现有 `Tool` 主接口和 Engine 执行主链。**
