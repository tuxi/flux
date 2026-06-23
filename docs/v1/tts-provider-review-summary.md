# AI Engine TTS Provider 评审汇报版摘要

日期：2026-04-24

关联文档：

- [AI Engine TTS Provider 分层与付费化需求文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-strategy-requirements.md)
- [AI Engine TTS Provider 实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-task-breakdown.md)
- [AI Engine TTS Provider 技术设计文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-technical-design.md)

## 一页结论

当前项目中的 TTS 已经不是一个孤立工具，而是商品视频生成链路里的关键基础能力。它直接影响：

- 口播音频生成
- 分段真实时长回填
- 字幕与口播时间轴对齐
- 成片音轨拼接
- 缓存结果与生成诊断

现阶段继续只依赖 `edge-tts`，可以支撑开发、测试和低成本草稿预览，但不适合作为正式发布链路的唯一方案。

本次建议立项，采用明确分层策略：

- `draft` 默认使用 `edge-tts`
- `publish` 默认使用付费 TTS
- 保留 `edge-tts` 作为最终兜底
- 统一 provider 抽象、策略路由、回退链路、缓存、埋点和成本统计

同时，这个方案需要对齐 `main` 已经落地的异步基建，而不是独立造一套 TTS 等待体系：

- `await runtime`
- `provider router / callback contract`
- `poll_once`
- replay / scanner / webhook 恢复链路

目标不是“换一个 TTS 接口”，而是把 TTS 从“能调用”升级为“可运营、可观测、可控成本的基础设施”。

## 为什么现在做

当前商品视频工作流已经形成完整 TTS 依赖链路：

1. 先生成字幕草案
2. 再做分段 TTS
3. 再按真实时长对齐字幕和口播时间轴
4. 分段不完整时回退整段音频
5. 最终把音频与视频合成为成片

这意味着 TTS 的问题不再只是音质问题，而是产线问题。

目前的核心风险已经很清晰：

- 当前仍直接调用 `edge-tts` CLI，缺少真正的 provider 抽象
- 缺少超时、重试、熔断和分层回退治理
- 缺少 provider 维度的日志、延迟、成功率和成本统计
- 商品视频缓存 key 没有纳入 `tts_voice / tts_provider / tts_strategy_version`
- 当前计费体系按视频任务结算，尚未纳入 TTS 成本

如果现在不做，后续问题会逐步扩大：

- 正式发布链路的稳定性无法保证
- 换音色或换 provider 后可能命中旧缓存
- 成本开始发生时，系统却没有统计口径
- 业务上线越多，回退和排障成本越高

## 本期要做什么

本期建议聚焦 7 件事：

1. 建立统一 TTS Provider 抽象
2. 引入 `draft / publish` 策略路由
3. 接入首个付费 Provider
4. 为异步 paid provider 增加同步兼容包装
5. 建立 `paid -> edge` 的多层回退链路
6. 升级商品视频缓存 key
7. 增加 TTS 结构化日志、指标和成本统计基础

对应的技术方案是：

- `provider layer`
- `strategy layer`
- `service layer`
- `workflow adapter layer`
- `trace / billing layer`

并新增一个关键约束：

- 付费 TTS 的接口设计要兼容 `sync / async / sse / websocket`

工作流侧尽量少动 DSL 名称，只替换工具内部实现，降低回归风险。

## 本期不做什么

- 不一次性全量切换所有 TTS 流量到付费 provider
- 不立即把 TTS 成本直接透传给用户计费
- 不优先做复杂多 provider 动态调度
- 不改造成面向用户的实时流式 TTS 播放链路
- 不要求所有历史业务工作流同步迁移
- 不在本期强行把所有 TTS 调用都直接改成 `await` 节点

## 建议方案

### 业务策略

`draft`

- 默认走 `edge`
- 目标是低成本、快速预览

`publish`

- 默认走 `paid_primary`
- 失败后按策略回退到 `paid_secondary` 或 `edge`
- 目标是正式发布时提高稳定性和音质一致性

### 技术策略

- 工作流工具继续保留 `tts_speech_generate` 和 `tts_generate_segments`
- 内部改为调用统一 `TTSService`
- provider 选择、重试、fallback、缓存、埋点全部下沉到统一能力层
- 付费 provider 优先按异步 / SSE / WebSocket 能力接入
- 当前商品视频链路先通过同步兼容层消费 paid provider
- 稳定后再逐步演进到 `tts_submit + await + poll_once`

### 成本策略

- 第一阶段先统计，不改现有主计费链路
- 先记录 `tts_chars_total`、`tts_provider_used`、`tts_provider_cost_estimate`
- 后续再将 `publish` 场景的 TTS 成本纳入报价或积分模型

## 预期收益

业务收益：

- 正式发布成片稳定性提升
- 分段失败率更低
- 字幕和口播对齐更稳
- 发布与草稿的成本模型分离更清晰

研发收益：

- TTS 不再散落在多个工具里各自处理
- 能解释本次任务为什么走某个 provider
- 能解释为什么发生了 fallback 或 degraded
- 新接入 provider 的成本显著下降

工程收益：

- 缓存键更安全，避免跨 provider 污染
- TTS 有统一日志、指标和 trace
- 后续成本分析和经营统计有数据基础
- 后续若将 TTS 正式纳入 await 体系，迁移成本会更低

## 主要风险

### 风险 1：切 provider 后音频时长变化

影响：

- 字幕时间轴和口播节奏可能变化

对策：

- 保留现有 `align_voiceover_timeline`
- 升级缓存 key
- 灰度阶段重点观察时长偏差

### 风险 2：付费 provider 不稳定导致发布失败

影响：

- 正式发布链路受阻

对策：

- 保留 `edge` 最终兜底
- 增加 provider 级告警
- 提供配置开关快速关闭付费通道

### 风险 4：异步 provider 被长期藏在同步包装里

影响：

- 失去 await runtime 的恢复、补偿、排障优势

对策：

- 明确同步包装只是第一阶段过渡方案
- 在 P1 把 paid TTS 纳入 `submit + await + poll_once`

### 风险 3：统计和成本字段引入后影响现有账单逻辑

影响：

- 可能误伤现有视频任务计费

对策：

- 第一阶段只记录，不参与结算
- 与现有 `video` 资源计费主链路解耦

## 建议优先级

### P0

- 统一 TTS Provider 抽象
- 引入 `draft / publish` 策略路由
- 接入首个付费 Provider
- 为异步 paid provider 增加同步兼容包装
- 建立多层回退链路
- 升级商品视频缓存 key
- 增加日志与基础指标

### P1

- 增加通用 TTS 音频缓存
- 为付费 TTS 引入 `await / poll_once` 正式接入
- 细化 segment 级失败处理
- 将 TTS 用量写入任务级统计
- 增加灰度和白名单能力
- 补齐集成测试与故障演练测试

### P2

- 接入第二付费 Provider
- 将 TTS 成本纳入报价模型
- 扩展音色与策略运营能力

## 建议实施顺序

1. 先完成 provider 抽象与策略路由
2. 再接入 `paid_primary`
3. 然后通过同步兼容层把 paid provider 接入当前商品视频链路
4. 再完成回退链路、缓存 key 升级、日志指标
5. 再补通用 TTS 缓存、`await / poll_once` 正式接入、segment 级失败治理和任务级统计
6. 最后再扩展双付费 provider 和经营计费联动

## 建议评审结论

建议通过立项，并按 `P0 -> P1` 推进。

建议本次评审会重点确认 5 个决策：

1. 是否确认 `draft -> edge`、`publish -> paid` 作为默认业务策略
2. 是否确认首个付费 provider 优先接入火山
3. 是否确认第一阶段先做统计，不把 TTS 成本直接并入现有用户计费
4. 是否确认商品视频工作流作为首个接入与验证场景
5. 是否确认第一阶段允许“同步包装异步 paid provider”，第二阶段再对齐 await runtime

如果这 5 点确认，本项目就可以直接进入 P0 研发拆解和排期。
