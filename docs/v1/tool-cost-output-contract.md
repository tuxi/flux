# Tool Cost Output Contract

## 1. 目标

`task_cost_trace` 不应该直接依赖工具输出“最终费用”，而应该依赖各类会产生成本的工具输出统一的 **usage facts（可计费使用事实）**。

这些 usage facts 由工具或节点负责产出，Engine 统一收口层负责：

- 识别 `resource_type`
- 归一化 `provider / model / request_id`
- 根据 `pricing_version` 计算 `estimated_cost`
- 写入 `task_cost_trace`
- 刷新 `task` 汇总成本

本文档的目标不是定义一个脱离代码现状的理想接口，而是基于当前仓库里已存在的工具输出能力，定义：

1. 各类资源当前已经能输出哪些事实
2. 哪些工具已经基本满足成本契约
3. 哪些字段必须补齐
4. 每类资源的最小成本输出契约

## 2. 核心原则

### 2.1 工具输出的是 usage facts，不是正式账单

工具层应优先输出：

- `resource_type`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity`
- `usage_unit`
- `usage_breakdown`
- `trace_payload`

工具层可以顺手输出：

- `estimated_cost`
- `pricing_hint`
- `billable`

但系统正式入账时，不应把工具给出的 `estimated_cost` 当成唯一权威口径。统一收口层仍应保留二次校验和重算能力。

### 2.2 成本契约必须兼容 sync / async / await

对于同步工具，usage facts 可以在本次节点输出里直接给出。

对于异步工具，usage facts 不一定在 `submit` 阶段就齐全，经常要在：

- `wait`
- `poll_once`
- `await callback`

这些“结果收口节点”里补齐。

因此成本契约允许分阶段输出：

- `submit` 输出提交事实
- `wait/poll_once` 输出最终可计费事实
- `merge` 节点只做结构统一，不应丢失成本字段

### 2.3 merge 节点不能丢失 provider/model

统一收口层最终通常依赖节点输出做抽取。如果中间 merge 节点把：

- `model`
- `provider_request_id`
- `usage_breakdown`

丢掉，就会导致后续无法稳定入账。

当前图片链路已经出现这个问题：`image_provider_result_merge` 会把 `model` 输出成空字符串，这属于成本契约上的信息丢失。

## 3. 契约分层

### 3.1 L0：可识别提交事实

适用于 `submit` 类异步工具，至少要输出：

- `resource_type`
- `provider`
- `model`
- `provider_request_id`
- `task_mode`

这层只能支持“任务提交追踪”，还不能可靠入账。

### 3.2 L1：可识别最终结果事实

适用于 `wait / poll_once / callback` 类节点，至少要输出：

- `resource_type`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity`
- `usage_unit`

这层已经可以支持 `task_cost_trace` 的最小入账。

### 3.3 L2：可精细计费事实

建议额外输出：

- `usage_breakdown`
- `estimated_cost`
- `billable`
- `pricing_hint`
- `provider_status`
- `provider_payload`

这层可以支持更稳定的核算、排障和后续账单对账。

## 4. 各类资源现状分析

## 4.1 TTS

当前状态：**已基本满足 L2**

当前 TTS 链路已经能稳定输出：

- `tts_provider`
- `tts_protocol`
- `tts_chars_total`
- `tts_estimated_cost`
- `tts_fallback_chain`
- `tts_subtitle_sentences`
- `job_id / request_id / warnings`

尤其在 `paid_primary` 火山异步链路里，已经具备：

- `submit + wait`
- provider / protocol
- 字符数
- 估算成本
- job 追踪信息

结论：

- TTS 可以作为第一条接入 `task_cost_trace` 的标准样板
- 当前更多是字段命名还偏业务化，后续需要抽象成通用 usage facts

建议最小契约：

- `resource_type = tts`
- `provider`
- `model` 或 `voice_type`
- `provider_request_id`
- `usage_quantity = chars_total`
- `usage_unit = chars`
- `usage_breakdown.subtitle_sentence_count`
- `estimated_cost`

## 4.2 LLM

当前状态：**已基本满足 L1，接近 L2**

`ai-engine/pkg/llm/types.go` 已有统一 usage 结构：

- `PromptTokens`
- `CompletionTokens`
- `TotalTokens`

`ChatResponse` 同时包含：

- `Provider`
- `Model`
- `RequestedModel`
- `FinalProvider`
- `FinalModel`
- `FallbackHops`
- `Usage`

OpenAI provider 也已经把 usage 实际填充到了响应里，包括流式场景的 usage 收尾。

结论：

- LLM 是当前除 TTS 外最接近标准成本契约的一类
- 主要缺口不是 provider SDK 层，而是工作流工具层/节点输出是否把这些字段继续透传出来

建议最小契约：

- `resource_type = llm`
- `provider`
- `model`
- `provider_request_id`（如有）
- `usage_quantity = total_tokens`
- `usage_unit = tokens`
- `usage_breakdown.prompt_tokens`
- `usage_breakdown.completion_tokens`
- `usage_breakdown.fallback_hops`

## 4.3 VLM

当前状态：**未满足 L1**

`vlm_grounding_analyze` 当前主要输出的是业务语义结果：

- `subject_summary`
- `environment_summary`
- `style_summary`
- `raw_content`

它在运行时会发出开始事件，包含：

- `model_ep`

但最终工具输出里没有稳定暴露：

- `provider`
- `model`
- `provider_request_id`
- `usage tokens`
- `estimated_cost`

结论：

- VLM 当前还不能直接接入 `task_cost_trace`
- 需要先补 usage facts，再谈正式入账

建议最小契约：

- `resource_type = vlm`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity = total_tokens`
- `usage_unit = tokens`
- `usage_breakdown.prompt_tokens`
- `usage_breakdown.completion_tokens`
- `usage_breakdown.image_count`

当前必须补齐：

- 最终节点输出里的 `provider`
- 最终节点输出里的 `model`
- SDK/HTTP 响应里的 token usage 提取
- `provider_request_id`

## 4.4 Image Generation

当前状态：**submit 节点大多满足 L0，wait 节点部分满足 L1，merge 节点存在信息丢失**

### 4.4.1 Submit 节点

阿里和火山图片生成 submit 节点当前都已经输出：

- `api_task_id`
- `provider_task_id`
- `api_provider`
- `model`
- `task_mode`

这是比较标准的异步提交事实，已经满足 L0。

### 4.4.2 Wait / poll_once 节点

当前图片 wait/poll_once 一般会输出：

- `image_url`
- `width`
- `height`
- `provider_task_id`
- `api_provider`
- `model`

阿里 wait 还从响应里提取了 `usage.size`，并换算成了 `width / height`。

这已经比较接近 L1，但还缺少显式的“计费单位”表达。

### 4.4.3 Merge 节点

`image_provider_result_merge` 最终会统一输出：

- `image_url`
- `width`
- `height`
- `provider_task_id`
- `api_provider`
- `model`

但当前 `model` 被写成了空字符串，这会导致正式入账时丢失模型信息。

结论：

- 图片链路不是完全不能做成本契约，而是已经有不少基础字段
- 最大问题是缺少统一 usage unit，以及 merge 节点丢模型

建议最小契约：

- `resource_type = image_generation`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity = image_count`
- `usage_unit = images`
- `usage_breakdown.width`
- `usage_breakdown.height`
- `usage_breakdown.size`

当前必须补齐：

- 显式输出 `image_count`
- merge 节点保留 `model`
- 可选输出 `estimated_cost`

## 4.5 Video Generation

当前状态：**大多只满足 L0，少数 wait 节点部分满足 L1**

### 4.5.1 通用视频生成

`video_generate_submit` / `video_generate_wait` 这条链路当前主要输出：

- `api_task_id`
- `api_provider`
- `video_url`

缺少：

- `model`
- `duration_seconds`
- `job_count`
- `provider_request_id` 的统一表达

### 4.5.2 Motion Control

`kling_motion_submit` / `volc_motion_submit` 当前提交结果基本只有：

- `api_task_id`
- `api_provider`

这只能视为 L0。

### 4.5.3 Goods Shot I2V

`goods_shot_i2v_wait` 相对更强，它的 provider 返回里实际上已经包含：

- `model`
- `duration`
- `resolution`
- `usage.total_tokens`
- `usage.completion_tokens`

但当前最终工具输出只保留了：

- `video_url`
- `api_task_id`
- `api_provider`

也就是说，这条链路的 provider 原始响应里已经有可计费事实，但最终节点把它们丢掉了。

结论：

- 视频类目前整体还不能稳定入账
- 但 `goods_shot_i2v_wait` 是最值得优先补的一条，因为基础数据已经存在

建议最小契约：

- `resource_type = video_generation`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity = job_count`
- `usage_unit = jobs`

建议增强契约：

- `usage_breakdown.duration_seconds`
- `usage_breakdown.resolution`
- `usage_breakdown.ratio`
- `usage_breakdown.total_tokens`
- `usage_breakdown.completion_tokens`

当前必须补齐：

- wait 节点输出 `model`
- wait 节点输出 `duration_seconds`
- 可选输出 `total_tokens`
- 统一 `provider_request_id`

## 5. 每类资源的最小成本输出契约

## 5.1 通用字段

所有会产生成本的节点，建议统一使用以下字段名：

- `resource_type`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity`
- `usage_unit`
- `usage_breakdown`
- `estimated_cost`
- `billable`
- `provider_status`
- `trace_payload`

### 字段说明

- `resource_type`
  - `tts / llm / vlm / image_generation / video_generation`
- `provider`
  - 厂商名，如 `openai / volcengine / aliyun / kling`
- `model`
  - 实际调用的模型、音色或 endpoint
- `provider_request_id`
  - 厂商侧任务号、请求号或 job id
- `usage_quantity`
  - 最主要的计费数量
- `usage_unit`
  - `chars / tokens / images / jobs / seconds`
- `usage_breakdown`
  - 细分计量信息
- `estimated_cost`
  - 工具侧可选预估值
- `billable`
  - 当前调用是否应计费
- `provider_status`
  - `submitted / running / completed / failed`
- `trace_payload`
  - 保留必要的 provider 原始计费信息

## 5.2 TTS

- `resource_type = tts`
- `provider`
- `model` 或 `voice_type`
- `provider_request_id`
- `usage_quantity = chars_total`
- `usage_unit = chars`
- `usage_breakdown.subtitle_sentence_count`
- `estimated_cost`

## 5.3 LLM

- `resource_type = llm`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity = total_tokens`
- `usage_unit = tokens`
- `usage_breakdown.prompt_tokens`
- `usage_breakdown.completion_tokens`

## 5.4 VLM

- `resource_type = vlm`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity = total_tokens`
- `usage_unit = tokens`
- `usage_breakdown.prompt_tokens`
- `usage_breakdown.completion_tokens`
- `usage_breakdown.image_count`

## 5.5 Image Generation

- `resource_type = image_generation`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity = image_count`
- `usage_unit = images`
- `usage_breakdown.width`
- `usage_breakdown.height`

## 5.6 Video Generation

- `resource_type = video_generation`
- `provider`
- `model`
- `provider_request_id`
- `usage_quantity = job_count`
- `usage_unit = jobs`
- `usage_breakdown.duration_seconds`
- `usage_breakdown.resolution`

## 6. 建议的落地顺序

### P0

先接已经接近契约的资源：

1. `tts`
2. `llm`

### P1

补最容易改造的异步资源：

1. `goods_shot_i2v_wait`
2. `image wait/poll_once`
3. `image_provider_result_merge` 保留 `model`

### P2

补尚未暴露 usage 的资源：

1. `vlm_grounding_analyze`
2. `video_generate_wait`
3. `motion_control` 系列

## 7. 评审建议

本期评审建议拍板以下事项：

1. 成本契约是否统一使用本文档中的通用字段名
2. `estimated_cost` 是否允许由工具先给出，但正式入账仍以统一收口层为准
3. `submit` 节点是否只承担 L0，不强求直接可入账
4. `wait/poll_once/merge` 节点是否必须承担最终成本事实输出责任
5. 图片和视频链路是否接受“先补最小 usage facts，再接正式计费”的分阶段推进方式
