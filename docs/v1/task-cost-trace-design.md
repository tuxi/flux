# AI Engine Task Cost Trace 设计稿

日期：2026-04-25

状态：Draft

关联文档：

- [AI Engine TTS Provider 技术设计文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-technical-design.md)
- [Engine Await Runtime V1 PRD](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/engine-await-runtime-v1-prd.md)

## 1. 背景

当前引擎已经有：

- `task`
- `node runtime`
- `event`
- `workflow output`

这些数据足够支撑运行态调试，但还不适合承担统一成本记账职责。

当前问题有两类：

1. 如果把成本信息继续塞进 `task / node runtime`
会让运行态字段和记账字段混在一起，职责不清，后续聚合和报表也不稳定。

2. 如果只为 `tts` 单独建表
会导致口径碎片化。一个完整生成任务的成本实际来自多种资源：

- `llm`
- `vlm`
- `tts`
- `image_generation`
- `video_generation`
- 未来可能还包括 `storage / upload / cdn / moderation`

因此，本期建议直接引入一张面向“任务成本明细”的通用表：`task_cost_trace`。

## 2. 设计目标

本设计目标：

1. 为单个 `task` 记录完整的资源成本明细
2. 从第一天起支持 `tts / llm / vlm / image / video`
3. 兼容当前“先记预估成本，后补实际成本”的落地方式
4. 与 `task / node runtime` 分层清晰，不污染运行态模型
5. 支持后续成本报表、定价、积分、对账和运营分析

## 3. 非目标

本期不覆盖：

1. 直接接入供应商账单系统进行日账单自动对账
2. 用户侧正式扣费结算逻辑
3. 一次性把所有 provider 的真实成本都打通
4. 完整财务总账系统

本期重点是：先把 AI Engine 内部的统一成本明细数据模型立起来。

## 4. 总体思路

三层职责建议如下：

### `node runtime`

用于记录运行态信息：

- 节点输入输出
- 状态流转
- 重试信息
- provider 原始结果
- 调试细节

适合排障，不适合作为长期记账主数据。

### `task`

用于记录任务级汇总信息：

- 任务状态
- 最终结果
- 汇总成本
- 成本状态

适合作为任务总览，不适合存明细。

### `task_cost_trace`

用于记录资源级成本明细：

- 该任务用了什么资源
- 每种资源消耗了多少
- 由哪个节点产生
- 供应商和模型是谁
- 预估成本和实际成本是多少

结论：

- `node runtime` 负责运行
- `task` 负责汇总
- `task_cost_trace` 负责记账

## 5. 表模型建议

表名建议：`public.task_cost_trace`

推荐字段如下。

### 主键与关联字段

- `id`
- `task_id`
- `workflow_name`
- `node_runtime_id`
- `node_name`
- `scene_type`
- `scene_key`

说明：

- `task_id` 是主关联字段
- `node_runtime_id` 可为空
  适用于后续某些任务级聚合成本不直接对应单个 runtime 的场景
- `node_name` 便于直接定位来源节点

### 资源识别字段

- `resource_type`
- `provider`
- `model`
- `sub_resource_type`

建议的 `resource_type` 枚举：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`
- `storage`
- `upload`
- `cdn`
- `moderation`
- `other`

说明：

- `vlm` 需要作为独立资源类型保留，不应并入 `llm`
- `sub_resource_type` 用于细分口径，例如：
  - `tts_async_long_text`
  - `llm_prompt_generation`
  - `vlm_image_understanding`
  - `video_i2v`

### 用量字段

- `usage_quantity`
- `usage_unit`
- `usage_input_quantity`
- `usage_output_quantity`
- `usage_payload`

建议的 `usage_unit` 示例：

- `chars`
- `tokens`
- `images`
- `seconds`
- `jobs`
- `requests`
- `bytes`

说明：

- `usage_quantity` 是统一主口径
- `usage_input_quantity / usage_output_quantity` 用于像 `llm / vlm / tts` 这类存在输入输出双维度的资源
- `usage_payload` 用于记录扩展明细，例如：
  - `input_tokens`
  - `output_tokens`
  - `subtitle_sentence_count`
  - `duration_seconds`

### 成本字段

- `currency`
- `unit_price`
- `estimated_cost`
- `actual_cost`
- `cost_status`
- `pricing_version`

建议的 `cost_status`：

- `estimated`
- `actualized`
- `waived`
- `failed`
- `unknown`

说明：

- 本期先写 `estimated_cost`
- 后续若拿到供应商真实账单，再补 `actual_cost`
- `pricing_version` 用于避免价格调整后历史口径不清

### 幂等与追踪字段

- `idempotency_key`
- `provider_request_id`
- `provider_job_id`
- `trace_id`
- `trace_payload`

说明：

- `idempotency_key` 用于避免重试重复记账
- `trace_payload` 记录供应商原始计费依据和补充信息，不把所有细节拆成字段

### 生命周期字段

- `created_at`
- `updated_at`
- `deleted_at`

## 6. 建表建议

建议表结构可表达为：

```sql
create table public.task_cost_trace (
    id bigserial primary key,
    task_id bigint not null,
    workflow_name varchar(128) not null default '',
    node_runtime_id bigint null,
    node_name varchar(128) not null default '',
    scene_type varchar(64) not null default '',
    scene_key varchar(128) not null default '',

    resource_type varchar(64) not null,
    sub_resource_type varchar(128) not null default '',
    provider varchar(64) not null default '',
    model varchar(128) not null default '',

    usage_quantity numeric(20,6) not null default 0,
    usage_unit varchar(32) not null default '',
    usage_input_quantity numeric(20,6) not null default 0,
    usage_output_quantity numeric(20,6) not null default 0,
    usage_payload jsonb not null default '{}'::jsonb,

    currency varchar(16) not null default 'CNY',
    unit_price numeric(20,8) not null default 0,
    estimated_cost numeric(20,8) not null default 0,
    actual_cost numeric(20,8) not null default 0,
    cost_status varchar(32) not null default 'estimated',
    pricing_version varchar(64) not null default '',

    idempotency_key varchar(191) not null default '',
    provider_request_id varchar(191) not null default '',
    provider_job_id varchar(191) not null default '',
    trace_id varchar(191) not null default '',
    trace_payload jsonb not null default '{}'::jsonb,

    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    deleted_at timestamptz null
);
```

建议索引：

```sql
create index idx_task_cost_trace_task_id
    on public.task_cost_trace (task_id);

create index idx_task_cost_trace_node_runtime_id
    on public.task_cost_trace (node_runtime_id);

create index idx_task_cost_trace_resource_type
    on public.task_cost_trace (resource_type);

create index idx_task_cost_trace_created_at
    on public.task_cost_trace (created_at desc);

create unique index uk_task_cost_trace_idempotency_key
    on public.task_cost_trace (idempotency_key)
    where deleted_at is null and idempotency_key <> '';
```

## 7. 字段语义约定

### `usage_quantity`

统一主度量。

示例：

- `tts`: 字符数
- `llm`: token 总量
- `vlm`: token 总量
- `image_generation`: 图片张数
- `video_generation`: 视频秒数或任务数

### `unit_price`

与 `usage_unit` 对应。

示例：

- `tts`: 元 / 字符 或 元 / 万字符折算后的单字符价
- `llm`: 元 / token
- `image_generation`: 元 / 张
- `video_generation`: 元 / 秒 或 元 / 次

### `estimated_cost`

内部估算成本，用于：

- 任务成本统计
- 运营看数
- 后续积分模型

### `actual_cost`

供应商真实账单或对账后的成本。

本期可先恒为 `0`。

### `trace_payload`

用于存结构化扩展信息，建议按资源类型写不同内容。

示例：

```json
{
  "mode": "publish",
  "protocol": "async",
  "fallback_chain": ["paid_primary"],
  "subtitle_sentence_count": 3,
  "wait_timeout_ms": 90000
}
```

## 8. 资源类型口径建议

### `tts`

建议口径：

- `usage_quantity = chars_total`
- `usage_unit = chars`
- `provider = paid_primary / edge / volc / aliyun`
- `model = voice_type`
- `estimated_cost = chars_total * 单价`

### `llm`

建议口径：

- `usage_quantity = total_tokens`
- `usage_input_quantity = prompt_tokens`
- `usage_output_quantity = completion_tokens`
- `usage_unit = tokens`

### `vlm`

建议口径：

- `resource_type = vlm`
- 不与 `llm` 合并
- `usage_quantity = total_tokens`
- `usage_input_quantity` 可记录图像理解输入 token
- `usage_output_quantity` 可记录文本输出 token

原因：

- `vlm` 在业务上和纯文本 `llm` 成本结构不同
- 后续更利于统计“图像理解 / 商品理解 / 视觉质检”等环节的成本

### `image_generation`

建议口径：

- `usage_quantity = image_count`
- `usage_unit = images`

### `video_generation`

建议口径：

- 可按 `jobs` 或 `seconds`
- 第一阶段建议先按 `jobs`
- 若后续供应商价格与秒数强相关，再补 `seconds`

## 9. 写入时机设计

推荐分两种写入时机：

### 9.1 节点成功后立即写一条成本明细

适用于：

- `tts`
- `llm`
- `vlm`
- `image_generation`
- `video_generation`

原则：

- 只在成本口径已经明确时写入
- 写入后即视为该资源已被消耗

### 9.2 任务完成后回写汇总

任务成功、失败或取消时，统一汇总 `task_cost_trace`：

- `estimated_cost_total`
- `actual_cost_total`
- `cost_status`

回写到 `task` 表。

## 10. 与 task / node runtime 的关系

### `task`

建议新增汇总字段：

- `estimated_cost_total`
- `actual_cost_total`
- `cost_status`
- `cost_version`

作用：

- 任务列表快速展示总成本
- 后续任务级报表

### `node runtime`

不建议把成本明细直接做成正式记账主数据。

可以保留少量运行态辅助字段或 output trace，例如：

- `provider`
- `usage_summary`
- `warnings`

但成本以 `task_cost_trace` 为准。

### 关系原则

1. `node runtime` 是运行态来源
2. `task_cost_trace` 是记账明细
3. `task` 是汇总视图

## 11. 幂等与重复写入控制

成本写入最重要的风险是重试重复入账。

建议 `idempotency_key` 按以下规则生成：

```text
task_id + node_name + resource_type + provider_request_id
```

如果没有 `provider_request_id`，则退化为：

```text
task_id + node_name + resource_type + runtime_attempt
```

原则：

1. 同一次真实资源消耗只能写一次
2. provider 超时重试但供应商已执行成功时，不能重复记账
3. 需要允许不同资源类型在同一节点下各写一条

## 12. 与 await runtime 的兼容

对于 `await` 类异步 provider：

1. `submit` 阶段不一定立刻写成本
2. 若供应商在 `submit` 后就确定会扣费，可先写 `estimated`
3. 若只有在完成后才算消耗，则在 `await callback / poll_once` 成功收口时写入

建议规则：

- `submit accepted` 但未确认消耗时，不记正式成本项
- `completed` 且拿到可计量 usage 后，写 `task_cost_trace`

## 13. 分阶段落地建议

### Phase 1

先只接 `tts`

目标：

- 表结构落地
- `tts_estimated_cost` 写入 `task_cost_trace`
- `task` 回写 `estimated_cost_total`

### Phase 2

接入 `llm / vlm`

目标：

- prompt / completion / multimodal token 成本统一入账

### Phase 3

接入 `image_generation / video_generation`

目标：

- 完成单条任务全链路 AI 资源成本闭环

### Phase 4

引入 `actual_cost`

目标：

- 对接供应商账单或内部对账流水

## 14. 本期推荐实现

本期最小可落地方案：

1. 新建 `task_cost_trace` 表
2. 新增 `resource_type`，并从第一天支持：
   - `tts`
   - `llm`
   - `vlm`
   - `image_generation`
   - `video_generation`
3. 当前先只写 `tts`
4. `task` 表新增：
   - `estimated_cost_total`
   - `actual_cost_total`
   - `cost_status`
5. `node runtime` 不做正式记账，只保留运行态 trace

## 15. 评审会需拍板的事项

1. 表名是否确定为 `task_cost_trace`
2. `vlm` 是否作为独立 `resource_type`
3. `video_generation` 第一阶段按 `jobs` 还是 `seconds`
4. `task` 是否同步新增成本汇总字段
5. 本期是否只落 `estimated_cost`

## 16. 结论

当前最合理的路径不是：

- 把成本继续塞进 `task / node runtime`
- 也不是只为 `tts` 单独建账

而是：

1. 新建统一的 `task_cost_trace`
2. 从第一天支持 `tts / llm / vlm / image / video`
3. 先从 `tts` 写第一条成本明细
4. 后续逐步扩展为完整生成成本总账

这样既不会污染运行态模型，也不会在后面接入更多 AI 资源时重复翻修数据模型。
