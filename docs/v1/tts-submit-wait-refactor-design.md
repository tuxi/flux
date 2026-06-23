# TTS 重构问题说明与 `submit/wait` 方案

## 背景

当前 TTS 能力已经具备以下基础：

- `draft -> edge-tts`
- `publish -> paid -> edge fallback`
- 火山异步长文本 TTS 已接通
- 商品视频主链已经能消费 TTS 音频、字幕对齐和成本记录

但随着真实任务联调深入，当前 TTS 架构的可观测性和可排障性问题已经非常明显，尤其在 `publish` 走 paid TTS 的场景下。

## 当前问题

### 1. paid TTS 被错误地包装成同步黑盒

当前 `pkg/tts` 的主入口仍然是同步语义：

- `DefaultService.Synthesize(req)`

对于 paid TTS（如火山异步长文本）来说，真实过程其实是：

1. submit 创建异步任务
2. query/wait 轮询任务结果
3. download 下载音频
4. probe 探测音频时长

但现在这些阶段被包装成一次同步调用，导致工作流层看不到真实阶段边界。

### 2. 节点表缺少关键排障信息

当前在 `task_nodes` 里只能看到：

- `tts_generate_segments`
- `tts_generate_full_fallback`

但看不到：

- submit 是否成功
- submit 后生成了哪个 `job_id`
- query 是超时、后端合成失败、还是下载失败
- fallback 是在哪个阶段触发

结果就是问题只能从 warning 文本里倒推，而不是从节点状态直接判断。

### 3. submit 成功 / wait 失败时，状态和成本语义不清晰

真实联调中已经出现：

- submit 成功
- query 返回 50001 合成失败

当前模型下，工作流只会表现成“整次 TTS 失败后 fallback 到 edge”。

但从资源语义上：

- 外部任务已经提交
- 可能已经产生供应商资源消耗
- 结果却没有成功返回

如果继续保持同步黑盒模型，后续在以下方面都会持续模糊：

- 计费
- 对账
- 重试边界
- SLA 诊断

### 4. 重试有重复提交风险

如果 submit 和 wait 混在同一个节点中：

- 节点重试
- worker 重试
- 手动 retry

都可能再次触发 submit，从而造成：

- 重复创建外部 TTS 任务
- 重复计费
- 多份结果并存

这与工作流系统“清晰管理外部异步任务生命周期”的目标相违背。

### 5. `tts_generate_full_fallback` 不是按需兜底，而是默认重复生成

当前主工作流中同时存在：

- `tts_generate_segments`
- `tts_generate_full_fallback`

其中 `tts_generate_full_fallback` 会无条件执行整段 TTS：

- 不判断分段 TTS 是否成功
- 不判断分段拼接是否成功
- 不等待分段链路结果

这意味着当前所谓的 `full fallback`，并不是“在主链失败后按需触发的兜底”，而是：

- 主链分段 TTS 执行一次
- 整段文本再默认完整执行一次

这会带来明显问题：

- 重复调用供应商
- 重复资源消耗
- paid TTS 下存在重复计费风险
- 增加整体时延
- 掩盖主链真实问题

因此，`tts_generate_full_fallback` 当前的真实语义不是 fallback，而是 **eager duplicate generation**。

这已经是现有 TTS 架构中的一个明确资源浪费点。

## 结论

paid TTS 不应继续维持当前这种“同步 service 黑盒”模型。

下一阶段应按工作流异步资源的标准形态，拆成两个节点：

1. `tts_submit`
2. `tts_wait` 或 `tts_poll`

核心原则：

- submit 必须是单独节点
- submit 成功后必须产出稳定 `job_id`
- wait/poll 只能消费已有 `job_id`
- wait/poll 失败不能重新 submit
- 成本和状态至少要区分 `submit` 与 `completed`

## 为什么必须拆成两个节点

### 1. submit 是幂等边界

工作流系统的价值之一就是：

- 创建外部任务
- 等待外部任务结果

必须拆开管理。

只有 submit 独立成节点，才能保证：

- 只要 submit 成功一次
- 后续无论 wait 重试多少次
- 都不会重复创建外部任务

### 2. 节点表会直接变成排障入口

拆分后，数据库中可直接观察：

#### `tts_submit`

- success / failed
- `provider`
- `job_id`
- `provider_request_id`
- submit 原始返回信息

#### `tts_wait`

- running / success / failed
- query 返回状态
- 下载是否成功
- 音频路径、时长、字幕句级时间戳
- 最终失败原因

这意味着排障时无需再从 warning 字符串里反推阶段。

### 3. 成本和状态语义会更清楚

拆分后可以明确记录：

- `billable_stage = submit`
- `billable_stage = completed`

后续无论定价策略是：

- 仅按成功结果计费
- submit 即计费
- submit 与 completed 分阶段计费

都能有清晰的事实基础。

## 最小可行重构方案

本阶段不要求强依赖 `await runtime`，先实现工作流层面的两节点拆分即可。

### 节点一：`tts_submit_paid`

职责：

- 调用 paid provider 的 submit 接口
- 创建外部 TTS 任务
- 返回稳定 `job_id`

建议输出：

- `provider`
- `tts_type`
- `protocol = async`
- `job_id`
- `provider_request_id`
- `voice`
- `chars`
- `resource_type = tts`
- `billable_stage = submit`

失败语义：

- 只有 submit 失败才会在这里失败
- 不负责 query

### 节点二：`tts_wait_paid`

职责：

- 轮询 query 结果
- 下载音频
- 探测时长
- 产出字幕句级时间戳

建议输入：

- `job_id`
- `provider`
- `provider_request_id`
- `tts_type`
- `voice`
- `chars`

建议输出：

- `audio_local_path`
- `duration`
- `subtitle_sentences`
- `provider`
- `resource_type = tts`
- `billable_stage = completed`

失败语义：

- 只表示查询/下载/最终完成阶段失败
- 不允许再次 submit

## fallback 建议

`publish` 路径下的 fallback 也应拆阶段考虑：

1. `tts_submit_paid`
2. `tts_wait_paid`
3. 如果 wait/query 失败，再触发 `edge` fallback

这样可以明确区分：

- submit 未成功：无需进入 wait
- submit 成功但 wait 失败：允许 fallback 到 edge
- fallback 的真实触发点：在 wait 之后，而不是隐藏在同步 service 内部

### 推荐的 fallback 机制

下一阶段不建议再使用当前这种“并行跑一份整段 full fallback 音频”的方式。

推荐改成：

- `tts_submit` 输出明确的执行类型和任务标识
- `tts_wait` 根据类型查询结果
- 如果 paid wait/query 失败，再在 wait 阶段按需降级到 edge

也就是说，`tts_submit` 至少应输出：

- `tts_type`
- `provider`
- `job_id`
- `provider_request_id`

例如：

- paid 异步任务：`tts_type = volc_async`
- edge 本地任务：`tts_type = edge_local`

随后 `tts_wait` 根据 `tts_type` 决定：

- 如何查询状态
- 是否需要下载结果
- 是否允许触发 edge fallback

这样可以保证：

- submit 仍然是唯一幂等边界
- wait 重试不会重复提交
- fallback 仍然保留
- fallback 是按需触发，而不是默认双跑

### 关于 `tts_generate_full_fallback` 的结论

下一阶段不建议继续保留当前这种“默认执行整段 TTS”的 full fallback 设计。

推荐结论是：

- **移除当前 `tts_generate_full_fallback` 节点**
- 不再默认并行生成整段音频
- 让分段 `submit/wait` 主链承担唯一的正式口播生成职责
- 如果未来仍需保底方案，也必须改成“由 `tts_wait` 根据 `tts_type` 和 wait 结果按需触发”的懒兜底分支

也就是说：

- 当前 full fallback 应移除
- 未来如果重新引入 fallback，也只能是 failure-driven，而不是 eager duplicate generation
- 推荐落点是在 `tts_wait` 阶段，而不是额外并行节点

## edge-tts 的统一方向

虽然 `edge-tts` 本身是同步 CLI，但长期也可以包装成同样的两阶段模型：

1. `tts_submit_edge`
2. `tts_wait_edge`

具体做法是：

- `submit` 先创建本地 TTS job
- 丢入本地队列或任务表
- 返回内部 `job_id`
- `wait` 再按 `job_id` 查询状态

这样 paid TTS 与 edge TTS 就可以统一成同一套工作流协议。

短期内不要求立即这样做，但长期是更整齐的架构方向。

## 对现有架构的影响

### 需要改的部分

- `pkg/tts` 的 provider/service 契约
- `goods_video_pro_v3` 的 TTS 节点编排
- fallback 逻辑
- TTS 相关 `usage facts` 和 `task_cost_trace` 颗粒度

### 不一定马上要改的部分

- 不强制要求本阶段接入 `await runtime`
- 不强制要求 edge-tts 立即改成异步

## 推荐实施顺序

### P0

- 设计 `tts_submit_paid` / `tts_wait_paid` 的输入输出 schema
- 在 goods 工作流里先替换 paid TTS 主路径
- 保留 edge fallback
- 移除当前 `tts_generate_full_fallback`

### P1

- 调整 TTS usage facts 颗粒度，区分 submit/completed
- 优化节点表和 trace 可观测性

### P2

- 评估 edge-tts 是否也统一为 submit/wait 模型
- 再决定是否接入统一 `await runtime`

## 预期收益

重构完成后，系统将获得：

- 更清晰的节点级状态边界
- 不重复 submit 的幂等保证
- 更准确的成本和对账语义
- 更容易定位 paid TTS 的真实失败阶段
- 更适合后续定价和 SLA 建设的异步任务模型

## 总结

当前 TTS 架构“能跑”，但在 paid TTS 场景下，已经明显不适合继续作为长期模型。

真正的问题不是 provider 抽象不够，而是：

- 将异步资源错误地封装成了同步黑盒

下一阶段最合理的方向，是将 paid TTS 正式重构为：

- `tts_submit`
- `tts_wait/poll`

并把 submit 作为独立节点和幂等边界。
