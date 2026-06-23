# AI Engine TTS Provider 分层与付费化需求文档

日期：2026-04-24

本文档用于沉淀当前 AI Engine 中 TTS 的业务现状、问题分析、目标方案与落地要求，指导我们完成以下策略升级：

- `draft` 阶段默认使用 `edge-tts`
- `publish` 阶段默认使用付费 TTS API
- 保留 `edge-tts` 作为兜底与灾备能力
- 将 TTS 从“可用工具”升级为“可运营、可观测、可控成本”的基础能力

同时，本文档基于 `main` 分支已落地的 `await runtime / provider router / callback contract / poll_once` 异步基建更新，明确区分：

- 短期兼容方案：当前商品视频链路继续以“拿到完整音频文件”为目标，由 paid provider 在内部完成异步 submit/wait 或流式收集，再对工作流暴露同步结果
- 中期正式方案：付费 TTS 尤其是 `publish` 场景，逐步对齐 `await` 主链、`poll_once` 和统一 callback/replay 体系

## 1. 背景

当前项目中的 TTS 能力已经进入实际业务工作流，尤其影响商品视频生成链路中的：

- 口播音频生成
- 分段时长回填
- 字幕时间轴对齐
- 成片音轨拼接
- 生成质量诊断与缓存命中结果

现阶段 TTS 主要依赖 `edge-tts` 命令行工具。该方案具备以下优点：

- 接入成本低
- 无需单独申请付费 API
- 中文音色基本可用
- 适合开发、测试和低成本草稿预览

但随着业务从“能生成”走向“稳定交付”和“可计费运营”，当前实现已经暴露出明显瓶颈：

- 稳定性依赖第三方在线服务与 CLI 子进程调用
- 缺少供应商抽象，不利于灰度与切换
- 缺少 TTS 维度的用量、延迟、失败率、成本监控
- 当前计费体系按视频任务结算，尚未感知 TTS 的真实成本
- 当前缓存 key 没有纳入 `tts_voice` 和未来的 `tts_provider`，切换策略后存在缓存污染风险

因此需要把 TTS 从“单一实现”升级为“分层 provider 能力”，并按业务阶段采用差异化策略。

同时需要避免一个新的偏差：`main` 已经把异步 provider 的统一等待模型建设起来，TTS 不能长期停留在“把异步 API 藏在同步工具内部”的状态。否则会失去：

- await 状态可观测性
- webhook / replay / eventbridge / poll_once 的统一治理能力
- 长耗时 provider 的恢复和排障能力

## 2. 当前业务现状

### 2.1 当前 TTS 工具

当前服务启动时注册了两类 TTS 工具：

1. `tts_speech_generate`
   用于整段文本转语音，当前基于 `edge-tts` 生成本地音频。
2. `tts_generate_segments`
   用于按 `voiceover_plan` 逐段生成语音，并回填每段真实时长。

相关代码位置：

- [tts_speech_generate.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/tool/builtin/tts_speech_generate.go:41)
- [tts_generate_segments.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/tts_generate_segments.go:47)

### 2.2 当前主要业务使用链路

从工作流 DSL 看，当前真正直接依赖 TTS 的核心业务，是商品视频生成链路 `goods_video_pro_v3`。

链路如下：

1. `build_subtitle_timeline`
   基于脚本和 voiceover plan 先生成字幕草案。
2. `tts_generate_segments`
   对每个镜头的口播 segment 做 TTS。
3. `align_voiceover_timeline`
   根据分段 TTS 的真实时长修正 voiceover 和 subtitle 时间轴。
4. `tts_generate_full_fallback`
   生成整段口播音频，作为分段失败时的回退音频。
5. `assemble_voiceover_audio`
   若分段完整则拼接，不完整则回退整段音频。
6. `video_assemble_pro`
   将最终口播音轨与画面合成为视频。
7. `build_generation_trace`
   记录 `tts_warnings`、`tts_segment_degraded`、`used_fallback_audio`。
8. `video_cache_save_v2`
   将相关 TTS 状态保存到缓存。

相关代码位置：

- [goods_video_pro_dsl.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_pro_dsl.go:419)
- [align_voiceover_timeline.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/align_voiceover_timeline.go:21)
- [assemble_voiceover_audio.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/assemble_voiceover_audio.go:41)

### 2.3 当前业务特点

当前商品视频链路对 TTS 的要求，不只是“生成一段音频”，而是：

- 支持分段口播
- 能拿到真实音频时长
- 能驱动字幕和镜头时间轴对齐
- 某些分段失败时，仍然要保证成片可产出

这意味着 TTS 对当前业务的影响不仅是音色质量，还包括：

- 产线成功率
- 时间轴对齐精度
- 回退策略复杂度
- 缓存一致性
- 成本和报价模型

## 3. 当前实现的主要问题

### 3.1 供应商能力没有真正抽象

虽然代码中已出现 `SpeechGenerateProvider` 接口，但当前实现仍然直接执行 `edge-tts` CLI，未形成真正可切换的 provider 架构。

表现为：

- 主流程仍然直接依赖 `edge-tts`
- 分段 TTS 与整段 TTS 各自实现了一套调用逻辑
- 新接入付费 TTS 时需要修改现有工具内部实现，而不是只新增 provider

### 3.2 稳定性控制不足

当前主要问题包括：

- 直接拉起本地子进程调用 CLI
- 缺少统一的超时策略
- 缺少标准化的重试与退避
- 缺少并发控制
- 缺少按 provider 的熔断与降级机制

当前分段 TTS 的失败行为是记录 warning 并继续，之后在拼接阶段回退整段音频。这能保底出片，但不利于 SLA 管理。

### 3.3 缺少 TTS 维度的可观测性

当前生成链路只记录了：

- `tts_warnings`
- `tts_segment_degraded`
- `used_fallback_audio`

缺少以下关键指标：

- 每次 TTS 的字符数
- provider 名称
- 音色
- 请求耗时
- 成功率
- 回退次数
- 单任务 TTS 总字符数
- 单任务 TTS 成本估算

没有这些指标，就无法做：

- 供应商对比
- 成本分析
- 预算控制
- 计费模型升级

### 3.4 缓存键缺少 TTS 维度

当前带货视频缓存 key 包含：

- `workflow_name`
- `primary_image`
- `image_urls`
- 商品信息
- 平台信息
- 时长、分辨率、画幅
- `api_provider`

但没有包含：

- `tts_voice`
- `tts_provider`
- `tts_strategy_version`

这意味着：

- 切换音色时，旧缓存仍可能命中
- 切换供应商时，旧音频结果仍可能命中
- TTS 策略升级后，产物可能与当前配置不一致

相关代码位置：

- [video_cache_lookup_v2.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/video_cache_lookup_v2.go:408)
- [goods_types.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_types.go:73)

### 3.5 当前计费体系尚未纳入 TTS 成本

当前账单体系以视频任务为单位进行冻结与结算，资源类型默认为 `video`。

现状：

- 估价与冻结依据主要是视频时长、分辨率、镜头数、模型等
- 任务账单记录里没有 TTS 字符数、TTS 成本、provider 维度
- 用户日统计也未记录 TTS 用量

这意味着：

- 付费 TTS 成本当前只能被平台内部吸收
- 无法做精细化的利润分析
- 无法按业务类型区分“草稿成本”和“正式发布成本”

相关代码位置：

- [billing_task_service.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/internal/service/billing_task_service.go:60)
- [task_billing_record.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/internal/model/entity/task_billing_record.go:9)
- [user_daily_usage_stat.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/internal/model/entity/user_daily_usage_stat.go:5)

## 4. 目标方案

## 4.1 总体策略

TTS 策略升级采用分层方案：

1. `draft` 默认使用 `edge-tts`
2. `publish` 默认使用付费 TTS API
3. 付费 TTS 失败时，按策略可回退到 `edge-tts`
4. 所有 TTS 请求都统一走 provider 抽象层
5. 所有 TTS 请求都统一沉淀日志、指标和成本信息

此外，目标方案需要与当前引擎异步基建保持一致：

6. 付费 TTS provider 的接口设计需要兼容 `sync / async / sse / websocket`
7. 当前工作流允许先继续走同步包装，但 provider 契约不能只支持阻塞式调用
8. 后续需要支持 `tts_submit + await/poll_once` 的正式异步接入形态

## 4.2 推荐的 provider 策略

结合当前代码基础和中文业务场景，建议优先采用：

1. `edge`
   用于开发、测试、草稿、低成本预览、灾备回退。
2. `paid_primary`
   用于正式发布的主 provider，建议优先接入火山引擎 TTS。
3. `paid_secondary`
   预留备用付费 provider，后续可根据商业和稳定性接入阿里云等。

推荐火山优先的原因：

- 当前项目本身已使用火山的图像/视频能力，供应商整合成本较低
- 中文场景适配度更高
- 成本模型按字符更容易纳入业务报价
- 更适合视频生成类离线口播场景

## 4.3 业务分层规则

建议按业务阶段定义 TTS 策略：

### `draft`

目标：

- 快速出预览
- 成本尽量低
- 允许少量质量差异

默认策略：

- 主 provider：`edge`
- 失败后：可重试一次 `edge`
- 若仍失败：直接报错或按业务需要跳过成片

### `publish`

目标：

- 提升正式成片稳定性与音质一致性
- 降低分段失败率
- 让正式产物具备更强可控性

默认策略：

- 主 provider：`paid_primary`
- 首次失败：重试 `paid_primary`
- 持续失败：可切 `paid_secondary`
- 最终兜底：`edge`
- 当前阶段允许以“同步包装 paid async provider”的方式先落地
- 中期应逐步迁移为 `submit + await/poll_once` 正式链路

### `emergency_fallback`

目标：

- 在付费接口异常、额度不足、网络故障时，尽量保证出片

默认策略：

- 强制降级为 `edge`
- 结果需显式标记 `degraded`
- 对外和对内日志中都要保留原因

## 5. 详细需求

## 5.1 Provider 抽象层

需求：

- 引入统一的 `TTSProvider` 接口
- 统一支持整段合成与分段合成
- provider 契约需要兼容同步、异步、SSE、WebSocket 四类传输方式
- provider 不应直接耦合工作流节点
- 工作流工具只感知“策略”和“能力”，不感知具体供应商细节

建议 provider 能力分层：

- 基础能力：`Synthesize`
- 异步能力：`SubmitSynthesize`、`WaitSynthesize`
- 流式能力：`OpenSynthesisStream`

说明：

- `Synthesize` 继续作为当前商品视频链路的兼容入口
- 对火山这类建议使用异步 / SSE / WebSocket 的 provider，不应强行要求其原生实现只暴露同步接口
- 若工作流暂时仍要求拿完整音频文件，则由 service/provider 内部完成异步收敛

建议接口能力至少包含：

- `Synthesize`
- `SynthesizeSegments`
- `ProviderName`
- `SupportsTimestamps`
- `SupportsStreaming`
- `EstimateCost`

建议统一请求字段：

- `text`
- `voice`
- `rate`
- `volume`
- `pitch`
- `format`
- `mode`
- `scene`
- `task_id`
- `segment_id`
- `preferred_protocols`

建议统一输出字段：

- `audio_local_path`
- `duration`
- `provider`
- `voice`
- `chars`
- `request_latency_ms`
- `estimated_cost`
- `degraded`
- `provider_request_id`
- `protocol`
- `fallback_chain`

对于异步 / 流式 provider，还需要统一补充：

- `submission_id`
- `submission_status`
- `stream_event_type`

## 5.2 策略路由层

需求：

- 新增 `TTSStrategyResolver`
- 根据工作流模式、任务阶段、开关配置、故障状态决定路由 provider
- 支持基于 `draft / publish / fallback` 的路由
- 支持灰度比例和白名单

决策输入建议包括：

- 工作流名称
- 任务模式
- 用户类型
- 是否正式发布
- 供应商健康状态
- 配置开关

决策输出建议包括：

- 主 provider
- 重试次数
- 备用 provider
- 是否允许 edge fallback
- 首选协议类型
- 是否允许同步包装异步 provider

默认约束建议：

- `draft` 优先走同步 `edge`
- `publish` 优先走支持 `async / sse / websocket` 的 paid provider
- 若工作流当前仍是同步节点，则由 `TTSService.Synthesize` 内部根据 provider 能力决定是否走 `submit + wait`

## 5.3 分段 TTS 能力升级

需求：

- 分段 TTS 应优先走 provider 的原生能力
- 若 provider 支持时间戳或更稳定的 segment duration，应优先使用
- 统一记录每段合成结果和失败原因
- 支持对分段失败做精细化回退，而不是仅依赖最终整段 fallback
- 对支持异步 submit 的 provider，应避免长期在 segment 内做无边界阻塞等待

具体要求：

- 每段需要保留 `segment_index`、`shot_index`、`chars`、`duration`
- 每段失败原因要结构化输出
- 总体输出中需区分：
  - `segment_partial_failed`
  - `segment_duration_probe_failed`
  - `fallback_used`

阶段性要求：

- 第一阶段：商品视频分段 TTS 仍允许通过同步包装返回完整分段文件
- 第二阶段：为 `publish` 分段 TTS 预留 `submit + await/poll_once` 接入形态，避免长任务全部阻塞在单个工具调用里

## 5.4 与 Await Runtime 的对齐要求

需求：

- TTS 设计必须复用 `main` 已有异步基建，而不是另起一套等待体系
- 付费 TTS 的正式异步接入应优先考虑 `await` 节点、统一 callback ingress 和 `poll_once`
- replay、scanner、故障补偿应尽量复用统一的 await 恢复链路

阶段策略：

### 第一阶段：兼容落地

- 保留 `tts_speech_generate` 和 `tts_generate_segments` 现有同步对外形态
- `paid_primary` 可在 provider/service 内部走 `submit + wait` 或流式收集后落盘
- 目标是尽快接入真实付费 TTS，不阻塞当前商品视频主链

### 第二阶段：正式对齐

- 为付费 TTS 设计 `tts_submit`
- 为 TTS 增加 `tts_poll_once`
- 在 `publish` 链路中评估引入 `await_type=external_task` 的 TTS 等待节点
- callback / replay / fallback poll 统一复用 await runtime 约定

## 5.5 音频缓存

需求：

- 增加 TTS 结果缓存，避免相同文本反复合成
- 缓存不应只绑定视频工作流，应可作为通用 TTS 能力复用

缓存 key 至少应包括：

- `tts_provider`
- `tts_provider_version`
- `voice`
- `text_hash`
- `rate`
- `volume`
- `pitch`
- `format`

同时，商品视频缓存 key 需要纳入：

- `tts_provider`
- `tts_voice`
- `tts_strategy_version`

以避免跨策略污染。

## 5.6 可观测性与埋点

需求：

- 所有 TTS 调用都要沉淀结构化日志
- 所有 TTS 调用都要输出指标
- 生成 trace 中应能清楚看出 TTS 使用情况

至少需要记录：

- `task_id`
- `workflow_name`
- `mode`
- `tts_provider`
- `tts_voice`
- `chars`
- `segments`
- `latency_ms`
- `success`
- `degraded`
- `fallback_used`
- `estimated_cost`
- `error_code`
- `error_message`
- `protocol`
- `submission_status`
- `provider_request_id`

指标建议包括：

- TTS 请求总量
- TTS 成功率
- TTS 失败率
- 分段失败率
- submit 成功率
- wait 成功率
- poll_once 触发次数
- callback 恢复成功率
- fallback 触发率
- provider 维度 P95 延迟
- provider 维度日字符数
- provider 维度预估成本

## 5.6 计费与成本控制

需求：

- 第一阶段允许平台先内部吸收 TTS 成本
- 但系统侧必须开始沉淀 TTS 用量和成本信息
- 后续支持把 TTS 成本纳入报价和积分模型

第一阶段要求：

- 在任务级记录 `tts_chars_total`
- 在任务级记录 `tts_provider_cost_estimate`
- 在日统计中记录 `tts_chars_total`
- 预留 `resource_type = tts` 的扩展能力

第二阶段要求：

- 在报价逻辑中支持将 TTS 成本计入正式发布场景
- 对 `draft` 和 `publish` 使用不同计价口径
- 可按 provider 设不同成本因子

### 费用控制原则

1. `draft` 默认不走付费 TTS
2. 仅 `publish` 或明确要求高质量的任务启用付费 TTS
3. 对重复文案优先命中缓存
4. 对口播文本设置长度上限
5. 保留 provider 级预算监控和告警
6. 当付费 provider 异常或成本超阈值时，允许自动降级

### 业务估算口径

建议按“单视频口播字符数”估算成本，而不是按任务数粗估。

建议系统沉淀以下字段：

- `voiceover_char_count`
- `segment_count`
- `avg_chars_per_segment`
- `tts_estimated_cost`

## 5.7 回退与容灾

需求：

- 明确定义多层回退路径
- 回退不是隐式行为，必须可观测、可统计、可审计

建议优先级：

1. 分段付费 TTS 成功，走分段拼接
2. 分段付费 TTS 部分失败，尝试补偿重试
3. 若仍不完整，走整段付费 TTS
4. 若整段付费 TTS 失败，再走整段 `edge-tts`
5. 最终结果标记为 `degraded`

相关结果字段建议包括：

- `tts_provider_used`
- `tts_provider_fallback_chain`
- `tts_segment_degraded`
- `used_fallback_audio`
- `tts_degrade_reason`

## 6. 对当前业务的影响评估

## 6.1 正向影响

1. 正式发布成片的稳定性提升
2. 口播音质和一致性提升
3. 分段时长波动更可控
4. 字幕与口播对齐更稳定
5. TTS 成本第一次进入可量化、可运营状态

## 6.2 需要注意的影响

1. 切换 provider 后，同一文案的音频时长可能变化
2. 现有字幕时间轴结果会受到影响
3. 缓存策略必须同步升级
4. 部分测试用例需要从“只断言文件生成”升级为“断言 provider 输出与回退路径”
5. 运维需要关注新的外部 API 配置、额度和健康状态

## 6.3 不应发生的回归

1. 切到付费 provider 后，草稿模式成本明显上升
2. 切 provider 后旧缓存污染新结果
3. provider 故障导致正式发布完全无法出片
4. 只有付费接口能用，edge 灾备失效

## 7. 非目标

本次需求文档不要求立即完成以下事项：

- 一次性全量切换所有 TTS 流量到付费 provider
- 重构所有非商品视频业务为统一 TTS 工作流
- 立即将 TTS 成本对用户透传计费
- 立即接入多个付费 provider 并实现复杂调度算法

当前阶段的重点是：

- 建立 provider 抽象
- 落地 `draft / publish` 双策略
- 补齐可观测与控费基础设施

## 8. 分阶段落地建议

### Phase 1：基础设施改造

目标：

- 把当前 TTS 从单一实现改成 provider 化
- 不改变现有默认产线行为

范围：

- 引入 `TTSProvider`
- 引入 `TTSStrategyResolver`
- 增加统一日志与指标
- 升级缓存 key

### Phase 2：接入首个付费 provider

目标：

- 在 `publish` 场景灰度启用付费 TTS

范围：

- 接入火山 TTS
- 增加 provider 配置
- 增加 `draft / publish` 路由
- 增加 provider fallback

### Phase 3：成本与计费联动

目标：

- 将 TTS 成本纳入平台经营数据

范围：

- 记录任务级 TTS 用量
- 日统计加入 TTS 字符数
- 报价逻辑支持 TTS 成本因子

### Phase 4：精细化策略优化

目标：

- 提升质量与成本平衡能力

范围：

- 多 provider 灰度
- 音色策略分层
- 文案长度与成本策略联动
- 更细粒度的 segment 级回退

## 9. 验收标准

### 9.1 技术验收

1. `draft` 和 `publish` 能通过统一策略路由到不同 provider
2. 商品视频工作流可正常生成音频和成片
3. provider 失败时可按策略回退
4. 缓存 key 已包含 TTS 策略维度
5. TTS 日志和指标可按 provider 聚合

### 9.2 业务验收

1. `draft` 成本维持低位
2. `publish` 成片稳定性高于当前基线
3. 分段失败率可监控
4. fallback 使用率可监控
5. 单任务 TTS 成本可估算

### 9.3 运营验收

1. 能按日看到各 provider 字符消耗
2. 能按工作流看到付费 TTS 使用量
3. 能按任务追踪是否发生降级
4. 能快速关闭付费 provider 并切回 `edge`

## 10. 后续拆解建议

该文档落地后，建议继续拆成以下执行项：

1. TTS provider 抽象设计文档
2. `edge + paid_primary` 双 provider 改造任务
3. 商品视频工作流接入 `draft / publish` 策略
4. TTS 监控与埋点任务
5. TTS 缓存与缓存 key 升级任务
6. TTS 成本字段与计费扩展任务

## 11. 结论

当前项目中的 TTS 已经是商品视频产线的一部分，不再只是附属工具。继续只依赖 `edge-tts`，可以支撑低成本草稿和开发验证，但不适合作为正式发布链路的唯一方案。

因此，后续 TTS 能力建设应明确采用以下路线：

- `draft` 用 `edge-tts`
- `publish` 用付费 TTS
- `edge-tts` 保留为兜底
- 统一 provider 抽象、策略路由、缓存、埋点与成本控制

这套方案既能延续当前研发效率，也能为正式业务提供更稳定的交付基础。
