# 阿里云百炼 EventBridge Webhook 接入设计

日期：2026-04-24

状态：Draft

关联文档：

- [Provider 回调注册模型规范](./provider-callback-registration-model.md)
- [Await Runtime V1 PRD](./engine-await-runtime-v1-prd.md)

## 1. 背景

当前 `ai-engine` 已经完成：

1. `await` 节点
2. `AwaitBinding`
3. 统一 webhook ingress
4. `CompleteAwaitNode -> ResumeTask`
5. fallback poll scanner

同时，图片链路中：

1. `aliyun_wait` 已迁到 `await`
2. fallback poll 已具备

但阿里云百炼异步任务的官方回调模型并不是：

1. 每次请求显式传 `callback_url`

而是：

1. 任务完成后上报到 EventBridge
2. 由事件总线将事件推送到配置好的 HTTP 回调 URL 或 MQ

这意味着当前阿里 `await` 的 webhook 主路径还没有完全落地。

当前已有能力更准确地说是：

1. `await + fallback poll` 已成立
2. `await + aliyun EventBridge webhook` 还缺统一入口适配

## 2. 目标

本设计目标：

1. 为阿里云百炼异步任务补上 EventBridge 事件入口
2. 将阿里平台事件格式映射到现有 `AwaitBinding` 与恢复链
3. 保持 `await` 主模型不变
4. 不要求在 `AliyunImageGenerateSubmitTool` 中新增 `callback_url`

## 3. 非目标

本期不做：

1. 不重构阿里 submit API
2. 不在 V1 中一次性支持所有阿里异步能力
3. 不替代 fallback poll scanner
4. 不要求在业务 DSL 中新增 EventBridge 细节配置

## 4. 官方事件模型理解

根据阿里云文档，百炼异步任务完成后会通过 EventBridge 推送事件。

事件关键字段包括：

1. `type = dashscope:System:AsyncTaskFinish`
2. `source = acs.dashscope`
3. `data.task_id`
4. `data.task_status`

其中事件只表示：

1. 哪个任务完成了
2. 当前任务状态是什么

它通常**不直接包含完整的最终结果 URL**。

所以阿里 EventBridge 的正确处理方式应是：

1. 先接收事件
2. 从事件中拿到 `task_id`
3. 再调用一次查询结果接口获取最终图像/视频地址
4. 然后再完成 `await`

## 5. 总体方案

阿里 EventBridge 接入采用“两段式恢复”：

### 5.1 第一段：事件命中 Binding

1. EventBridge POST 事件到我们的 HTTP 入口
2. handler 解析事件
3. 提取 `data.task_id`
4. 按 `provider=aliyun + provider_task_id/task_id` 命中 `AwaitBinding`

### 5.2 第二段：补查结果并完成 Await

1. 若事件状态是终态成功
2. 使用 provider task id 调一次阿里查询接口
3. 取到图像结果
4. 映射成统一 payload
5. 调 `CompleteAwaitNode`
6. 再进入 `ResumeTask`

所以阿里模式不是：

`event payload 直接完成 await`

而是：

`event payload -> 命中 binding -> query result -> complete await`

## 6. 入口设计

建议增加专门入口：

- `POST /api/v1/webhooks/ai/await/aliyun/eventbridge`

理由：

1. 便于和“provider 原始 webhook”区分
2. 事件体结构与已有 provider webhook 风格差异较大
3. 后续可对 EventBridge 做专门验签/过滤/调试

也可以在内部仍然复用统一 handler/service，只是在 HTTP 层分一个 adapter。

## 7. 事件结构建议

建议先定义最小解析结构：

```go
type AliyunEventBridgeEvent struct {
	Source string `json:"source"`
	Type   string `json:"type"`
	Data   struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		RequestID  string `json:"request_id"`
		Region     string `json:"region"`
	} `json:"data"`
}
```

我们当前真正需要的最小字段只有：

1. `type`
2. `source`
3. `data.task_id`
4. `data.task_status`

## 8. 状态映射

阿里 EventBridge 事件状态建议映射为：

| 阿里状态 | 语义 |
| --- | --- |
| `SUCCEEDED` | 成功终态 |
| `FAILED` | 失败终态 |
| `CANCELED` | 失败终态 |
| `UNKNOWN` | 失败终态或忽略，视业务决定 |
| `PENDING` | 非终态 |
| `RUNNING` | 非终态 |

处理规则：

1. 非终态事件不恢复 task，只记录并返回 `ignored_non_terminal`
2. 终态失败事件可直接按错误完成 `await`
3. 终态成功事件先查结果，再完成 `await`

## 9. 结果补查设计

EventBridge 成功事件到来后：

1. 先命中 `AwaitBinding`
2. 再根据 binding 选择合适的 query tool / query service

建议优先复用标准 `poll_once` tool：

1. `aliyun_image_generate_poll_once`

这样可以避免重复造一套查询逻辑，也能让 EventBridge、scanner、replay 共用同一套“单次补查”语义。

兼容期仍允许使用 legacy wait tool 的单次查询模式，但它不再是推荐终态。

## 10. 与现有 Await Runtime 的集成

建议复用已有链路：

1. EventBridge handler 解析事件
2. 命中 `AwaitBinding`
3. 若成功终态，则调用单次 query
4. 将 query 结果映射成 `aliyun` 标准 payload
5. 继续走：
   - `CompleteAwaitNode`
   - `ResumeTask`

这意味着：

1. 不新增旁路状态修改
2. 不绕开现有状态机
3. 不绕开 `AwaitBinding`

## 11. 统一 Payload 目标

无论事件来自 EventBridge 还是本地 replay，阿里最终都建议归一化成：

```json
{
  "image_url": "https://...",
  "width": 1024,
  "height": 1024,
  "provider_task_id": "task_id",
  "api_provider": "aliyun",
  "model": "qwen-image-plus"
}
```

这样下游：

1. `provider_result_merge`
2. `image_download`
3. `image_postprocess`

都不需要区分“这是 EventBridge 触发的结果”。

## 12. 推荐实现分层

建议新增：

### 12.1 HTTP 层

`AliyunEventBridgeHandler`

职责：

1. 接收 EventBridge POST
2. 校验最小事件格式
3. 调用 service 层

### 12.2 Service 层

`AliyunEventBridgeService`

职责：

1. 解析事件状态
2. 命中 binding
3. 成功终态时补查结果
4. 调 `CompleteAwaitNode`

### 12.3 Query 层

先复用：

1. `aliyun_image_generate_poll_once`

后续可演进为：

1. `aliyun_image_generate_poll_once`

## 13. 可观测性建议

建议增加结构化 task event：

1. `aliyun_eventbridge_received`
2. `aliyun_eventbridge_ignored_non_terminal`
3. `aliyun_eventbridge_binding_not_found`
4. `aliyun_eventbridge_query_result_started`
5. `aliyun_eventbridge_query_result_completed`
6. `aliyun_eventbridge_query_result_failed`

这些事件应至少携带：

1. `binding_id`
2. `provider_task_id`
3. `task_status`
4. `event_type`
5. `request_id`
6. `source=eventbridge`

## 14. 安全与运维要求

阿里 EventBridge 不是 provider 原始 webhook，而是平台事件推送，因此建议：

1. 单独 route
2. 加来源白名单 / 签名校验（若平台支持）
3. 仅开放给公网入口或指定网关
4. 保留原始事件日志便于排障

## 15. 与 DSL 的关系

此设计不要求修改业务 DSL。

原因：

1. `await` 已经正确表达了等待语义
2. 阿里的回调注册不发生在 request body，而发生在平台侧
3. EventBridge 接入属于 provider integration/runtime 层能力

所以业务 DSL 不需要新增 `callback_url`。

## 16. 当前结论

对阿里百炼来说：

1. `submit` 里没有 `callback_url` 不构成设计缺陷
2. 真正缺的是 EventBridge 事件入口适配
3. 当前 `aliyun_wait(await)` 已具备 fallback poll，但 webhook 主路径尚未完整落地

## 17. 下一步建议

建议按以下顺序实现：

1. 新增 `POST /api/v1/webhooks/ai/await/aliyun/eventbridge`
2. 实现 EventBridge 事件解析与 binding 命中
3. 成功事件复用单次 query 获取结果
4. 统一走 `CompleteAwaitNode`
5. 为该链路补 handler/service 测试

## 18. 参考文档

1. 阿里云百炼异步任务事件通知文档：[https://help.aliyun.com/zh/model-studio/async-task-api](https://help.aliyun.com/zh/model-studio/async-task-api)
2. 阿里云百炼异步调用 API 参考：[https://help.aliyun.com/zh/model-studio/asynchronous-call-api-reference](https://help.aliyun.com/zh/model-studio/asynchronous-call-api-reference)
