# AI Engine TTS Provider 技术设计文档

日期：2026-04-24

状态：Draft

关联文档：

- [AI Engine TTS Provider 分层与付费化需求文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-strategy-requirements.md)
- [AI Engine TTS Provider 实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-task-breakdown.md)

## 1. 设计目标

本设计文档用于定义 AI Engine 中 TTS provider 化改造的技术方案，目标是把当前基于 `edge-tts` 的单实现，升级为可扩展、可灰度、可回退、可观测、可统计成本的统一 TTS 基础设施。

本文档已按 `main` 分支最新异步基建更新，默认前提为：

- 引擎已具备 `await runtime`
- 新增 provider 接入应优先考虑 `callback / replay / poll_once` 统一模型
- 当前 TTS 仍需兼容商品视频同步链路，因此本设计采用“两阶段”方案：
  - 第一阶段：同步包装异步 provider，先接入真实付费 TTS
  - 第二阶段：正式对齐 `submit + await + poll_once`

本次设计的核心目标：

1. 支持 `draft -> edge`、`publish -> paid` 的策略分流
2. 统一整段 TTS 和分段 TTS 的 provider 调用模式
3. 将 provider 路由、回退、缓存、日志、成本统计从工作流节点中剥离
4. 保证商品视频工作流在改造后仍可稳定生成成片
5. 保证付费 TTS 的协议设计兼容 `sync / async / sse / websocket`
6. 避免长期把异步 provider 能力隐藏在同步工具内部

## 2. 非目标

本设计当前不覆盖：

- 一次性接入多个付费 provider 的复杂动态调度
- 面向用户的实时流式 TTS 播放链路
- 用户侧 TTS 单独计费页面和产品化展示
- 所有历史业务工作流的同步迁移
- 在本期内一次性把所有 TTS 调用全部 await 化

## 3. 当前问题总结

当前实现的主要问题：

1. `tts_speech_generate` 与 `tts_generate_segments` 直接执行 `edge-tts` CLI
2. 整段与分段调用逻辑重复，缺少统一能力层
3. 无法按业务模式切换 provider
4. 缺少 provider 维度日志、指标和成本统计
5. 缓存 key 未纳入 TTS 维度
6. 回退逻辑散落在工作流节点中，不利于统一治理

## 4. 总体架构

建议将 TTS 能力拆成五层：

1. `provider layer`
   负责与具体供应商交互，如 `edge`、`volc`、`aliyun`
2. `strategy layer`
   负责根据业务模式选择 provider 和回退顺序
3. `service layer`
   负责统一整段/分段调用、重试、日志、成本估算、缓存读写
4. `workflow adapter layer`
   负责把工作流输入映射到 TTS service
5. `trace/billing layer`
   负责沉淀 TTS 用量、trace、指标和后续成本联动

在此之上，增加一个跨层约束：

- `async integration contract`
  负责约束 paid provider 如何接入 `await runtime / callback / poll_once`

推荐目录：

```text
ai-engine/pkg/tts/
  types.go
  provider.go
  strategy.go
  service.go
  cache.go
  metrics.go
  errors.go
  providers/
    edge_provider.go
    volc_provider.go
    aliyun_provider.go
```

## 5. 核心模块设计

## 5.1 类型定义

建议先定义统一类型，避免 provider 各自返回不同结构。

### `TTSMode`

```go
type TTSMode string

const (
    TTSModeDraft   TTSMode = "draft"
    TTSModePublish TTSMode = "publish"
)
```

### `TTSProviderName`

```go
type TTSProviderName string

const (
    TTSProviderEdge         TTSProviderName = "edge"
    TTSProviderPaidPrimary  TTSProviderName = "paid_primary"
    TTSProviderPaidSecondary TTSProviderName = "paid_secondary"
)
```

### `SynthesizeRequest`

建议统一请求结构：

- `Text`
- `Voice`
- `Rate`
- `Volume`
- `Pitch`
- `Format`
- `Mode`
- `Scene`
- `TaskID`
- `SegmentKey`
- `Metadata`
- `PreferredProtocols`

### `SynthesizeResult`

建议统一输出结构：

- `AudioLocalPath`
- `DurationSec`
- `Provider`
- `Voice`
- `Chars`
- `LatencyMs`
- `EstimatedCost`
- `CacheHit`
- `Degraded`
- `Warnings`
- `ProviderRequestID`
- `Protocol`
- `SubmissionID`
- `SubmissionStatus`

### `SegmentSynthesizeResult`

用于分段结果：

- `SegmentIndex`
- `ShotIndex`
- `Text`
- `AudioLocalPath`
- `DurationSec`
- `Chars`
- `Success`
- `ErrorCode`
- `ErrorMessage`
- `Provider`

### `SubmitSynthesizeRequest`

用于 paid provider 的异步提交。

核心字段建议包括：

- `Request`
- `CallbackURL`
- `AwaitSource`
- `FallbackPollTool`

### `SubmitSynthesizeResult`

建议字段：

- `Provider`
- `Protocol`
- `SubmissionID`
- `Status`
- `ProviderRequestID`
- `AcceptedAt`

### `WaitSynthesizeRequest`

用于同步兼容层或 await fallback poll 补查。

建议字段：

- `Provider`
- `SubmissionID`
- `Protocol`
- `TaskID`
- `Metadata`

### `StreamEvent`

用于 SSE / WebSocket 这类流式 provider。

建议字段：

- `Type`
- `Provider`
- `Protocol`
- `ProviderRequestID`
- `AudioChunk`
- `Progress`
- `ErrorCode`
- `ErrorMessage`

## 5.2 Provider 接口

建议定义统一 provider 接口：

```go
type Provider interface {
    Name() TTSProviderName
    SupportsSegments() bool
    SupportsTimestamps() bool
    Synthesize(ctx context.Context, req SynthesizeRequest) (*SynthesizeResult, error)
}
```

说明：

- 分段合成不强制 provider 暴露单独接口，可由 service 层循环调用 `Synthesize`
- 若未来某 provider 提供原生批量分段接口，可通过可选扩展接口支持
- `Synthesize` 保留为当前工作流兼容入口，但不应成为 paid provider 的唯一契约

扩展接口可选：

```go
type SegmentProvider interface {
    SynthesizeSegments(ctx context.Context, req SegmentSynthesizeRequest) (*SegmentSynthesizeBatchResult, error)
}
```

```go
type AsyncProvider interface {
    SubmitSynthesize(ctx context.Context, req SubmitSynthesizeRequest) (*SubmitSynthesizeResult, error)
    WaitSynthesize(ctx context.Context, req WaitSynthesizeRequest) (*SynthesizeResult, error)
}
```

```go
type StreamProvider interface {
    OpenSynthesisStream(ctx context.Context, req OpenStreamRequest) (<-chan StreamEvent, error)
}
```

接入原则：

1. `edge` 这类本地或天然同步 provider，只实现 `Synthesize` 即可
2. 火山这类推荐异步 / SSE / WebSocket 的 provider，应优先实现 `AsyncProvider` 或 `StreamProvider`
3. `Synthesize` 可以由 service/provider 内部包装异步能力实现，但这只是兼容层，不是长期主路径

## 5.3 Strategy Resolver

### 目标

根据任务模式、配置、灰度、provider 健康状态，决定本次请求应走哪个 provider，以及失败后的回退顺序。

### 输入

- `mode`
- `workflow_name`
- `task_id`
- `user_id`
- `is_publish`
- provider 健康信息
- 配置开关

### 输出

```go
type StrategyDecision struct {
    PrimaryProvider   TTSProviderName
    FallbackProviders []TTSProviderName
    RetryTimes        int
    AllowEdgeFallback bool
    StrategyVersion   string
}
```

### 默认规则

`draft`

- `primary = edge`
- `fallback = []`
- `retry = 1`

`publish`

- `primary = paid_primary`
- `fallback = [paid_secondary, edge]`
- `retry = 1 or 2`
- `allow_edge_fallback = true`
- `preferred_protocols = [async, sse, websocket, sync]`

## 5.4 TTS Service

`TTSService` 是工作流实际调用的统一入口。

建议接口：

```go
type Service interface {
    Synthesize(ctx context.Context, req ServiceSynthesizeRequest) (*ServiceSynthesizeResult, error)
    SynthesizeSegments(ctx context.Context, req ServiceSegmentRequest) (*ServiceSegmentResult, error)
    SubmitSynthesize(ctx context.Context, req SubmitSynthesizeRequest) (*SubmitSynthesizeResult, error)
    WaitSynthesize(ctx context.Context, req WaitSynthesizeRequest) (*SynthesizeResult, error)
    OpenSynthesisStream(ctx context.Context, req OpenStreamRequest) (<-chan StreamEvent, error)
}
```

职责：

1. 读取策略决策
2. 读取或写入 TTS 缓存
3. 调用 provider
4. 处理重试和 fallback
5. 记录日志、指标、trace 字段
6. 计算字符数和预估成本
7. 在当前同步工作流中，为异步 provider 提供有限度的同步兼容包装

非职责：

- 不负责商品视频的字幕对齐和音频拼接
- 不负责视频缓存保存
- 不负责最终用户计费

### 两阶段职责说明

#### 第一阶段：同步兼容层

- `Synthesize` 内部允许识别 provider 是否实现 `AsyncProvider`
- 若已实现，则内部执行 `SubmitSynthesize + WaitSynthesize`
- 若实现 `StreamProvider`，则内部可以消费 SSE / WebSocket，直到完整音频落盘后再返回

适用场景：

- 当前商品视频工作流
- 需要立即拿到完整音频文件和时长的工具

#### 第二阶段：正式异步层

- 工作流直接调用 `SubmitSynthesize`
- `await` 节点负责等待回调 / signal / fallback poll
- `poll_once` tool 负责标准化单次状态补查

适用场景：

- `publish` 场景的正式 paid provider 主路径
- 长耗时、高并发、需要 replay 的异步任务

## 5.5 Workflow Adapter

当前工作流中有两个直接 TTS 节点：

1. `tts_speech_generate`
2. `tts_generate_segments`

改造后建议：

- 这两个工具保留对外名字不变，避免 DSL 大面积变更
- 但内部实现改为调用 `TTSService`
- 工作流节点不直接处理 provider 逻辑

### `tts_speech_generate`

职责变为：

- 解析整段文本请求
- 调用 `TTSService.Synthesize`
- 保持现有输出字段兼容

### `tts_generate_segments`

职责变为：

- 从 `voiceover_plan` 提取分段文本
- 构造 `ServiceSegmentRequest`
- 调用 `TTSService.SynthesizeSegments`
- 将结果映射回 `voiceover_plan`

### 后续异步化演进

第一阶段不要求改 DSL 名称，但第二阶段建议新增：

- `tts_submit`
- `tts_poll_once`

并评估是否在 `publish` 付费 TTS 场景下引入独立 `await` 节点。

## 6. 配置设计

建议在配置中增加 TTS 专属配置：

```yaml
tts:
  enabled: true
  strategy_version: "v1"
  publish_paid_tts_enabled: true
  draft_default_provider: "edge"
  publish_default_provider: "paid_primary"
  allow_edge_fallback: true
  default_timeout_ms: 15000
  segment_timeout_ms: 10000
  retries: 1
  cache_ttl_seconds: 604800
  gray_ratio: 1.0

  edge:
    command: "edge-tts"

  paid_primary:
    provider: "volc"
    endpoint: ""
    api_key: ""
    app_id: ""
    transport: "async"
    callback_enabled: false
    callback_base_url: ""
    websocket_url: ""
    sse_url: ""
    submit_timeout_ms: 10000
    wait_timeout_ms: 60000

  paid_secondary:
    provider: "aliyun"
    endpoint: ""
    access_key_id: ""
    access_key_secret: ""
```

### 配置原则

1. provider 认证配置和策略配置分离
2. 所有 provider 都应允许独立开关
3. 应能快速全局关闭付费 provider
4. 协议偏好与认证配置分离
5. callback / poll_once 相关配置应对齐现有 await runtime 约定

## 7. 缓存设计

## 7.1 通用 TTS 缓存

建议增加独立 TTS 缓存层，不依赖商品视频缓存。

建议缓存 key：

```text
tts:{provider}:{voice}:{format}:{rate}:{volume}:{pitch}:{text_hash}:{strategy_version}
```

缓存 value 建议包括：

- `provider`
- `voice`
- `format`
- `text_hash`
- `audio_object_key`
- `duration_sec`
- `chars`
- `created_at`

缓存存储策略可选：

1. Redis 记录 metadata + OSS 存实际音频
2. Redis 记录 metadata + 本地磁盘临时复原

推荐：

- 生产环境优先使用 Redis + OSS
- 本地开发允许使用本地磁盘

## 7.2 商品视频缓存 key 升级

当前 `BuildGoodsVideoCacheKeyV2` 需要新增字段：

- `tts_provider`
- `tts_voice`
- `tts_strategy_version`

否则会出现：

- 换音色不换缓存
- 换 provider 不换缓存
- 策略升级后旧结果继续命中

## 8. 回退与重试设计

## 8.1 整段 TTS

`draft`

1. `edge`
2. `edge retry`
3. 返回失败

`publish`

1. `paid_primary`
2. `paid_primary retry`
3. `paid_secondary`
4. `edge`
5. 结果标记 `degraded`

若 `paid_primary` 为异步 provider，第一阶段回退行为建议为：

1. `submit`
2. `wait`
3. 超时或失败后，同 provider retry
4. 再切 fallback provider
5. 最终回退 `edge`

第二阶段则建议拆分为：

1. `tts_submit`
2. `await`
3. `tts_poll_once`
4. fallback provider 或 `edge`

## 8.2 分段 TTS

建议将分段状态分为：

- `success`
- `synthesize_failed`
- `duration_probe_failed`
- `missing_output`

分段处理策略：

1. 单段失败先重试本 provider
2. 单段仍失败则切 fallback provider
3. 若仍失败则记 warning
4. 若整体不完整，由 `assemble_voiceover_audio` 决定是否回退整段音频

## 8.3 错误分层

建议错误至少分三类：

1. `provider_error`
2. `transport_error`
3. `local_runtime_error`

这样可以区分：

- 供应商接口失败
- 网络或鉴权失败
- 本地文件不存在、ffprobe 失败等问题

## 9. 日志、指标与 Trace 设计

## 9.1 结构化日志

每次 TTS 请求建议输出：

- `task_id`
- `workflow_name`
- `mode`
- `provider`
- `voice`
- `chars`
- `segments`
- `latency_ms`
- `cache_hit`
- `success`
- `degraded`
- `fallback_chain`
- `error_code`
- `error_message`
- `protocol`
- `submission_id`
- `submission_status`
- `provider_request_id`

## 9.2 指标

建议指标：

- `tts_requests_total`
- `tts_request_success_total`
- `tts_request_failure_total`
- `tts_chars_total`
- `tts_estimated_cost_total`
- `tts_request_latency_ms`
- `tts_fallback_total`
- `tts_degraded_total`
- `tts_submit_total`
- `tts_submit_failure_total`
- `tts_wait_total`
- `tts_poll_once_total`
- `tts_callback_resume_total`

标签建议：

- `provider`
- `mode`
- `workflow`
- `voice`
- `protocol`

## 9.3 Generation Trace

建议在 `build_generation_trace` 中新增或保留以下字段：

- `tts_provider_used`
- `tts_voice`
- `tts_chars_total`
- `tts_provider_cost_estimate`
- `tts_fallback_chain`
- `tts_segment_degraded`
- `used_fallback_audio`
- `tts_protocol`
- `tts_submission_status`

## 10. 成本设计

## 10.1 第一阶段

目标：

- 先统计，不改现有用户计费主链路

建议：

- 在任务运行中累计 `tts_chars_total`
- 记录 `tts_provider_cost_estimate`
- 写入任务 trace 和任务账单扩展字段
- 区分同步 edge 成本和 paid provider 成本
- 区分 submit 成本、wait 成本和流量型成本口径

## 11. 与 Await Runtime 的对齐设计

正式异步方案建议遵循现有引擎约定：

1. `submit` 节点负责创建外部 TTS 任务
2. `await` 节点负责等待 webhook / signal / fallback poll
3. `poll_once` tool 负责单次查询 provider 任务状态
4. replay / scanner / callback handler 统一通过 await runtime 恢复

对 TTS 的启示是：

- 不应新增一套独立的“TTS wait runtime”
- `tts_poll_once` 应尽量和现有 `*_poll_once` 规范一致
- 若 provider 支持 callback，应优先把 callback 视为主路径，poll 作为兜底

## 12. 迁移建议

建议按以下顺序迁移：

1. 完成 provider 抽象和 paid provider 真实接入
2. 通过同步兼容层接入当前商品视频工作流
3. 稳定后再为 `publish` 设计 `tts_submit + await + tts_poll_once`
4. 最后再决定是否把分段 TTS 也逐步 await 化

## 10.2 第二阶段

目标：

- 将正式发布场景的 TTS 成本纳入报价模型

建议：

- 为 `publish` 单独增加 TTS 成本因子
- `draft` 默认不计入付费 TTS 成本
- 对会员或白名单任务可有特殊策略

## 11. 与现有模块的关系

## 11.1 `tts_speech_generate`

保留工具名与输出兼容性，内部替换为 service 调用。

## 11.2 `tts_generate_segments`

保留工作流位置，内部改为调用 `SynthesizeSegments`。

## 11.3 `assemble_voiceover_audio`

无需承担 provider 选择职责，继续专注于：

- 检查 segment 是否完整
- 拼接 segment
- 在必要时回退整段音频

## 11.4 `align_voiceover_timeline`

继续依赖 `ActualDuration` 做对齐，但未来可以逐步适配 provider 原生 timestamps。

## 11.5 `video_cache_lookup_v2`

需升级缓存 key，避免跨 provider 污染。

## 12. 数据结构建议

如果需要把 TTS 统计正式入库，建议为以下模型预留扩展字段：

### `TaskBillingRecord`

可增加：

- `TTSCharsTotal`
- `TTSProvider`
- `TTSCostEstimate`
- `TTSMode`

### `UserDailyUsageStat`

可增加：

- `TTSCharsTotal`
- `TTSCostEstimate`

如果当前不希望立刻改表，也可先放入：

- `pricing_snapshot`
- `extra_json`
- trace payload

## 13. 测试设计

建议覆盖四类测试：

### 13.1 单元测试

- strategy resolver
- provider request mapping
- cache key 生成
- fallback 决策

### 13.2 Provider 模拟测试

- provider success
- provider timeout
- provider auth failure
- provider 返回空音频

### 13.3 工作流集成测试

- `draft` 生成成功
- `publish` 生成成功
- `publish` 主 provider 失败后回退
- 分段不完整后回退整段音频

### 13.4 回归测试

- 缓存 key 升级后命中行为正确
- 原有 `audio_local_path`、`duration` 等字段兼容
- generation trace 保持可消费

## 14. 上线策略

建议分阶段上线：

### 阶段一

- 只接 provider 抽象
- 所有流量仍走 `edge`

### 阶段二

- 接入 `paid_primary`
- 只对 `publish` 的小流量灰度

### 阶段三

- 扩大 `publish` 流量
- 开启成本统计和日报

### 阶段四

- 视需要接入 `paid_secondary`
- 将 TTS 成本纳入正式经营模型

## 15. 风险与对策

### 风险 1：provider 切换导致音频时长变化

对策：

- 升级缓存 key
- 保留对齐链路
- 在灰度阶段重点观察时长偏差

### 风险 2：付费 provider 不稳定导致发布失败

对策：

- 保留 `edge` 兜底
- 开启 provider 级告警
- 允许一键关闭付费通道

### 风险 3：统计和成本字段引入后影响现有计费链路

对策：

- 第一阶段只做记录，不参与结算
- 与现有 `video` 计费路径解耦

### 风险 4：工作流改造过大导致回归

对策：

- 工具名和 DSL 节点名保持不变
- 先替换内部实现，再逐步增加策略能力

## 16. 结论

本次技术设计的核心思想是：

- 用 `provider + strategy + service` 三层结构，替代当前直接调用 `edge-tts` CLI 的单点实现
- 用 `draft / publish` 双模式实现“低成本预览”和“高稳定正式发布”的平衡
- 用统一缓存、埋点、trace、成本字段，支撑后续运营和计费升级

在不破坏当前商品视频主链路的前提下，这套设计能够让 TTS 从“工具依赖”升级为“可治理基础能力”。
