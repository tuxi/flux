# Goods Video TTS 输入控制实施清单

日期：2026-04-26

状态：Draft

关联文档：

- [AI Engine TTS Provider 技术设计文档](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-technical-design.md)
- [AI Engine TTS Provider 实施清单](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/tts-provider-task-breakdown.md)
- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)

## 1. 目标

为商品视频工作流补齐三个客户端可控输入：

1. `enable_tts`
2. `tts_voice`
3. `enable_subtitle`

目标不是一次性大改工作流，而是按风险拆分：

- 先交付低风险、马上可见的能力
- 再做需要调整工作流分支语义的能力

## 2. 结论

建议按以下顺序推进：

### P0

1. `enable_subtitle`
2. `tts_voice`

原因：

- 后端已有较完整基础
- 工作流语义不需要重构
- 影响范围小，最适合先上线

### P1

3. `enable_tts`

原因：

- 不是简单透传字段，而是要让“无口播视频”成为正式分支
- 需要处理 TTS 节点、音频拼接、缓存和 trace 的分支行为

## 3. 当前现状

### `tts_voice`

已经存在基础链路：

- 输入结构里已有 `tts_voice`
- `goods_video_pro_v3` 已透传到：
  - `tts_generate_segments`
  - `tts_generate_full_fallback`
- cache key 已纳入 `tts_voice`

涉及文件：

- [goods_types.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_types.go)
- [goods_video_pro_dsl.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_pro_dsl.go)
- [video_cache_lookup_v2.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/video_cache_lookup_v2.go)

### `enable_subtitle`

字幕烧录工具已有开关支持：

- `video_subtitle_burn_v2` 已支持 `enable_subtitle`
- 关闭时会直接透传原视频

但当前主 DSL 尚未把 `input.enable_subtitle` 传入该节点。

涉及文件：

- [video_subtitle_burn_v2.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/video_subtitle_burn_v2.go)
- [goods_video_pro_dsl.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_pro_dsl.go)

### `enable_tts`

当前没有正式开关，主链默认假设：

1. 一定要生成 TTS
2. 一定要拼接完整口播音频
3. 一定会将音频带入最终视频

因此 `enable_tts` 不是单纯新增字段，而是工作流分支语义变更。

## 4. 设计原则

### 原则 1

客户端开关要体现在最终输入契约中，而不是依赖客户端“自己约定”。

### 原则 2

所有会影响输出结果的输入，都要进入 cache key。

### 原则 3

对于 `enable_tts`，第一版只支持：

`enable_tts=false -> 不生成口播音频，但仍允许继续出片`

不建议第一版同时改成：

- 不生成 voiceover 文案
- 不生成 subtitle plan
- 不生成 timeline

因为这会明显扩大影响面。

### 原则 4

先保证“工作流可用”，再补前端体验细节，例如：

- 音色列表动态下发
- 不同 provider 的音色白名单联动

## 5. P0 实施项

## 5.1 `enable_subtitle`

### 目标

让客户端可以显式控制是否烧录字幕。

### 预期行为

- `enable_subtitle=true`
  - 正常执行字幕烧录
- `enable_subtitle=false`
  - 跳过烧录，直接使用原始合成视频进入 postprocess

### 后端改动点

#### 1. 输入契约补齐

在商品视频请求输入中增加：

- `enable_subtitle: bool`

建议文件：

- [goods_types.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_types.go)
- [goods_video_param_validate.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_param_validate.go)

#### 2. DSL 透传

在 `goods_video_pro_v3` 中将：

- `input.enable_subtitle`

传给：

- `subtitle_burn_v2`

建议文件：

- [goods_video_pro_dsl.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_pro_dsl.go)

#### 3. goods 主链路透传

当前聚焦 `goods_video_pro_v3`，字幕开关只需要在 goods 主链路内完成透传。

建议文件：

- [goods_video_pro_dsl.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_pro_dsl.go)

#### 4. 缓存 key

将 `enable_subtitle` 纳入 cache key，避免：

- 带字幕视频
- 不带字幕视频

命中同一缓存结果。

建议文件：

- [video_cache_lookup_v2.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/video_cache_lookup_v2.go)

#### 5. 展示与 trace

建议在最终返回或创作详情中保留：

- `enable_subtitle`

便于后台排查与结果解释。

### 前端改动点

1. 在客户端输入面板增加“字幕开关”
2. 默认值建议为 `true`
3. 在任务提交 payload 中透传 `enable_subtitle`

### 验收标准

1. 相同输入下，`enable_subtitle=true/false` 输出文件不同
2. `enable_subtitle=false` 时不执行实际烧录
3. cache 不串
4. 后台任务详情能看到该输入

## 5.2 `tts_voice`

### 目标

让客户端可以显式选择音色。

### 预期行为

- 未传 `tts_voice`
  - 走当前默认音色
- 传入 `tts_voice`
  - TTS 走指定音色

### 后端改动点

#### 1. 输入契约收口

虽然 `tts_voice` 已存在于工作流结构里，但仍建议补到统一输入校验口径中。

建议文件：

- [goods_types.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_types.go)
- [goods_video_param_validate.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_param_validate.go)

#### 2. 音色白名单

建议增加基础白名单或至少做非空字符串规范化，避免客户端误传不可用音色。

第一版可先支持：

- 不校验 provider 粒度
- 只做字符串规范化

第二版再做：

- 按 provider 返回音色列表

#### 3. 外层工作流透传校对

确认电商模式工作流都继续透传：

- `tts_voice`

当前已存在大部分透传，但上线前应补一轮回归检查。

#### 4. 最终结果/trace 回显

建议在：

- generation trace
- creative detail
- 最终 extras

中回显最终使用音色，便于调试与客服排查。

### 前端改动点

1. 增加音色选择器
2. 默认跟随后端默认音色
3. 最好在 UI 上把“草稿 / 发布”差异说明清楚

### 验收标准

1. 指定音色后，TTS 结果实际变更
2. cache 正确按音色区分
3. trace / 后台能看到音色输入和实际 provider 结果

## 6. P1 实施项

## 6.1 `enable_tts`

### 目标

让客户端可以显式关闭口播生成。

### 第一版建议语义

`enable_tts=false` 时：

1. 不调用 TTS provider
2. 不生成 voiceover 音频
3. 视频仍然可以产出
4. 字幕是否保留，由 `enable_subtitle` 独立控制

### 为什么这是 P1

因为它影响的不只是输入，而是以下链路的正式语义：

- TTS 节点是否执行
- 音频拼接节点是否可空跑
- 最终视频合成是否允许无音轨
- trace 和 cache 如何区分“关闭 TTS”与“失败 fallback”

### 后端改动点

#### 1. 输入契约

新增：

- `enable_tts: bool`

建议文件：

- [goods_types.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_types.go)
- [goods_video_param_validate.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/goods_video_param_validate.go)

#### 2. DSL 分支能力

需要让以下节点在关闭 TTS 时具备可控行为：

- `tts_generate_segments`
- `tts_generate_full_fallback`
- `assemble_voiceover_audio`
- `video_assemble_pro`

有两种可选方案：

##### 方案 A：节点内部 no-op

即：

- 继续执行节点
- 但在 `enable_tts=false` 时直接返回空结果或透传结果

优点：

- DSL 结构改动小

缺点：

- 工具内部要承担更多分支判断

##### 方案 B：DSL 条件分支

即：

- 在工作流中显式加“是否启用 TTS”的条件边

优点：

- 语义更清晰

缺点：

- DSL 改动更大

建议第一版采用：

方案 A 为主，优先降低改动面。

#### 3. `assemble_voiceover_audio` 兼容空音轨

当前该节点默认要求：

- 有 `voiceover_plan`
- 有 `tts_segments`
- 没拼出来就 fallback 整段音频

需要改成在 `enable_tts=false` 时：

- 允许成功返回
- `audio_local_path` 为空
- `used_fallback=false`
- 明确标记 `tts_disabled=true`

建议文件：

- [assemble_voiceover_audio.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/assemble_voiceover_audio.go)

#### 4. `video_assemble_pro` 验证

当前工具已支持无音频路径情况，但需要补回归测试，确保：

- 无音轨视频可正常合成
- 后续 postprocess 不报错

建议文件：

- [video_assemble_pro.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/video_assemble_pro.go)

#### 5. 缓存 key

必须将：

- `enable_tts`

纳入 cache key。

否则会出现：

- 有口播视频
- 无口播视频

互相串缓存。

建议文件：

- [video_cache_lookup_v2.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/video_cache_lookup_v2.go)

#### 6. trace 与结果回显

建议补充：

- `enable_tts`
- `tts_disabled`

并在 trace 中区分：

- 用户主动关闭 TTS
- provider 失败导致降级

这两者不能混在一起。

### 前端改动点

1. 增加“口播开关”
2. 默认建议为 `true`
3. 如果关闭口播，建议同时提示：
   - 视频将不包含配音
   - 字幕可单独保留或关闭

### 验收标准

1. `enable_tts=false` 时不会触发实际 TTS 调用
2. 无音轨视频能正常产出
3. trace 中明确区分 `tts_disabled` 与 `tts_failed`
4. cache 不串

## 7. 推荐开发顺序

### 第 1 步

先做 `enable_subtitle`

原因：

- 风险最低
- 价值直接可见
- 后端只需接线

### 第 2 步

再做 `tts_voice`

原因：

- 后端主链已具备基础
- 主要是输入契约和前端面板补齐

### 第 3 步

最后做 `enable_tts`

原因：

- 这是语义级改动
- 需要工作流显式支持“无口播视频”

## 8. 建议拆分方式

### P0-A

`enable_subtitle`

### P0-B

`tts_voice`

### P1

`enable_tts`

## 9. 一句话结论

这三项不是同一难度级别：

- `字幕开关`：小改，适合立刻做
- `音色选择`：中小改，当前后端基础已在
- `TTS 开关`：中到大改，建议在 P1 作为正式分支能力实现

最稳的推进方式是：

先把 `enable_subtitle + tts_voice` 做成一轮低风险交付，再单独推进 `enable_tts`。
