# AI Engine 可选 Usage 协议实施清单

日期：2026-04-26

状态：Draft

关联文档：

- [AI Engine 可选 Usage 协议设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-optional-usage-protocol-design.md)
- [AI Engine Task Cost Trace 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-design.md)
- [AI Engine Task Cost Trace 统一收口层设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-unified-recorder-design.md)
- [AI Engine Task Cost Usage Schema 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-usage-schema-design.md)

## 1. 目标

本实施清单的目标是把当前“猜字段式 extractor”为主的成本采集路径，逐步升级为：

1. 有成本语义的工具显式实现可选 usage 协议
2. Engine 在成功节点后统一读取 usage facts
3. Engine 将 usage facts 写入：
   - `runtime.Checkpoint["usage_facts"]`
   - `task_cost_traces`
4. 旧 extractor 从主路径降级为兼容兜底

## 2. 分阶段原则

推荐分三阶段推进：

- `P0`
  搭协议和 Engine 主路径，但不要求一次性迁完所有工具
- `P1`
  优先迁移核心资源工具
- `P2`
  收缩旧 extractor，完成平台化治理

## 3. P0：协议和 Engine 主路径

### P0-1 新增可选 Usage 协议

目标：

- 保持 `tool.Tool` 主接口不变
- 新增一个可选协议，供产生成本的工具实现

建议内容：

- 新增接口，例如：
  - `UsageAwareTool`
  - 或 `CostUsageProvider`

建议方法：

- `UsageSchema()`
- `BuildUsageFacts(input, output)`

验收标准：

- 不修改 `tool.Tool` 主接口
- 不影响现有普通工具编译

### P0-2 在 step/tool 适配层暴露 usage 能力识别

目标：

- 让 Engine 在拿到 `node.Step` 时，能判断底层工具是否实现了可选 usage 协议

建议改动点：

- [tool_step_adapter.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflow/nodes/tool_step_adapter.go)
- 必要时在 step 层增加一个轻量透传接口

验收标准：

- Engine 可以从当前执行节点拿到底层工具的 usage 能力
- 不破坏已有 step 执行逻辑

### P0-3 Engine 优先读取 usage 协议

目标：

- 在 `tryRecordNodeCost(...)` 中优先读显式 usage 协议，而不是先猜字段

建议改动点：

- [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go)
- [recorder.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/cost/recorder.go)

推荐流程：

1. 节点成功且 finalize 成功
2. 读取工具是否实现 `UsageAwareTool`
3. 若实现：
   - 调 `BuildUsageFacts(input, output)`
   - 用 `UsageSchema()` 校验
4. 写：
   - `runtime.Checkpoint["usage_facts"]`
   - `task_cost_traces`
5. 若未实现：
   - 继续走旧 extractor 兜底

验收标准：

- Engine 成功节点后能稳定记录显式 usage
- 当前已有 `task_cost_trace` 写库逻辑仍可复用

### P0-4 usage facts checkpoint 结构标准化

目标：

- 把节点级副本从现在偏 `cost_facts` 的内部结构，收敛成更稳定的 `usage_facts`

建议改动点：

- [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go)
- 节点 runtime checkpoint 读写相关逻辑

建议格式：

- `runtime.Checkpoint["usage_facts"]`
- `runtime.Checkpoint["usage_recorded_output_hash"]`

验收标准：

- 节点级排障可以直接看到显式 usage facts
- 与当前 output hash 幂等逻辑兼容

### P0-5 兼容旧 extractor

目标：

- 在迁移初期不影响现有已经接入的成本链

建议策略：

- 有 usage 协议的工具：优先使用协议
- 没有 usage 协议的工具：继续走旧 extractor

验收标准：

- `tts / llm / vlm / image_generation / video_generation` 当前可用能力不回退

## 4. P1：优先迁移核心资源工具

### P1-1 迁移 `video_generation`

优先级最高。

原因：

- 当前最容易出现 `submit / wait` 阶段语义混淆
- 显式协议最能体现“完成态记账”价值

建议改动点：

- `goods_shot_i2v_submit`
- `goods_shot_i2v_wait`
- `goods_shot_i2v_poll_once`

推荐策略：

- `submit` 可产出 submission usage，但 `billable=false`
- `wait/poll_once` 产出 completed usage，`billable=true`

验收标准：

- `video_generation` 不再依赖字段猜测作为主路径
- 不再出现 submit/wait 双记

### P1-2 迁移 `tts`

目标：

- 让 TTS 工具显式产出 chars/provider/protocol/fallback 等 usage facts

建议改动点：

- [tts_speech_generate.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/tool/builtin/tts_speech_generate.go)
- [tts_generate_segments.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/workflows/goods/tts_generate_segments.go)

验收标准：

- `tts` 主链不再依赖 extractor 猜测 chars/provider
- segment/full fallback 记账语义清晰

### P1-3 迁移 `llm`

目标：

- 让直调 `llmClient` 的工具显式输出 token usage

优先工具：

- goods 主链
- images prompt enhance
- plan/videos 主链

验收标准：

- `llm` 资源主链的 `prompt_tokens / completion_tokens / total_tokens` 来自协议，而非猜字段

### P1-4 迁移 `vlm`

目标：

- 让 VLM 工具显式输出 token usage 和 request id

优先工具：

- `analyze_product_image`
- `vlm_grounding_analyze`

验收标准：

- `vlm` 成本主链不再依赖旧 extractor

### P1-5 迁移 `image_generation`

目标：

- 让图片生成 wait/poll_once/merge 的正式记账来源于显式 usage

优先工具：

- `aliyun_*_wait`
- `volcengine_*_wait`
- `image_provider_result_merge`

验收标准：

- `image_generation` 的 provider/model/request_id/image_count 来自协议输出

## 5. P2：平台化收口和旧路径收缩

### P2-1 extractor 降级为 fallback

目标：

- 旧 extractor 只作为历史兼容，不再作为主路径

验收标准：

- 新接入工具默认不再要求补 extractor
- protocol/usage aware 工具即可进入 `task_cost_trace`

### P2-2 新工具接入模板化

目标：

- 给新工具提供 usage 协议接入模板

建议内容：

- usage schema 示例
- `BuildUsageFacts(...)` 实现模板
- 常见 `resource_type` 示例

验收标准：

- 新增一个有成本工具时，不需要先理解旧 extractor 体系

### P2-3 节点级调试增强

目标：

- 在调试工具或后台中更方便展示 `usage_facts`

建议方向：

- task/node runtime detail 页面展示 `usage_facts`
- 记账失败原因也保留在 checkpoint 或日志

验收标准：

- 成本漏记问题可快速定位到具体工具或 schema 校验失败点

### P2-4 文档和规范沉淀

目标：

- 把 usage 协议变成工具开发规范的一部分

建议补充：

- 工具开发 checklist
- 哪些资源类型必须实现 usage 协议
- 哪些字段是必填

验收标准：

- 新功能开发时，usage 协议接入成为标准流程

## 6. 推荐实施顺序

建议按以下顺序推进：

1. `P0-1` 新增可选 usage 协议
2. `P0-2` 在 step/tool adapter 暴露协议识别能力
3. `P0-3` 让 Engine 优先读 usage 协议
4. `P0-4` 统一 `usage_facts` checkpoint 结构
5. `P0-5` 保留 extractor 兼容
6. `P1-1` 优先迁移 `video_generation`
7. `P1-2` 迁移 `tts`
8. `P1-3` 迁移 `llm`
9. `P1-4` 迁移 `vlm`
10. `P1-5` 迁移 `image_generation`
11. `P2` 收缩旧 extractor 并平台化

## 7. 一句话总结

这份实施清单的核心是：

**先把“显式 usage 协议 + Engine 成功节点统一收口”这条主路径搭起来，再优先迁移 `video_generation / tts / llm / vlm / image_generation`，最终逐步淘汰“猜字段式 extractor”。**
