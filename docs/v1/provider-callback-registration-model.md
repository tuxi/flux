# Provider 回调注册模型规范

日期：2026-04-24

状态：Draft

## 1. 背景

随着 `ai-engine` 已经引入 `await` 节点、`AwaitBinding`、统一 webhook ingress 和 fallback poll scanner，工作流层已经具备了：

1. 声明“等待外部世界唤醒”的能力
2. 用统一恢复链处理 webhook / signal / poll
3. 用 fallback poll 作为 webhook 丢失时的兜底

但当前 provider 接入还存在一个容易混淆的问题：

**并不是所有第三方 provider 都用同一种方式把回调地址注册给我们。**

有的 provider 要求：

1. 在每次提交任务时显式传 `callback_url`

也有的 provider 要求：

1. 预先在平台级事件系统中配置 HTTP 回调 URL / MQ
2. 任务完成后通过平台事件总线推送事件

如果这两类模式不区分清楚，就会出现两种误解：

1. 误以为所有 `submit` 都必须有 `callback_url`
2. 误以为只要 DSL 里把 `await.source` 写成 `webhook_or_poll`，provider 就会自动知道该回调哪里

因此，需要补一份统一的 provider 回调注册模型规范，明确：

1. provider 的回调注册方式有哪些类型
2. 每种类型在 `submit`、DSL、handler、运维配置上的责任边界
3. 哪些 provider 需要在请求内显式传 `callback_url`
4. 哪些 provider 应走平台级事件订阅模式

## 2. 设计目标

本规范目标是：

1. 统一解释 provider 回调是如何“注册到引擎”的
2. 澄清 `await` 与 `submit` 在 webhook 接入中的职责边界
3. 为后续 provider 接入提供一致的工程约束
4. 避免将不同 provider 的回调模型混为一谈

## 3. 核心原则

### 3.1 `await` 只声明等待语义，不负责回调注册

`await` 节点的职责是：

1. 声明这里会等待外部结果
2. 创建 `AwaitBinding`
3. 定义 correlation key、超时与 fallback poll

`await` **不负责**告诉第三方“应该回调哪里”。

### 3.2 `submit` 负责按 provider 规则完成回调注册

真正把“回调地址”传给 provider 或平台的责任，属于 `submit` 阶段。

也就是说：

1. `submit` 负责创建第三方任务
2. `submit` 负责按 provider 能力注册 callback
3. `await` 只负责等待和恢复

### 3.3 `source=webhook_or_poll` 不等于 provider 已知道 callback 地址

`await.source = webhook_or_poll` 只表示：

1. 这条 workflow 希望优先走 webhook
2. 如果 webhook 没到，再由 poll 补偿

它不是 provider 回调注册动作本身。

### 3.4 `submit` 与 `await` 必须拆分

第三方异步任务接入必须保持：

1. `submit` 节点负责创建任务
2. `await` 节点负责等待结果

不能把“创建任务 + 等待结果 + 重试”揉成一个节点，否则会带来：

1. 重复提交第三方任务
2. 重复计费
3. 重复外部 side effect

## 4. Provider 回调注册模型分类

本规范将 provider 回调注册方式分成三类。

### 4.1 `request_callback`

定义：

1. provider 要求在 **创建任务请求** 中显式传入 `callback_url`
2. provider 在任务完成后直接回调该 URL

接入要求：

1. `submit` tool 必须支持 `callback_url`
2. 对应 workflow DSL 必须把 `callback_url` 显式透传进 `submit`
3. 统一 webhook ingress 必须支持该 provider 的 payload normalize
4. `await` 仍使用 `webhook_or_poll`

典型特征：

1. 请求体里存在 `callback_url` / `webhook_url` / `notify_url`
2. 回调目标与这次具体任务强绑定

当前仓库示例：

1. `kling_motion_submit`

说明：

1. 这类 provider 如果 submit 不传 `callback_url`，第三方就不会回调我们
2. 因此这类 provider 漏传 `callback_url` 属于接入缺陷

### 4.2 `platform_event_subscription`

定义：

1. provider 不要求每次 submit 传 `callback_url`
2. 平台通过事件总线、消息队列或控制台配置的 HTTP 回调统一推送事件

接入要求：

1. `submit` tool 不需要 `callback_url`
2. 运维或平台层需要提前完成事件目标配置
3. 引擎侧需要支持平台事件格式的 webhook ingress / normalize
4. `await` 通常仍使用 `webhook_or_poll`

典型特征：

1. 官方文档强调通过 EventBridge / EventBus / MQ / Console 配置回调
2. submit 请求中没有 callback 字段
3. 回调事件通常是统一包裹格式，而不是直接返回最终业务结果

当前明确属于此类的 provider：

1. 阿里云百炼异步任务

说明：

1. 对这类 provider 而言，`submit` 中没有 `callback_url` 并不构成问题
2. 真正的缺口通常在于：平台事件格式还没有接进统一 webhook ingress

### 4.3 `poll_only`

定义：

1. provider 不提供可用 webhook
2. 只能通过任务 ID 主动查询状态

接入要求：

1. `await` 使用 `source = poll` 或 `source = webhook_or_poll`
2. `AwaitBinding + poll scanner` 作为主恢复路径
3. 不推荐在 wait tool 内部做长轮询
4. 优先使用单次查询型 poll tool

说明：

1. 即使 provider 只支持 poll，业务 DSL 仍然更适合 `submit + await`
2. `await` 的语义依然成立，只是 completion source 不同

## 5. 当前 provider 归类建议

基于当前文档与现有代码，建议先按以下方式归类：

| Provider | 回调注册模型 | 当前结论 |
| --- | --- | --- |
| `kling` | `request_callback` | submit 必须显式支持并透传 `callback_url` |
| `aliyun` | `platform_event_subscription` | 不要求 submit 传 `callback_url`，需要补平台事件入口适配 |
| `volcengine`（现有图片/视频链路） | 待确认 | 当前先按 `webhook_or_poll + fallback poll` 兼容；若 provider 文档要求 request callback，则后续应按 `request_callback` 收口 |
| `openai` / 其他占位 provider | 待实现 | 未来按文档能力归入上述三类之一 |

说明：

1. `volcengine` 当前仓库中更多是以 poll 兼容为主
2. 是否需要显式 `callback_url`，应以 provider 官方接口文档为准

## 6. DSL 与 Submit 设计约束

### 6.1 DSL 层约束

对于 `request_callback` 型 provider：

1. workflow DSL 必须在 `submit` 节点显式透传 `callback_url`

对于 `platform_event_subscription` 型 provider：

1. workflow DSL 不必新增 `callback_url`
2. 但必须保证 `await` 节点的 correlation key 能命中平台事件里的任务标识

### 6.2 Submit tool 约束

`submit` tool 建议统一支持以下可选能力：

1. `callback_url`
2. `external_task_id`
3. `callback_registration_mode`

其中：

1. `callback_url` 只在 `request_callback` 模型下使用
2. `external_task_id` 用于业务系统侧做幂等或回放关联
3. `callback_registration_mode` 主要用于可观测性与调试输出，不要求一定对外暴露

### 6.3 Await 约束

`await` 节点应始终只表达：

1. `await_type`
2. `source`
3. `provider`
4. `correlation`
5. `fallback_poll`

不应把 provider 平台级 webhook 配置细节塞进业务 DSL。

## 7. 当前代码现状结论

### 7.1 已正确实现的部分

1. `kling_motion_submit` 已支持 `callback_url`
2. `motion_control` workflow 已显式透传 `input.callback_url`
3. `await` runtime 与 `AwaitBinding` 已能支撑 webhook_or_poll 模式

### 7.2 当前仍缺的部分

1. 阿里云百炼虽然已经迁到 `await` 主模型，但平台事件格式尚未完整接入统一 webhook ingress
2. 需要新增阿里 EventBridge 事件入口适配
3. 需要系统检查其他 `request_callback` 型 provider 的 submit tool 是否漏传 `callback_url`

## 8. 开发规范

从现在开始，新增或重构 provider 接入时必须先回答一个问题：

**这个 provider 的回调注册模型属于哪一类？**

推荐流程：

1. 阅读 provider 官方文档
2. 判定是 `request_callback`、`platform_event_subscription` 还是 `poll_only`
3. 再决定 `submit` 是否需要 `callback_url`
4. 再决定 webhook ingress 需要吃什么事件格式
5. 再决定 `await.source` 应配置为 `webhook_or_poll` 还是 `poll`

## 9. 实施建议

建议接下来的工程顺序为：

1. 先补阿里云百炼 EventBridge webhook 入口设计
2. 再系统梳理现有 provider submit tool 是否存在 `callback_url` 漏传
3. 最后将该规范摘要回填进 Await Runtime 主文档体系

## 10. 参考文档

1. Kling 文生视频接口文档：[https://klingai.com/document-api/apiReference/model/textToVideo](https://klingai.com/document-api/apiReference/model/textToVideo)
2. 阿里云百炼异步任务事件通知文档：[https://help.aliyun.com/zh/model-studio/async-task-api](https://help.aliyun.com/zh/model-studio/async-task-api)
3. 阿里云百炼异步调用 API 参考：[https://help.aliyun.com/zh/model-studio/asynchronous-call-api-reference](https://help.aliyun.com/zh/model-studio/asynchronous-call-api-reference)
