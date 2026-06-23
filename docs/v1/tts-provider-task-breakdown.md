# AI Engine TTS Provider 实施清单

日期：2026-04-24

状态：Draft

说明补充：

- 本清单已按 `main` 分支最新的 `await runtime / poll_once / callback` 基建更新
- 任务拆分明确区分“短期兼容落地”和“中期 await 对齐”
- 当前已落地的 TTS 骨架代码，属于 `P0-1 / P0-2 / P0-4` 的第一版基础实现，后续任务需要在最新 `main` 基线上继续收敛

关联文档：

- [AI Engine TTS Provider 分层与付费化需求文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-strategy-requirements.md)
- [AI Engine TTS Provider 技术设计文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-technical-design.md)

## 1. 说明

本清单按 `P0 / P1 / P2` 拆分，目标是直接作为开发排期、联调和验收基础使用。

每个任务包含：

- 任务目标
- 核心改动点
- 主要涉及模块
- 验收标准

## 2. P0 任务

## P0-1 建立统一 TTS Provider 抽象

任务目标：

- 将当前 TTS 调用从直接依赖 `edge-tts` CLI，升级为统一 provider 抽象

核心改动点：

- 定义统一 `TTSProvider` 接口
- 定义统一请求/响应结构
- 将整段 TTS 和分段 TTS 的公共字段收敛
- 将 `tts_speech_generate` 与 `tts_generate_segments` 改为依赖 provider 层

主要涉及模块：

- `ai-engine/tool/builtin/tts_speech_generate.go`
- `ai-engine/workflows/goods/tts_generate_segments.go`
- 新增 `ai-engine/pkg/tts/`

验收标准：

- `edge` provider 可通过统一接口驱动整段与分段 TTS
- 工作流工具不再直接拼装 `edge-tts` 命令逻辑
- 现有商品视频链路可继续正常生成音频

## P0-2 引入 TTS 策略路由

任务目标：

- 支持 `draft` 与 `publish` 走不同 TTS 策略

核心改动点：

- 定义 `TTSStrategyResolver`
- 支持按模式选择 `edge` 或付费 provider
- 支持配置默认策略、重试次数、是否允许回退
- 在商品视频工作流输入中识别 `draft / publish` 模式

主要涉及模块：

- 新增 `ai-engine/pkg/tts/strategy.go`
- `ai-engine/workflows/goods/goods_video_pro_dsl.go`
- 相关 DTO/输入结构

验收标准：

- `draft` 默认走 `edge`
- `publish` 默认走 `paid_primary`
- 策略决策结果可写入日志与 trace

## P0-3 接入首个付费 Provider

任务目标：

- 建立正式发布场景可用的付费 TTS 主通道

核心改动点：

- 新增 `paid_primary` provider 实现
- 增加 provider 配置项、超时与认证配置
- 实现整段/分段 TTS 调用
- 优先对接 provider 推荐的异步 / SSE / WebSocket 协议，而不是只做阻塞式同步 HTTP
- 规范化 provider 级错误码与错误信息

主要涉及模块：

- 新增 `ai-engine/pkg/tts/providers/`
- `config/config.go`
- `config/config.example.yaml`
- `ai-engine/server/server.go`

验收标准：

- `publish` 场景可成功走付费 provider
- 失败时能输出结构化错误
- 未配置付费 provider 时系统行为明确
- provider 至少实现一条真实的 `submit + wait` 或流式收集链路

## P0-4 建立 paid provider 的同步兼容包装

任务目标：

- 在不大改现有商品视频 DSL 的前提下，把异步付费 provider 稳妥接入当前同步工作流

核心改动点：

- `TTSService.Synthesize` 支持在 provider 具备异步能力时，内部走 `submit + wait`
- 统一输出 `protocol / provider_request_id / degraded / fallback_chain`
- 为后续正式 await 化保留兼容字段，不把异步信息丢掉

主要涉及模块：

- `ai-engine/pkg/tts/service.go`
- `ai-engine/pkg/tts/types.go`
- `ai-engine/pkg/tts/provider.go`

验收标准：

- 当前商品视频链路无需改 DSL 也能使用异步 paid provider
- 结果中能区分“真正同步 provider”和“同步包装异步 provider”
- 超时、失败、fallback 行为可预测

## P0-5 建立多层回退链路

任务目标：

- 保证正式发布链路在 provider 不稳定时仍尽量可出片

核心改动点：

- 定义 `paid_primary -> paid_secondary -> edge` 的回退框架
- 将整段 fallback 与分段 fallback 分开处理
- 在结果中显式标记 `degraded`
- 统一输出回退链路信息

主要涉及模块：

- `ai-engine/pkg/tts/strategy.go`
- `ai-engine/workflows/goods/tts_generate_segments.go`
- `ai-engine/tool/builtin/tts_speech_generate.go`
- `ai-engine/workflows/goods/assemble_voiceover_audio.go`

验收标准：

- 分段失败后可按策略重试或回退
- 整段回退音频可继续驱动成片
- 结果中可明确看到实际使用的 provider 和 fallback 路径

## P0-6 升级商品视频缓存 key

任务目标：

- 避免切换 TTS 音色、provider 或策略后命中旧缓存

核心改动点：

- 在商品视频缓存 key 中加入 `tts_provider`
- 在商品视频缓存 key 中加入 `tts_voice`
- 在商品视频缓存 key 中加入 `tts_strategy_version`
- 更新缓存命中/保存相关测试

主要涉及模块：

- `ai-engine/workflows/goods/video_cache_lookup_v2.go`
- `ai-engine/workflows/goods/video_cache_save_v2.go`
- 商品视频工作流测试

验收标准：

- 修改音色后缓存 key 变化
- 修改 provider 后缓存 key 变化
- 修改策略版本后缓存 key 变化

## P0-7 增加 TTS 结构化日志与基础指标

任务目标：

- 让 TTS 请求具备可观测性和对比能力

核心改动点：

- 为每次 TTS 请求输出统一结构化日志
- 记录 provider、voice、chars、latency、success、fallback_used
- 记录 protocol、submission_status、provider_request_id
- 在 generation trace 中保留关键 TTS 信息
- 在告警与排障日志中区分 provider 错误与本地错误

主要涉及模块：

- `ai-engine/pkg/tts/`
- `ai-engine/workflows/goods/build_generation_trace.go`
- 统一日志模块

验收标准：

- 能从日志中按 provider 聚合成功率和延迟
- 能从任务 trace 中看到是否发生 TTS 降级
- 能定位单次任务的 TTS 路由结果
- 能区分 submit、wait、stream、fallback 发生在哪一层

## 3. P1 任务

## P1-1 增加通用 TTS 音频缓存

任务目标：

- 避免相同文本在同一 provider/voice 组合下重复合成

核心改动点：

- 建立通用 TTS 缓存 key 规则
- 支持整段与分段缓存
- 支持缓存命中后的本地文件复用或下载恢复
- 明确缓存 TTL 与失效策略

主要涉及模块：

- 新增 `ai-engine/pkg/tts/cache.go`
- OSS/本地文件缓存辅助层

验收标准：

- 同一文本重复请求可命中缓存
- 切 provider 或 voice 后不会命中错误缓存
- 缓存命中结果与原始生成结果一致

## P1-2 为付费 TTS 引入 await / poll_once 正式接入

任务目标：

- 让 `publish` 付费 TTS 从“同步包装异步 provider”演进到与 `main` 一致的 await 主链

核心改动点：

- 设计并实现 `tts_submit`
- 设计并实现 `tts_poll_once`
- 评估在 `publish` 链路引入 TTS `await` 节点
- 对齐 callback ingress / replay / fallback poll 的统一约定

主要涉及模块：

- 新增 `ai-engine/workflows/.../tts_submit*.go`
- 新增 `ai-engine/workflows/.../tts_poll_once*.go`
- `ai-engine/workflow/nodes/await_*`
- provider router / callback 相关模块

验收标准：

- paid TTS 至少一条正式链路可以跑通 `submit + await + poll_once`
- replay / fallback poll 可以复用统一 await 运行时
- 不再把所有长耗时等待都阻塞在同步工具调用中

## P1-3 细化分段 TTS 的失败处理

任务目标：

- 从“整批失败后回退整段”升级到“segment 级可控回退”

核心改动点：

- 记录每个 segment 的独立状态
- 区分合成失败、时长探测失败、结果缺失等原因
- 支持只对失败 segment 重试
- 为后续更精细的字幕/时间轴策略留出口

主要涉及模块：

- `ai-engine/workflows/goods/tts_generate_segments.go`
- `ai-engine/workflows/goods/align_voiceover_timeline.go`

验收标准：

- segment 级状态可结构化输出
- 单段失败不会丢失整批上下文
- 回退与重试路径有测试覆盖

## P1-4 将 TTS 用量写入任务级统计

任务目标：

- 为后续成本分析和计费升级打基础

核心改动点：

- 记录 `tts_chars_total`
- 记录 `tts_provider_cost_estimate`
- 记录 `tts_provider_used`
- 预留 `tts` 资源类型扩展能力

主要涉及模块：

- `internal/service/billing_task_service.go`
- `internal/model/entity/task_billing_record.go`
- `internal/model/entity/user_daily_usage_stat.go`

验收标准：

- 单任务可查看 TTS 估算成本
- 每日统计可汇总 TTS 字符量
- 不影响现有视频任务计费主链路

## P1-5 增加配置开关与灰度能力

任务目标：

- 支持按环境、比例、白名单逐步切量

核心改动点：

- 增加全局 `tts_strategy_enabled`
- 增加 `publish_paid_tts_enabled`
- 增加灰度比例配置
- 增加用户或任务白名单能力

主要涉及模块：

- `config/config.go`
- 策略路由层

验收标准：

- 能快速关闭付费 TTS
- 能只对指定流量启用付费 TTS
- 灰度配置行为可测试

## P1-6 增加 TTS 集成测试与故障演练测试

任务目标：

- 确保 provider 路由、缓存、回退在真实链路上可回归

核心改动点：

- 为 `draft / publish` 分别增加集成测试
- 增加 provider 失败、超时、额度异常、fallback 触发测试
- 增加缓存 key 升级后的回归测试
- 增加 `submit / wait / poll_once / callback replay` 测试

主要涉及模块：

- `ai-engine/test/`
- `ai-engine/workflows/goods/*_test.go`

验收标准：

- 关键 TTS 策略路径有自动化覆盖
- 回退链路有明确测试断言
- 缓存污染问题可通过测试发现
- await 场景和同步兼容场景都能回归

## 4. P2 任务

## P2-1 接入第二付费 Provider

任务目标：

- 增强正式发布链路的稳定性和议价能力

核心改动点：

- 新增 `paid_secondary`
- 与现有策略路由层打通
- 补全 fallback 顺序配置

验收标准：

- 具备双付费 provider 互备能力
- 主 provider 故障时可自动切换

## P2-2 报价与积分模型纳入 TTS 成本

任务目标：

- 将 TTS 成本从“内部统计”升级为“可纳入经营模型”

核心改动点：

- 报价逻辑支持 TTS 成本因子
- 区分 `draft` 与 `publish` 的报价口径
- 为会员、试用、发布场景设不同策略

验收标准：

- 报价中可单独识别 TTS 成本项
- 不同模式下成本口径可配置

## P2-3 扩展音色与策略运营能力

任务目标：

- 让 TTS 不只是基础设施，也能成为业务配置能力

核心改动点：

- 建立音色白名单
- 支持平台/模板级默认音色
- 支持 `draft` 与 `publish` 使用不同音色策略

验收标准：

- 音色策略可配置
- 不同业务模式可有不同默认音色

## 5. 建议依赖顺序

建议按以下顺序推进：

1. `P0-1` 建立统一 TTS Provider 抽象
2. `P0-2` 引入 TTS 策略路由
3. `P0-3` 接入首个付费 Provider
4. `P0-4` 建立 paid provider 的同步兼容包装
5. `P0-5` 建立多层回退链路
6. `P0-6` 升级商品视频缓存 key
7. `P0-7` 增加 TTS 结构化日志与基础指标
8. `P1-1` 增加通用 TTS 音频缓存
9. `P1-2` 为付费 TTS 引入 await / poll_once 正式接入
10. `P1-3` 细化分段 TTS 的失败处理
11. `P1-4` 将 TTS 用量写入任务级统计
12. `P1-5` 增加配置开关与灰度能力
13. `P1-6` 增加 TTS 集成测试与故障演练测试
14. `P2-1` 接入第二付费 Provider
15. `P2-2` 报价与积分模型纳入 TTS 成本
16. `P2-3` 扩展音色与策略运营能力

## 6. 建议排期

如果按两期推进：

- 第一期：完成全部 `P0`
- 第二期：完成 `P1-1 / P1-2 / P1-3 / P1-4 / P1-5 / P1-6`

如果按三期推进：

 - 第一期：`P0-1 / P0-2 / P0-3`
 - 第二期：`P0-4 / P0-5 / P0-6 / P0-7 / P1-1`
 - 第三期：`P1-2 / P1-3 / P1-4 / P1-5 / P1-6 / P2-1`

## 7. 里程碑验收建议

### M1

- `draft / publish` 双策略可跑通
- 付费 provider 可灰度接入
- provider 失败时可回退到 `edge`
- 当前同步商品视频链路可兼容异步 paid provider

### M2

- 缓存 key 升级完成
- TTS 日志、trace、指标可观测
- 单任务 TTS 用量可统计
- 明确同步兼容层与正式 await 层的边界

### M3

- TTS 缓存可复用
- segment 级失败控制上线
- 付费 TTS 成本可进入经营分析
- paid TTS 开始对齐 `submit + await + poll_once` 正式模式
