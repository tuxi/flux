# Video Workflow Input Contract Cleanup

## 背景

随着 `await runtime`、`provider_router`、`callback contract` 和 `poll_once` 逐步落地，视频链路的输入边界已经发生了变化。

当前 `text_to_video` / `image_to_video` workflow 中，仍有一部分字段保留在 `input.*`，但它们实际上已经不再属于客户端业务输入，而应该由：

- `provider_router`
- workflow 运行时
- 系统内部调优配置

统一决定。

如果继续把这些字段暴露给客户端，会带来几个问题：

1. `provider_router` 和客户端输入出现双来源，削弱 router 的单一真相来源地位
2. request-callback provider 的回调配置仍可能被客户端错误覆盖
3. 系统内部调优参数被误当成产品能力参数长期暴露
4. debug/兼容字段长期残留在正式 API 契约中

本文档只聚焦**视频链路**：

- `text_to_video`
- `image_to_video`
- 与其共享的 submit / intent / prompt / cache 相关 tool

## 当前视频链路输入字段盘点

### 客户端业务输入

这些字段当前仍然属于用户真实表达的创作需求，应继续保留：

- `user_prompt`
- `images`（仅 `image_to_video`）
- `duration`
- `resolution`
- `aspect_ratio`
- `style`
- `negative_prompt`
- `watermark`
- `enable_audio`
- `fps`
- `mode`
- `model`

### 已经不应再由客户端直接控制的字段

这些字段要么已经被 `provider_router` 接管，要么属于运行时基础设施：

- `api_provider`
- `callback_url`
- `workflow_name`

### 更适合作为内部调优参数的字段

这些字段不属于终端用户的业务表达，更像系统内部 prompt/LLM 调优预算：

- `max_chars`
- `llm_max_tokens`

### 本就不该作为输入的字段

- `used_chars`

`used_chars` 是 prompt 增强工具的输出/诊断信息，不应属于 workflow 输入契约。

## 推荐分层

### A. 继续作为客户端正式输入的字段

这些字段和创作意图、媒体约束直接相关：

| 字段 | 说明 | 处理建议 |
| --- | --- | --- |
| `user_prompt` | 核心创作意图 | 保留 |
| `images` | 图生视频素材 | 保留 |
| `duration` | 时长约束 | 保留 |
| `resolution` | 分辨率约束 | 保留 |
| `aspect_ratio` | 画幅约束 | 保留 |
| `style` | 风格偏好 | 保留 |
| `negative_prompt` | 负向提示 | 保留 |
| `watermark` | 水印开关 | 保留 |
| `enable_audio` | 音频开关 | 保留 |
| `fps` | 帧率偏好 | 保留，必要时由 router/submit 再裁剪 |
| `mode` | 文生/图生/首尾帧模式 | 保留 |
| `model` | 当前阶段仍是模型选择入口 | 保留，但未来可以进一步收敛为更抽象的能力档位 |

### B. 应降级为内部/调试字段的字段

这些字段不应该作为正式客户端业务输入保留，但短期可以保留兼容：

| 字段 | 当前用途 | 推荐目标态 |
| --- | --- | --- |
| `api_provider` | 曾用于显式指定 provider；现在 router 已可根据 model 推断 | 从正式客户端输入中移除，仅保留服务端 debug override 或灰度开关 |
| `max_chars` | prompt 增强软约束 | 降级为内部调优参数，可由 workflow 默认值或服务端配置注入 |
| `llm_max_tokens` | intent / prompt LLM token 预算 | 降级为内部调优参数，不再由客户端直接传递 |

### C. 应彻底转为内部字段的字段

这些字段属于运行时基础设施，客户端不应感知：

| 字段 | 当前用途 | 推荐目标态 |
| --- | --- | --- |
| `callback_url` | request-callback provider 的 submit 回调地址 | 由 `provider_router` 输出，submit tool 消费 |
| `workflow_name` | router/debug context | 由 workflow DSL 固定或由系统注入 |
| `used_chars` | 提示词增强输出 | 仅作为 tool 输出 / creative detail / debug info |

## 逐字段建议

### 1. `api_provider`

#### 当前状态

在 `text_to_video` / `image_to_video` 中：

- `param_validate` 仍读取 `input.api_provider`
- `provider_router` 仍接收 `input.api_provider`

但后续主执行节点已经开始主要依赖：

- `provider_router.provider`
- `provider_router.model`

#### 问题

`api_provider` 继续作为客户端正式输入，会造成：

1. 客户端和 router 双重决定 provider
2. 未来如果想完全由 router 决策 provider，这个字段会持续形成绕过入口

#### 建议

目标态：

- 客户端不再传 `api_provider`
- `provider_router` 主要根据 `model + mode + workflow_name` 决策 provider
- 如需调试/灰度，保留服务端内部 override，而不是对外业务字段

#### 迁移步骤

1. 先把 `api_provider` 标记为 deprecated
2. `param_validate` 中将其改为完全可选，且不再作为正式校验项
3. 客户端停止发送该字段
4. `provider_router` 后续增加 `routing_hint` 或内部 override 机制，替代 `api_provider`

### 2. `callback_url`

#### 当前状态

视频链路中，request-callback provider 已经开始走：

- `provider_router.callback_url`

例如：

- `text_to_video`
- `image_to_video`
- `image_to_video_with_motion`
- `goods_video_pro`

#### 结论

`callback_url` 不应作为客户端输入存在。

它属于：

- provider callback registration contract
- 运行时 webhook ingress 基础设施配置

#### 建议

目标态：

- 只由 `provider_router` 输出 `callback_url`
- submit tool 统一从 router contract 中读取
- 客户端和业务 API 不再暴露 `callback_url`

### 3. `workflow_name`

#### 当前状态

`provider_router` 需要 `workflow_name` 作为：

- 路由上下文
- 调试上下文
- callback contract 生成上下文

当前 DSL 中已经基本是：

- `input.workflow_name ?? 'text_to_video'`
- `input.workflow_name ?? 'image_to_video'`

#### 结论

这不是客户端业务输入。

#### 建议

目标态：

- 由 DSL 固定写死 workflow 名称
- 或由系统运行时统一注入
- 不再保留客户端传入入口

### 4. `max_chars`

#### 当前状态

目前 `max_chars` 被用于：

- `param_validate`
- `parse_intent` / `enhance_prompt`
- prompt 生成长度预算

#### 判断

`max_chars` 更像系统内部 prompt 策略参数，不是终端用户业务意图。

用户表达的是：

- 想生成什么内容

而不是：

- 提示词增强后的最大字符预算是多少

#### 建议

短期：

- 先保留兼容输入

中期：

- 下沉为 workflow 默认值
- 或服务端配置中心注入

长期：

- 客户端不再直接控制该值
- 如需高级调试入口，放在内部 debug API，而不是正式产品输入

### 5. `llm_max_tokens`

#### 当前状态

目前用于：

- `parse_text_video_intent`
- `reconstruct_image_video_intent`
- `enhance_text_video_prompt`

#### 判断

这是典型的内部 LLM 调优预算，不是业务输入。

#### 建议

目标态：

- 从客户端输入中移除
- 由工具默认值 / workflow config / 系统配置注入

### 6. `used_chars`

#### 当前状态

它是：

- `enhance_text_video_prompt` 的输出
- creative detail / debug info 可消费字段

#### 结论

不应作为任何 workflow 输入字段出现。

#### 建议

继续保留为：

- 节点输出字段
- 内部诊断信息

不出现在客户端请求契约中。

## 视频链路推荐目标态

### Text To Video

客户端正式输入只保留：

- `user_prompt`
- `model`
- `mode`
- `duration`
- `resolution`
- `aspect_ratio`
- `style`
- `negative_prompt`
- `watermark`
- `enable_audio`
- `fps`

内部注入/内部决定：

- `provider_router.provider`
- `provider_router.callback_url`
- `provider_router.await_source`
- `provider_router.fallback_poll_tool`
- `workflow_name`
- `max_chars`
- `llm_max_tokens`

### Image To Video

客户端正式输入只保留：

- `images`
- `user_prompt`
- `model`
- `mode`
- `duration`
- `resolution`
- `aspect_ratio`
- `style`
- `negative_prompt`
- `watermark`
- `enable_audio`
- `fps`

内部注入/内部决定：

- `provider_router.provider`
- `provider_router.callback_url`
- `provider_router.await_source`
- `provider_router.fallback_poll_tool`
- `workflow_name`
- `max_chars`
- `llm_max_tokens`

## 建议迁移顺序

### 第一阶段：标记 deprecated

先将以下字段明确标记为 deprecated：

- `api_provider`
- `max_chars`
- `llm_max_tokens`

并在文档和客户端层面停止鼓励传入。

### 第二阶段：从 DSL 输入映射中弱化

优先做：

1. `param_validate` 不再依赖 `api_provider`
2. `workflow_name` 直接在 DSL 固定，不再从 `input` 读取
3. `provider_router` 改成：
   - 以 `model + mode + workflow_name` 为主
   - `api_provider` 仅作为兼容 override

### 第三阶段：内部预算参数下沉

将：

- `max_chars`
- `llm_max_tokens`

逐步迁到：

- workflow 默认值
- tool 默认值
- 服务端配置项

客户端不再显式传入。

### 第四阶段：客户端契约收口

最终客户端视频生成请求中，不再包含：

- `api_provider`
- `callback_url`
- `workflow_name`
- `max_chars`
- `llm_max_tokens`

## 结论

从视频链路来看，当前最应该收掉或降级的字段是：

### 优先移出客户端正式输入

- `api_provider`
- `callback_url`
- `workflow_name`
- `llm_max_tokens`

### 优先降级为内部调优字段

- `max_chars`

### 明确只保留为内部输出

- `used_chars`

其中最优先的是：

1. `api_provider`
2. `callback_url`
3. `workflow_name`

因为这三者直接关系到：

- provider routing 单一真相来源
- callback contract 正确归属
- workflow 基础设施边界清晰度
