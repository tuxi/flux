# AI Engine Task Cost Usage Schema 实施清单

日期：2026-04-26

状态：Draft

关联文档：

- [AI Engine Task Cost Usage Schema 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-usage-schema-design.md)
- [AI Engine Task Cost Trace 统一收口层设计](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-trace-unified-recorder-design.md)
- [AI Engine Task Cost Pricing Resolver 设计稿](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/docs/task-cost-pricing-resolver-design.md)

## 1. 目标

将当前“猜字段式”成本识别逐步迁移为：

- 节点显式输出 `usage_facts`
- Engine 在成功节点统一收口
- Engine 统一写入 `task_cost_trace`

目标不是一次性推翻现有实现，而是：

1. 保证现有成本链路继续可用
2. 为新工具提供显式 usage 契约
3. 逐步把旧资源类型迁移过来

## 2. 总体策略

建议分三步推进：

### P0

先把平台骨架补齐：

- 定义 `usage_facts` schema
- Engine 优先读取显式 usage
- 旧 extractor 保持兼容兜底

### P1

优先迁移高价值资源：

- `video_generation`
- `tts`
- `llm`
- `vlm`

### P2

完成生态治理：

- `image_generation`
- 更多资源类型
- 下线大部分猜字段 extractor

## 3. P0 实施项

## 3.1 定义统一 output usage schema

### 目标

为所有工具提供统一的 `usage_facts` 输出契约。

### 任务

1. 在 `ai-engine/cost` 中定义统一 schema 结构
2. 定义校验逻辑
3. 支持：
   - 单条 usage
   - 多条 usage

### 推荐结构

```json
{
  "usage_facts": [
    {
      "resource_type": "video_generation",
      "provider": "volcengine",
      "model": "doubao-seedance-1-0-pro-fast-251015",
      "provider_request_id": "cgt-xxx",
      "usage_quantity": 1,
      "usage_unit": "jobs",
      "billable": true,
      "billable_stage": "completed",
      "usage_breakdown": {
        "duration_seconds": 3
      }
    }
  ]
}
```

### 涉及模块

- `ai-engine/cost/types.go`
- 新增 `usage_schema.go`

### 验收标准

1. 有统一 Go struct
2. 有 schema 校验函数
3. 支持未来 recorder 直接消费

## 3.2 Engine 优先读取 usage_facts

### 目标

在不破坏现有成本链路的前提下，让 Engine 先支持显式 usage schema。

### 任务

1. 在 `tryRecordNodeCost(...)` 对应 recorder 中新增：
   - 优先读取 `output["usage_facts"]`
2. 如果存在显式 usage：
   - 做 schema 校验
   - 直接进入 `task_cost_trace`
3. 如果不存在：
   - 继续走旧 extractor 兜底

### 涉及模块

- [recorder.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/cost/recorder.go)
- [executor.go](/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/engine/executor.go)

### 验收标准

1. 显式 usage 能被优先识别
2. 老工具不受影响
3. 同一节点不会同时被“显式 usage + extractor”双记

## 3.3 billable stage 规则落地

### 目标

避免 `submit / wait` 双记，明确什么节点才是正式计费完成态。

### 任务

1. 定义：
   - `submit`
   - `running`
   - `completed`
   - `finalized`
2. Engine 只让：
   - `billable = true`
   - 且 `billable_stage` 为完成态
   的 usage 进入 `task_cost_trace`

### 涉及模块

- `ai-engine/cost`
- `recorder`

### 验收标准

1. `submit` usage 可以输出但不入正式账
2. `completed` usage 会入账
3. 旧 extractor 兼容期内不打破现有流程

## 3.4 文档和工具契约更新

### 目标

把“显式 usage schema”纳入后续工具开发规范。

### 任务

1. 更新工具开发文档
2. 在 `tool-cost-output-contract.md` 里补充显式 usage schema 方案
3. 形成新工具接入 checklist

### 验收标准

1. 新工具开发有明确参考文档
2. 成本接入不再依赖口口相传

## 4. P1 实施项

## 4.1 迁移 `video_generation`

### 原因

`video_generation` 最容易出现：

- `submit`
- `wait`

双记问题，最适合作为 usage schema 第一条正式样板线。

### 任务

1. `shot_submit` 输出 `usage_facts`
   - `billable=false`
   - `billable_stage=submit`
2. `shot_wait / poll_once` 输出 `usage_facts`
   - `billable=true`
   - `billable_stage=completed`
3. Engine 优先消费显式 usage
4. 旧 `ExtractVideoGenerationFact(...)` 保留兼容兜底

### 涉及模块

- `goods_shot_i2v_submit.go`
- `goods_shot_i2v_wait.go`
- `goods_shot_i2v_poll_once.go`
- `ai-engine/cost/video_generation.go`

### 验收标准

1. 不再依赖猜字段识别 `video_generation`
2. `submit` 不入账
3. `wait` 只记一笔正式账

## 4.2 迁移 `tts`

### 原因

`tts` 已经有较完整 usage 事实，是最适合迁移的资源之一。

### 任务

1. `tts_generate_segments`
2. `tts_speech_generate`

都显式输出 `usage_facts`

建议内容：

- `resource_type=tts`
- `usage_unit=chars`
- `provider`
- `model/voice`
- `provider_request_id`
- `billable_stage=completed`

### 验收标准

1. 不再依赖 `chars_total/provider` 猜测
2. `task_cost_trace` 继续稳定入账

## 4.3 迁移 `llm`

### 原因

`llm` 的 usage 字段结构最标准，适合从显式 schema 接入。

### 任务

让已接入的 LLM 工具在 output 中显式增加 `usage_facts`，而不是只透传：

- `llm_provider`
- `llm_model`
- `prompt_tokens`
- `completion_tokens`
- `total_tokens`

### 验收标准

1. `llm` recorder 优先读取 `usage_facts`
2. 老字段透传保留给业务层展示

## 4.4 迁移 `vlm`

### 原因

`vlm` 和 `llm` 类似，但资源类型独立。

### 任务

让：

- `analyze_product_image`
- `vlm_grounding_analyze`

显式输出 `usage_facts`

### 验收标准

1. `vlm` 成本识别不再依赖猜字段
2. 与 `llm` 的资源类型边界保持清晰

## 5. P2 实施项

## 5.1 迁移 `image_generation`

### 原因

图片生成通常也有：

- `submit`
- `wait`
- `poll_once`

需要和 `video_generation` 一样做完成态区分。

### 任务

1. `submit` 输出非 billable usage
2. `wait/poll_once` 输出 billable usage
3. Engine 按 schema 收口

## 5.2 兼容 extractor 收缩

### 目标

让旧 extractor 从“主路径”退化为“兼容兜底”。

### 任务

1. 为每类资源标记迁移完成状态
2. 对已完成迁移的资源：
   - recorder 默认只认显式 usage
3. 未迁移资源继续走 extractor

### 验收标准

1. 主资源类型不再依赖猜字段
2. extractor 逻辑明显收缩

## 5.3 引入测试矩阵

### 目标

确保 usage schema 迁移不会破坏现有成本链路。

### 建议覆盖

1. 工具单测
   - usage schema 输出结构
2. recorder 单测
   - 显式 usage 优先
3. engine 集成测试
   - 成功节点后写 trace
4. 回归测试
   - 旧工具继续可记账

## 6. 风险与控制

## 风险 1

显式 usage 和旧 extractor 同时生效，导致双记。

### 控制

明确规则：

- 显式 usage 优先
- 一旦命中显式 usage，对该资源类型不再跑 extractor

## 风险 2

工具输出 schema 不稳定，导致 recorder 拒绝入账。

### 控制

1. 提供统一 struct/helper
2. 工具单测覆盖
3. schema 校验错误写入日志/事件

## 风险 3

迁移期间工具和平台口径混乱。

### 控制

1. 建立迁移清单
2. 每类资源只选一条样板线先迁移
3. 逐类扩展，而不是一次性全改

## 7. 推荐实施顺序

建议按下面顺序做：

1. `P0-1` 定义 usage schema struct 和校验
2. `P0-2` recorder 支持显式 usage 优先
3. `P0-3` billable stage 规则落地
4. `P1-1` 迁移 `video_generation`
5. `P1-2` 迁移 `tts`
6. `P1-3` 迁移 `llm / vlm`
7. `P2-1` 迁移 `image_generation`
8. `P2-2` 收缩旧 extractor

## 8. 一句话结论

这次重构的核心不是“再加更多 extractor 规则”，而是：

**把资源用量从隐式规则升级为节点显式输出契约，再由 Engine 在成功节点统一收口入账。**

实施上最稳的方式是：

- 先补平台骨架
- 再迁移高价值资源
- 最后逐步下线猜字段路径
