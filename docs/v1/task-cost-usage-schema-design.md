# AI Engine Task Cost Usage Schema 设计稿

日期：2026-04-26

状态：Draft

关联文档：

- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)
- [AI Engine Task Cost Trace 统一收口层设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-unified-recorder-design.md)
- [AI Engine Task Cost Pricing Resolver 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-pricing-resolver-design.md)
- [AI Engine 工具成本输出契约](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tool-cost-output-contract.md)

## 1. 背景

当前 `task_cost_trace` 已经初步打通，Engine 会在节点成功后统一识别资源使用事实，并写入：

- `task_cost_traces`
- `task.estimated_cost_total`

这套链路已经证明：

1. 在 `runDAG -> finalizeNode` 成功之后做统一收口是可行的
2. `task_cost_trace` 作为资源明细账本是成立的
3. `tts / llm / vlm / image_generation / video_generation` 已有第一版接入

但当前实现仍有一个关键问题：

**资源使用事实主要依赖“猜字段式 extractor”。**

例如：

- 从 `api_task_id + api_provider` 猜测是视频生成
- 从 `prompt_tokens + completion_tokens` 猜测是 LLM/VLM
- 从 `chars_total` 猜测是 TTS

这种方式短期可用，但长期存在以下问题：

1. 规则隐式
只有熟悉实现的人才能判断“为什么这个节点被记账、为什么那个节点没记账”。

2. 容易误记
例如：

- `video_generation` 的 `submit` 节点和 `wait` 节点都含有相似字段
- 如果仅靠猜字段，容易双记

3. 扩展成本高
新资源接入时，需要不断新增或修改 extractor 规则，维护成本越来越高。

4. 排障效率低
当某个工具费用没记上时，很难直接判断：

- 是工具没输出
- 还是 extractor 没识别
- 还是 recorder 没写入

因此，后续成本体系应该从“猜字段式 extractor”升级到：

**节点显式输出 usage schema，Engine 在成功节点统一收口并持久化。**

## 2. 核心结论

推荐的长期方向是：

1. **节点负责显式输出 usage schema**
2. **Engine 在 `runDAG -> finalizeNode` 成功之后统一读取 usage schema**
3. **Engine 统一校验、入账、汇总**
4. **工具不直接写 `task_cost_trace`，也不直接承担权威价格计算**

一句话总结：

**工具负责输出 usage facts，Engine 负责统一记账。**

## 3. 设计目标

本设计目标：

1. 替代“猜字段式 extractor”作为主路径
2. 将资源用量从“隐式规则”升级为“显式输出契约”
3. 继续保留 `Engine finalize` 作为唯一正式记账入口
4. 让工具只负责输出 usage facts，不负责写库
5. 为后续接入统一 pricing resolver 做好输入基础

## 4. 非目标

本期不覆盖：

1. 直接改造所有已有工具
2. 一次性下线所有旧 extractor
3. 完整 pricing 计算能力
4. billing 正式扣费链路

本设计重点是：定义未来重构方向和推荐架构。

## 5. 为什么不能继续猜字段

### 5.1 猜字段本质上是“隐式语义”

例如当前 `video_generation` 的双记问题，本质不是数据库问题，而是：

- `shot_submit` 输出了 `api_provider + api_task_id`
- `shot_wait` 也输出了 `api_provider + api_task_id`

如果仅靠字段猜测，系统很难知道：

- 哪个节点只是“提交任务”
- 哪个节点才是“完成态结果”

### 5.2 规则会越来越依赖少数人记忆

继续往前堆规则会导致：

- 新资源接入越来越难
- 逻辑分散在 extractor 里
- 只有熟悉这套实现的人才知道“为什么这么算”

### 5.3 不利于契约化治理

如果没有 usage schema，平台无法明确回答：

- 这个工具到底是否应该产生成本事实
- 成本事实的字段结构是什么
- 哪个阶段才算 billable

## 6. 总体思路

后续建议引入统一的节点级 usage schema。

推荐模型：

```text
Tool Output
  -> 业务输出字段
  -> usage schema

Engine finalize
  -> 读取 usage schema
  -> 校验
  -> 转成 task_cost_trace
  -> 刷新 task 汇总
```

也就是说：

- 节点输出里显式包含 usage facts
- Engine 不再猜测“哪些字段像成本”
- Engine 只按约定 schema 收口

## 7. 节点输出 usage schema 建议

建议在节点 output 中增加一个保留字段，例如：

- `_usage`
或
- `usage_facts`

推荐使用：

- `usage_facts`

因为天然支持一个节点输出多笔使用事实。

### 推荐结构

```json
{
  "video_url": "https://...",
  "api_task_id": "cgt-xxx",
  "usage_facts": [
    {
      "resource_type": "video_generation",
      "provider": "volcengine",
      "model": "doubao-seedance-1-0-pro-fast-251015",
      "provider_request_id": "cgt-xxx",
      "usage_quantity": 1,
      "usage_unit": "jobs",
      "billable_stage": "completed",
      "billable": true,
      "usage_breakdown": {
        "duration_seconds": 3,
        "total_tokens": 62634,
        "completion_tokens": 62634
      }
    }
  ]
}
```

## 8. usage schema 建议字段

每条 `usage_fact` 建议至少包含：

- `resource_type`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity`
- `usage_unit`
- `usage_breakdown`
- `billable`
- `billable_stage`

### 字段说明

#### `resource_type`

建议枚举：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`
- `storage`
- `upload`
- `moderation`
- `other`

#### `provider`

例如：

- `edge`
- `volcengine`
- `qwen`
- `openai`

#### `model`

资源模型标识，可为空，但建议尽量提供。

#### `provider_request_id`

供应商任务 ID / 请求 ID。

#### `usage_quantity`

主计量值，例如：

- 1
- 3200
- 58

#### `usage_unit`

建议枚举：

- `jobs`
- `tokens`
- `chars`
- `images`
- `seconds`
- `requests`

#### `usage_breakdown`

扩展明细，例如：

- `prompt_tokens`
- `completion_tokens`
- `duration_seconds`
- `subtitle_sentence_count`

#### `billable`

表示这条 usage 是否应进入正式成本账。

#### `billable_stage`

建议枚举：

- `submit`
- `running`
- `completed`
- `finalized`

其中正式成本入账通常应要求：

- `billable = true`
- 且 `billable_stage` 为完成态

## 9. 为什么 usage schema 要允许节点不输出

不是所有工具都一定要产生成本事实。

例如：

- 纯粹的数据转换工具
- merge 工具
- 参数校验工具
- 只做本地拼装的工具

因此推荐约束是：

- 工具**可以不输出** `usage_facts`
- 如果没有输出，Engine 直接忽略
- 如果输出了，就按 schema 校验并记账

这样就能自然形成排障逻辑：

1. 某个工具费用没记上
2. 先看它有没有输出 `usage_facts`
3. 如果没有，就是工具输出问题
4. 如果有，再看 schema 是否合法
5. 如果 schema 合法，再看 Engine 是否成功入账

## 10. Engine 侧职责

Engine 应继续保持统一收口层职责。

当前推荐的收口时机不变：

- `runDAG`
- 节点 `finalizeNode` 成功之后

也就是保留现在的：

- `tryRecordNodeCost(...)`

但内部逻辑从：

- 猜字段 extractor

升级为：

- 读取 `usage_facts`
- 校验
- 写 `task_cost_trace`

### Engine 应做的事

1. 读取节点 output 中的 `usage_facts`
2. 校验 schema
3. 过滤：
   - `billable = false`
   - 非完成态 usage
4. 调用 PricingResolver
5. Upsert `task_cost_trace`
6. 刷新 `task` 汇总

### Engine 不应做的事

1. 不再猜测 output 字段语义
2. 不要求工具自己写数据库
3. 不把 pricing 逻辑下放给工具

## 11. 工具侧职责

工具职责应该收敛为：

1. 完成业务执行
2. 输出业务结果
3. 按约定输出 usage facts

工具**不负责**：

1. 写 `task_cost_trace`
2. 刷 `task` 汇总
3. 计算权威价格

### 工具侧的好处

这样可以让工具实现非常清晰：

- 我这个工具是否会产生资源使用
- 如果会，我输出什么 usage facts
- 如果不会，我就不输出

## 12. 为什么这比“工具直接输出 estimated_cost”更好

因为：

- `usage` 是事实
- `price` 是策略

工具输出 usage schema 后：

- pricing 可以统一计算
- 定价规则可以独立变更
- 历史账可以解释

如果工具直接输出权威 `estimated_cost`，会带来：

- 不同工具口径不一致
- 价格调整困难
- billing 难以统一

因此建议：

- 工具可以输出 `cost_hint`
- 但权威价格仍应由 Engine + PricingResolver 统一生成

## 13. 推荐演进路径

### Phase 1

保留现有 extractor 作为兼容兜底。

新增：

- `usage_facts` schema
- Engine 优先读取显式 usage

### Phase 2

逐步让核心计费工具输出 `usage_facts`：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`

### Phase 3

Engine 逐步从：

- 以 extractor 为主

迁移到：

- 以 usage schema 为主
- extractor 只做兼容旧节点

### Phase 4

稳定后，下线大部分猜字段 extractor。

## 14. 推荐改造优先级

建议优先改造这些资源：

### 1. `video_generation`

原因：

- 最容易出现 `submit/wait` 双记
- 完成态语义最明确

### 2. `tts`

原因：

- 当前已经有较完整 usage facts
- 与 pricing resolver 耦合最紧

### 3. `llm / vlm`

原因：

- usage 结构标准化程度高
- prompt/completion tokens 已较稳定

### 4. `image_generation`

原因：

- 需要进一步明确 submit/wait/poll_once 之间的完成态边界

## 15. 示例：video_generation 的正确 usage schema

### `shot_submit`

可以输出：

```json
{
  "api_task_id": "cgt-xxx",
  "usage_facts": [
    {
      "resource_type": "video_generation",
      "provider": "volcengine",
      "provider_request_id": "cgt-xxx",
      "usage_quantity": 1,
      "usage_unit": "jobs",
      "billable": false,
      "billable_stage": "submit"
    }
  ]
}
```

Engine 应忽略这条正式记账。

### `shot_wait`

应输出：

```json
{
  "video_url": "https://...",
  "usage_facts": [
    {
      "resource_type": "video_generation",
      "provider": "volcengine",
      "model": "doubao-seedance-1-0-pro-fast-251015",
      "provider_request_id": "cgt-xxx",
      "usage_quantity": 1,
      "usage_unit": "jobs",
      "billable": true,
      "billable_stage": "completed",
      "usage_breakdown": {
        "duration_seconds": 3,
        "total_tokens": 62634,
        "completion_tokens": 62634
      }
    }
  ]
}
```

Engine 应将其记入 `task_cost_trace`。

## 16. 与 task_cost_trace 的关系

这套设计不会替代 `task_cost_trace`。

关系应该是：

- `usage_facts` 是节点级输入契约
- `task_cost_trace` 是平台级成本账本

也就是说：

1. 工具输出 `usage_facts`
2. Engine 收口
3. PricingResolver 计算价格
4. 最终写入 `task_cost_trace`

## 17. 一句话结论

后续成本体系不应该继续依赖“猜字段”。

正确方向是：

**节点显式输出 usage schema，Engine 在成功节点统一收口并记账。**

这样既不把写库权交给工具，也不把记账语义藏在隐式规则里，后续新资源接入、排障、定价和 billing 都会清晰很多。
