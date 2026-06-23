# Provider Router Callback URL Design

## 1. 背景

当前引擎已经引入 `await` 节点、`AwaitBinding`、统一 webhook ingress 和 fallback poll。
对于 `request_callback` 型 provider，`submit` 节点除了提交任务本身，还必须在请求中显式注册 `callback_url`，否则第三方不会主动回调我方服务。

现状里存在两个问题：

1. `callback_url` 的来源不统一  
   `image_to_video_with_motion` 仍然通过 `input.callback_url` 透传，这意味着 callback 地址来自客户端输入，而不是服务端运行时。

2. video 主链未完成 callback 注册  
   `video_generate_submit` 当前未把 `CreateContentGenerationTaskRequest.CallbackUrl` 真正写入请求中。

这两个问题本质上说明：`callback_url` 还没有成为 provider routing contract 的一部分。

## 2. 设计目标

本设计的目标是把 `callback_url` 纳入 `provider_router` 的标准输出，由 router 决定：

1. 当前 provider 是否需要显式 callback 注册
2. callback 应该使用哪条 webhook ingress
3. `submit` 节点应该拿哪个 callback 地址去请求第三方

这样可以确保：

1. 客户端不再直接控制 callback 回调地址
2. request-callback / eventbridge / poll-only 三类 provider 统一收敛到 router contract
3. workflow DSL 不再依赖 `input.callback_url` 这类过渡字段
4. `submit` 与 `await` 在 provider 语义上对齐

## 3. 设计原则

### 3.1 callback_url 不是业务输入

`callback_url` 是平台回调基础设施配置，不是业务参数。
用户可以影响 provider 选择，但不应直接决定第三方回调打到哪个内部地址。

### 3.2 callback_url 是 provider routing contract 的一部分

`provider_router` 不只决定 `provider` / `model`，还要决定 provider 的执行契约，包括：

1. `submit` 用哪个工具
2. `await` 预期什么唤醒来源
3. 是否需要 request callback
4. 若需要，则应该回调到哪个 URL

### 3.3 callback_url 的环境基础配置仍由服务端掌控

`provider_router` 负责输出最终 callback 地址，但 callback 地址的基准域名、路径模板、环境差异，仍应由服务端配置控制。

换句话说：

1. 服务端提供 callback base config
2. `provider_router` 基于 provider 类型和 webhook 模型输出最终 `callback_url`

## 4. Provider Router 标准输出字段

建议把 `provider_router` 的标准输出扩展成下面这组字段。

### 4.1 基础路由字段

1. `provider`
2. `case`
3. `model`
4. `mode`
5. `submit_tool`
6. `fallback_poll_tool`

### 4.2 await / callback 相关字段

1. `await_source`
   可选值：
   - `webhook_or_poll`
   - `eventbridge_or_poll`
   - `poll_only`
   - `signal`

2. `callback_registration_mode`
   可选值：
   - `request_callback`
   - `platform_event_subscription`
   - `none`

3. `callback_url`
   仅在 `request_callback` 模型下有值。

4. `webhook_provider_path`
   provider 对应的统一 ingress 路径标识，例如：
   - `kling`
   - `volcengine`
   - `doubao`
   - `aliyun/eventbridge`

5. `callback_required`
   布尔值。用于 submit 层快速判断当前 provider 是否必须显式传 callback。

### 4.3 执行提示字段

1. `submit_hint`
2. `normalized_params`
3. `routing_reason`
4. `applied_features`
5. `dropped_features`

## 5. callback_url 生成规则

### 5.1 统一规则

对 `request_callback` 型 provider，callback URL 应由服务端运行时按以下规则生成：

1. 先确定部署环境的外部可访问基准地址
   - 例如 `https://ai.example.com`
   - 本地开发态可由 dev replay / 本地配置替代

2. 根据 provider 的 webhook ingress 模型拼接路径
   - `kling` -> `/api/v1/webhooks/ai/await/kling`
   - `doubao` -> `/api/v1/webhooks/ai/await/doubao`
   - `aliyun/eventbridge` -> `/api/v1/webhooks/ai/await/aliyun/eventbridge`
   - 其他 request-callback provider 按统一 provider 路径扩展

3. 如 provider 有额外要求，可附加查询参数或签名 token
   - 但优先建议通过 header / body 做验证

4. 由 `provider_router` 输出最终 `callback_url`

### 5.2 platform_event_subscription 型 provider

例如 `aliyun`。

这类 provider 不要求在每次 submit 请求中传 `callback_url`，而是通过平台级事件订阅完成通知。

因此 router 输出应为：

1. `callback_registration_mode = platform_event_subscription`
2. `callback_required = false`
3. `callback_url = ""`
4. `await_source = eventbridge_or_poll`
5. `webhook_provider_path = aliyun/eventbridge`

### 5.3 poll_only 型 provider

对没有 webhook 能力的 provider：

1. `callback_registration_mode = none`
2. `callback_required = false`
3. `callback_url = ""`
4. `await_source = poll_only`

## 6. DSL 使用方式

目标态里，`submit` 节点不再引用 `input.callback_url`，而是统一引用 `provider_router.callback_url`。

### 6.1 推荐写法

```go
"callback_url": "provider_router.callback_url"
```

### 6.2 request-callback provider

例如 `kling` / `doubao`：

1. `submit` 必须透传 `provider_router.callback_url`
2. `await` 节点继续消费 `provider_router.provider`
3. `await.source` 应与 router 输出保持一致

### 6.3 platform_event_subscription provider

例如 `aliyun`：

1. DSL 中不需要给 submit 节点传 `callback_url`
2. router 继续输出 `await_source=eventbridge_or_poll`
3. 运行时通过 EventBridge 入口完成恢复

## 7. 现有 input.callback_url 的迁移方案

### 7.1 当前问题

目前 `image_to_video_with_motion` 中存在：

1. `kling_motion_submit.callback_url = input.callback_url`
2. `volcengine_submit.callback_url = input.callback_url`

这说明 callback 地址来自客户端输入，属于过渡实现。

### 7.2 迁移目标

改为：

1. `provider_router` 输出 `callback_url`
2. `submit` 节点引用 `provider_router.callback_url`
3. workflow 输入模型中移除 `input.callback_url`

### 7.3 迁移步骤

#### 阶段 1：router 扩展

1. 给 `video_provider_router` 增加输出：
   - `callback_registration_mode`
   - `callback_required`
   - `callback_url`
   - `await_source`
   - `fallback_poll_tool`

2. callback 的实际生成依赖服务端配置，例如：
   - `webhook.public_base_url`

#### 阶段 2：workflow DSL 切换

把当前所有 `input.callback_url` 改成：

```go
"callback_url": "provider_router.callback_url"
```

#### 阶段 3：删除客户端输入依赖

1. 从 workflow 输入契约中移除 `callback_url`
2. 从客户端调用层删除对应字段
3. 让 callback 完全由服务端和 router 决定

## 8. 哪些 Workflow 需要先改

按优先级建议如下。

### P0：立刻要改

#### 8.1 `image_to_video_with_motion`

文件：

1. `ai-engine/workflows/motion_control/image_to_video_with_motion_dsl.go`
2. `ai-engine/workflows/motion_control/video_provider_router.go`
3. `ai-engine/workflows/motion_control/kling_motion_submit.go`

原因：

1. 当前已经有 `provider_router`
2. 当前仍然直接使用 `input.callback_url`
3. 这是最直接的错误来源，迁移成本最低

#### 8.2 video 主链

文件：

1. `ai-engine/workflows/videos/text_to_video_workflow_dsl.go`
2. `ai-engine/workflows/videos/image_to_video_workflow_dsl.go`
3. `ai-engine/workflows/videos/video_generate_api.go`

原因：

1. `video_generate_submit` 当前确实缺 `CallbackUrl`
2. 该 provider 模型与 `kling` 一样属于 `request_callback`
3. 当前 video workflow 还没有 provider_router 主线，需要优先设计接入方式

### P1：随后统一

#### 8.3 `goods_video_pro`

文件：

1. `ai-engine/workflows/goods/goods_video_pro_dsl.go`
2. `ai-engine/workflows/motion_control/video_provider_router.go`

原因：

1. 已有 `provider_router`
2. 后续如果 goods 链也接 request-callback provider，应直接消费 router 输出的 callback contract

### P2：无需增加 callback_url 的 workflow

#### 8.4 image 系列 aliyun workflow

文件：

1. `ai-engine/workflows/images/text_to_image_workflow_dsl.go`
2. `ai-engine/workflows/images/image_to_image_workflow_dsl.go`
3. `ai-engine/workflows/images/style_transfer_workflow_dsl.go`

原因：

1. 这些链路中的 `aliyun` 当前走 `platform_event_subscription`
2. 不需要 request callback URL
3. 需要的是 router 输出更明确的 `await_source`

## 9. 推荐的阶段性落地策略

### 9.1 第一阶段

先不要求所有 workflow 一次性补齐统一 router。

先做：

1. 扩展 `video_provider_router`
2. 让 `image_to_video_with_motion` 从 `input.callback_url` 切到 `provider_router.callback_url`
3. 给 `video_generate_submit` 增加 `callback_url`

### 9.2 第二阶段

把 video 主链补成真正的 router 模式：

1. `text_to_video`
2. `image_to_video`

都引入 `provider_router`，不再直接消费 `input.api_provider`

### 9.3 第三阶段

统一 provider contract：

1. `provider`
2. `model`
3. `await_source`
4. `callback_registration_mode`
5. `callback_url`
6. `fallback_poll_tool`

作为跨 workflow 的统一输出规范

## 10. 结论

`callback_url` 的正确归属不是客户端输入，也不是 submit tool 的局部私有字段，而是 `provider_router` 输出的 provider execution contract。

因此目标态应为：

1. 客户端不再传 `callback_url`
2. `provider_router` 决定是否需要 callback
3. `provider_router` 输出 `callback_url`
4. `submit` 节点统一透传 `provider_router.callback_url`
5. `await` 继续消费同一个 router 输出中的 provider / await_source / fallback_poll_tool

这样可以把 request-callback、eventbridge 和 poll-only 三类 provider 全部纳入统一模型，并消除 `input.callback_url` 这种错误来源。
