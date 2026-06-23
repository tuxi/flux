# AI Engine Task Cost Pricing Resolver 设计稿

日期：2026-04-26

状态：Draft

关联文档：

- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)
- [AI Engine Task Cost Trace 统一收口层设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-unified-recorder-design.md)
- [AI Engine 工具成本输出契约](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tool-cost-output-contract.md)

## 1. 背景

当前 `task_cost_trace` 已经开始记录以下资源的使用事实：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`

当前阶段已经能做到：

1. 在节点成功收口时提取 `usage facts`
2. 将资源用量写入 `task_cost_trace`
3. 对部分资源记录 `estimated_cost`

但现有“价格计算”能力仍然存在明显问题：

1. 价格逻辑分散
有的 provider 内部直接算成本，有的 extractor 只记 usage，没有统一定价入口。

2. 价格口径不稳定
供应商调价、模型变更、活动价、环境差异，会导致历史记录和当前逻辑难以解释。

3. `usage` 和 `price` 混在一起
资源使用事实本身相对稳定，但价格是策略，应该从架构上分层。

4. 后续无法平滑接入 billing
如果价格逻辑继续散在各个工具、provider 和 recorder 中，后面接入报价、积分、结算和报表会越来越乱。

因此，需要补一层独立的统一定价能力：`PricingResolver`。

## 2. 核心结论

统一 pricing 入口的本质是：

把“资源使用事实”和“价格策略”拆开，让 `task_cost_trace` 先稳定记录 `usage`，再由独立的 `PricingResolver` 统一把：

- `resource_type`
- `provider`
- `model`
- `usage`

解析成：

- `unit_price`
- `pricing_version`
- `estimated_cost`

这样后面不管：

- 供应商怎么变
- 价格怎么调
- 是否接入正式 billing
- 是否需要重算报价

都不会破坏现有成本体系。

## 3. 设计目标

本设计目标：

1. 为所有资源提供统一的价格解析入口
2. 将 `usage facts` 与 `pricing policy` 明确分层
3. 让 `task_cost_trace` 能稳定记录：
   - `unit_price`
   - `estimated_cost`
   - `pricing_version`
4. 支持后续接入：
   - 报价
   - billing
   - 积分换算
   - 成本报表
5. 支持价格规则版本化与生效时间管理

## 4. 非目标

本期不覆盖：

1. 直接对接供应商真实账单做自动对账
2. 一次性支持所有复杂阶梯价、包量价、折扣价
3. 用户侧正式扣费结算
4. 后台价格管理系统完整实现

本期重点是：先定义统一 pricing 架构和落地顺序。

## 5. 总体思路

统一 pricing 层建议拆成三层：

### 5.1 Usage Fact

`UsageFact` 是成本事实层，描述“资源到底用了什么”。

典型字段：

- `resource_type`
- `provider`
- `model`
- `usage_quantity`
- `usage_unit`
- `usage_breakdown`
- `provider_request_id`
- `trace_payload`

这一层由：

- tool output
- extractor
- cost recorder

共同提供或收口。

### 5.2 Pricing Rule

`PricingRule` 是定价策略层，描述“某种资源应该怎么计价”。

典型字段：

- `resource_type`
- `provider`
- `model_pattern`
- `usage_unit`
- `price_type`
- `unit_price`
- `currency`
- `pricing_version`
- `effective_from`
- `effective_to`
- `is_active`

### 5.3 Pricing Resolver

`PricingResolver` 是统一入口，负责：

1. 根据 `UsageFact` 匹配 `PricingRule`
2. 计算 `unit_price`
3. 输出 `estimated_cost`
4. 打上 `pricing_version`
5. 将结果交给 `task_cost_trace`

## 6. 为什么要把 usage 和 pricing 拆开

### usage 是事实

例如：

- `tts` 合成了多少字符
- `llm / vlm` 消耗了多少 token
- `image_generation` 生成了几张图
- `video_generation` 发起了几次任务、多少秒

这些数据一旦记录，通常不应因为价格变化而变化。

### price 是策略

例如：

- 火山 TTS 从 `3 元 / 万字符` 调整到 `3.5 元 / 万字符`
- `gpt-4o-mini` 的 token 价格变化
- 测试环境按成本价，生产环境按结算价
- 某个 provider 促销期价格下调

这些都属于策略变化，不应污染 usage 主数据。

结论：

- `task_cost_trace` 应优先保证 usage 稳定
- price 应通过独立规则层解析

## 7. 定价规则模型建议

建议引入统一 `PricingRule` 概念。

### 建议字段

- `resource_type`
- `provider`
- `model_pattern`
- `usage_unit`
- `price_type`
- `unit_price`
- `currency`
- `pricing_version`
- `effective_from`
- `effective_to`
- `priority`
- `is_active`
- `description`

### `price_type` 建议枚举

- `per_10k_chars`
- `per_1k_tokens`
- `per_image`
- `per_second`
- `per_job`
- `fixed`

### `usage_unit` 建议枚举

- `chars`
- `tokens`
- `images`
- `seconds`
- `jobs`
- `requests`

## 8. 匹配顺序建议

PricingResolver 建议按“从精确到宽松”的顺序匹配：

1. `resource_type + provider + model` 精确匹配
2. `resource_type + provider + model_pattern`
3. `resource_type + provider`
4. `resource_type` 默认规则

例如：

- `tts + volcengine + BV104_streaming`
- `llm + openai + gpt-4o-mini`
- `image_generation + aliyun`
- `video_generation` 默认按 `per_job`

这样可以避免：

- model 微调后整条规则失效
- 每个 provider 都必须写死所有 model

## 9. 各类资源建议定价口径

### `tts`

- `usage_unit = chars`
- `price_type = per_10k_chars`

建议支持：

- `provider`
- `voice_type / model`
- `draft/publish` 不同策略下的不同价格规则

### `llm`

- `usage_unit = tokens`
- `price_type = per_1k_tokens`

第一版可以先统一按 `total_tokens` 估算。

后续可扩展为：

- `prompt_tokens_price`
- `completion_tokens_price`

### `vlm`

建议与 `llm` 分开作为独立 `resource_type`。

原因：

- provider/model 不同
- 价格口径通常不同
- 后续视觉输入 token 规则可能不同

### `image_generation`

- `usage_unit = images`
- `price_type = per_image`

第一版先按“每张图”估价，后续再考虑：

- 分辨率差异
- 高质量模式差异
- 多图任务差异

### `video_generation`

第一版建议：

- `usage_unit = jobs`
- `price_type = per_job`

后续可扩展：

- `usage_unit = seconds`
- `price_type = per_second`

## 10. 与 task_cost_trace 的关系

`task_cost_trace` 最终建议至少包含：

- `resource_type`
- `provider`
- `model`
- `usage_quantity`
- `usage_unit`
- `unit_price`
- `estimated_cost`
- `currency`
- `pricing_version`
- `status`

如果当前表结构里还没有单独的 `pricing_version` 字段，建议作为下一阶段补充项加入。

这样后续才能回答：

1. 某条历史记录是按哪套价格算的
2. 为什么当前价格改了，但旧任务成本不变
3. 是否需要对历史 usage 做重算

## 11. 推荐代码架构

建议新增一层独立 pricing 包，例如：

```text
ai-engine/pricing/
  types.go
  resolver.go
  rules.go
  calculator.go
  providers/
    static_rules.go
```

职责建议如下：

### `types.go`

定义：

- `PricingInput`
- `PricingResult`
- `PricingRule`

### `resolver.go`

定义统一接口：

```go
type Resolver interface {
    Resolve(ctx context.Context, fact cost.UsageFact) (*PricingResult, error)
}
```

### `rules.go`

负责：

- 规则加载
- 规则匹配
- 生效时间过滤

### `calculator.go`

负责：

- 单位换算
- 成本计算
- rounding 规则

## 12. 与 CostRecorder 的衔接方式

推荐流程如下：

```text
Tool Output
  -> Usage Extractor
  -> CostRecorder
  -> PricingResolver
  -> TaskCostTraceRepository.Upsert
```

即：

1. extractor 先提取 usage facts
2. recorder 不再自己散落计算价格
3. recorder 调用 `PricingResolver.Resolve(...)`
4. 将返回的：
   - `unit_price`
   - `estimated_cost`
   - `currency`
   - `pricing_version`
   写入 `task_cost_trace`

## 13. 推荐演进顺序

### Phase 1

先定义统一接口，不改后台配置系统。

实现：

- `PricingResolver`
- 静态代码规则
- 先覆盖：
  - `tts`
  - `llm`
  - `vlm`
  - `image_generation`
  - `video_generation`

### Phase 2

把当前零散价格计算迁进统一入口。

优先项：

1. `tts` 价格计算迁移到 resolver
2. `llm/vlm` 增加静态价格规则
3. `image/video` 增加基础估价规则

### Phase 3

引入数据库配置化规则：

- `billing_pricing_rules`
- 生效时间
- 版本管理
- 后台管理入口

### Phase 4

接入更复杂能力：

- 阶梯价
- 包量价
- 用户补贴价
- 实际结算价
- 历史重算能力

## 14. 第一版落地建议

为了避免过度设计，第一版建议这样做：

1. 先定义 `PricingResolver` 接口
2. 先用静态规则实现
3. 先接入 recorder，不动 billing
4. 先把 `tts` 现有价格迁进去
5. 再补 `llm / vlm / image_generation / video_generation`

这样可以快速获得：

- 统一价格口径
- 统一版本标记
- 稳定的 `task_cost_trace`

同时避免一开始就做过重的管理后台和规则中心。

## 15. 待完成项

后续建议拆成以下任务：

1. 设计 `PricingResolver` 代码接口
2. 在 `task_cost_trace` 增加 `pricing_version`
3. 把 `tts` 当前 `estimated_cost` 迁移到 resolver
4. 为 `llm / vlm / image_generation / video_generation` 建立第一版静态规则
5. 为后台成本展示补充“价格版本”和“计价规则来源”
6. 后续再评估是否升级为 `billing_pricing_rules` 配置化方案

## 16. 一句话结论

统一 pricing 入口不是为了“现在马上做 billing”，而是为了从现在开始把：

- 资源使用事实
- 价格策略

这两件事分开治理。

这样 `task_cost_trace` 才能真正成为长期稳定的成本底座，而不是一套随着 provider 和价格变化不断漂移的临时实现。
