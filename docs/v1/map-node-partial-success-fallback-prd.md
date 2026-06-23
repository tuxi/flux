# Map Node Partial Success and Fallback PRD

## 背景

当前 `MapNodeStep` 是 all-or-nothing 语义：任一子任务失败，Map 节点失败，随后父工作流通过失败闭包关闭下游路径，最终父任务失败。

这个语义适合强依赖批处理，但不适合可选增强类节点。例如 `goods_video_timeline_v1` / `goods_video_pro` 中的 `augment_images_multi`：

- Map 下发多个图片增强子任务。
- 部分子任务成功，部分子任务因参数错误失败。
- 当前 Map 整体失败，导致 `merge_augmented_images` 被 skipped。
- 已成功的增强图没有被利用。
- 父任务最终失败。

真实案例：

- 父任务：`2053271605454323712`
- 失败子任务：`2053272132225347584`
- 失败类型：参数错误 / 模型不支持，属于确定性失败，重复重试不会成功。

期望：`augment_images_multi` 作为可选增强节点，应支持 partial success。成功的增强结果继续被使用，失败的 item 使用原始素材 fallback 补位，父任务继续完成，并在 output 中保留 warning。

## 目标

1. 为 `MapNodeStep` 增加显式 failure policy。
2. 保持默认 `fail_fast` 行为，避免影响现有强依赖 Map。
3. 支持 `partial` 模式：部分子任务失败时，Map 使用 fallback result 补齐对应 index，并以 success 收口。
4. 让 `augment_images_multi` 可以利用已成功的兄弟子任务结果。
5. 避免确定性参数错误反复自动重试，减少无效成本。
6. 建立可复用的弹性执行语义，避免“任务成功但质量不可控”。

## 非目标

- 不修改 DAG join 语义。
- 不修改主执行器调度模型。
- 不修改 Loop 节点。
- 不在 Phase 1 支持成功 child item redo。
- 不在 Phase 1 做复杂 per-index retry state machine。
- 不把 failed child task 改写成 success。child task 可继续保留 failed，用于审计。

## 当前行为

### 同步聚合路径

`MapNodeStep.processExistingChildren` 遇到 failed child 后直接返回错误：

```go
case domain.TaskFailed:
    return fmt.Errorf(
        "map child task failed at index=%d, task_id=%d, retry_count=%d",
        index, t.ID, t.RetryCount,
    )
```

结果：

```text
child failed
=> Map Run 返回 error
=> Map node failed
=> failClosure 关闭下游边
=> 父任务最终 failed
```

### 异步事件路径

`startSubWorkflowFailedListener` 监听 child task failed。对于 Map / Loop，当前逻辑是：

- 如果 child retry 未耗尽，唤醒父任务。
- 如果超过 `domain.MaxAutoRetryCount`，调用 `permanentFailParent`，将父节点和父任务标记为失败。

这导致参数错误类失败可能重复唤醒 / 重试，直到最终失败。

## 设计概览

新增 Map 配置：

```go
Config: map[string]any{
    "failure_policy":    "partial", // fail_fast | partial
    "max_child_retries": 0,
    "fallback_source":   "item",
    "max_fallback_ratio": 0.5,
}
```

默认值：

```text
failure_policy = fail_fast
max_child_retries = -1 // 使用现有全局默认策略
fallback_source = item
max_fallback_ratio = 1.0 // Phase 1 可先不强制
```

### fail_fast

保持当前行为：

```text
任一 child failed
=> Map failed
=> 下游失败闭包
=> 父任务 failed
```

### partial

新增行为：

```text
child success
=> 写入正常 result

child failed
=> 写入 fallback result
=> Map 不失败

所有 index 都有 success 或 fallback result
=> Map success(partial)
=> 下游继续消费混合 results
```

## 详细设计

### 1. MapNodeStep 字段

```go
type MapNodeStep struct {
    itemsExpr       string
    iterator        string
    workflow        string
    parallel        int
    failurePolicy   string // fail_fast | partial
    maxChildRetries int    // -1 = use global default
    fallbackSource  string // item
    maxFallbackRatio float64
}
```

建议常量：

```go
const (
    MapFailurePolicyFailFast = "fail_fast"
    MapFailurePolicyPartial  = "partial"
    MapFallbackSourceItem    = "item"
)
```

### 2. Factory 解析配置

`map_node_factory.go` 解析：

- `failure_policy`
- `max_child_retries`
- `fallback_source`
- `max_fallback_ratio`

非法值回退默认值，或构建失败。建议 Phase 1 对非法 `failure_policy` 直接返回构建错误，避免静默降级。

### 3. Checkpoint 存储策略

`initCheckpoint` 增加：

```go
"failure_policy":    m.failurePolicy,
"max_child_retries": m.maxChildRetries,
"fallback_source":   m.fallbackSource,
"max_fallback_ratio": m.maxFallbackRatio,
"warnings":          []any{},
"failed_count":      0,
```

目的：

- event listener 可以通过 parent runtime checkpoint 读取 failure policy。
- resume / retry 后策略不丢失。
- finalize output 能输出 partial 元数据。

旧任务 checkpoint 没有这些字段时，默认按 `fail_fast` 处理。

### 4. processExistingChildren

需要让 `processExistingChildren` 接收 items：

```go
processExistingChildren(execCtx, runtime, items)
```

partial 模式处理 failed child：

```go
case domain.TaskFailed:
    if m.failurePolicy == MapFailurePolicyPartial {
        if m.hasCheckpointResult(runtime.Checkpoint, index) {
            continue
        }

        fallback := m.buildFallbackResult(index, t, items)
        itemHash := m.getItemHash(items, index)
        runtime.WriteMapItemResult(index, itemHash, fallback, false)
        m.appendWarning(runtime.Checkpoint, index, t)
        changed = true
        continue
    }

    return fmt.Errorf(...)
```

关键要求：

- 写 fallback 必须幂等。
- index 已有 result 时不能重复写。
- `done` 只能在首次写入 index result 时增加。
- child task 可以继续保持 failed 状态。

### 5. Fallback Result

Phase 1 支持 `fallback_source=item`。

基础结构：

```json
{
  "index": 0,
  "status": "fallback",
  "quality": "low",
  "source": "original",
  "degraded": true,
  "fallback_used": true,
  "fallback_source": "item",
  "error": "parameter error: ...",
  "child_task_id": 2053272132225347584,
  "original_item": {}
}
```

对于图片增强场景，fallback result 应尽量补齐下游常用字段：

```json
{
  "primary_file_url": "https://...",
  "image_url": "https://...",
  "preview_url": "https://..."
}
```

字段提取策略：

1. 从 item 中优先读取 `source_image_url`。
2. 其次读取 `image_url`。
3. 其次读取 `primary_image` / `primary_file_url`。
4. 如果 item 有嵌套 input，可按已知字段递归查找。

注意：fallback result 结构必须与 `merge_augmented_images` 兼容。实现前需要确认 `merge_augmented_images` 的消费字段。

### 5.1 Result Semantics

Map partial 不应只把 fallback 塞进 `results` 数组，还需要给每个 item 明确结果语义。

建议每个 result 都尽量包含：

```json
{
  "index": 0,
  "status": "success | fallback | skipped | failed",
  "quality": "high | medium | low",
  "degraded": false,
  "source": "ai | fallback | cache | original",
  "warnings": []
}
```

推荐语义：

- AI 子任务成功：`status=success`，`quality=high`，`source=ai`，`degraded=false`
- fallback 到原图：`status=fallback`，`quality=low`，`source=original`，`degraded=true`
- 复用缓存：`status=success`，`quality=medium/high`，`source=cache`

这可以避免所有结果都表现为普通 success，导致 UI、下游策略和数据分析无法区分真实质量。

### 5.2 Position Semantics

Map 的 `results` 数组通常隐含 index 语义，例如：

```text
input[0] -> results[0]
input[1] -> results[1]
input[2] -> results[2]
```

partial 模式下，失败 item 会被 fallback result 补位。为了避免数组位置承载过多隐式业务语义，output 必须显式输出 index meta：

```json
{
  "results": [],
  "meta": {
    "success_indexes": [0, 2],
    "failed_indexes": [1],
    "fallback_indexes": [1]
  }
}
```

原则：

- `results` 长度应与输入 item 数量保持一致，方便下游按 index 对齐。
- 每个 result 必须带 `index`。
- 下游需要判断质量时，不应只看数组位置，应读取 `status` / `quality` / `degraded`。

### 6. finalizeCompleted 输出

`finalizeCompleted` 在 output 中增加 partial 元数据：

```json
{
  "results": [],
  "partial_success": true,
  "success_count": 2,
  "failed_count": 1,
  "fallback_count": 1,
  "fallback_rate": 0.3333,
  "high_quality_count": 2,
  "low_quality_count": 1,
  "meta": {
    "success_indexes": [0, 2],
    "failed_indexes": [1],
    "fallback_indexes": [1]
  },
  "warnings": [
    {
      "index": 0,
      "child_task_id": 2053272132225347584,
      "message": "child task failed, fallback result used"
    }
  ]
}
```

默认 `fail_fast` 下 output 保持现状。

### 6.1 Quality Metrics

partial success 会提升任务成功率，但如果没有质量指标，会造成“成功率虚高”。例如任务从 failed 变为 success，但实际结果大量 fallback。

建议输出和埋点至少区分：

- `task_success_rate`
- `task_partial_rate`
- `fallback_rate`
- `high_quality_rate`
- `low_quality_rate`

真正衡量系统质量时，应优先看：

```text
high_quality_success_rate
```

而不是只看 task success rate。

### 6.2 Decision Layer

partial 不应无条件吞掉所有失败。需要引入决策阈值，避免“全部 fallback 但任务仍 success”的低质量结果。

建议配置：

```go
Config: map[string]any{
    "failure_policy":     "partial",
    "max_fallback_ratio": 0.5,
}
```

语义：

- `fallback_ratio <= max_fallback_ratio`：Map success(partial)
- `fallback_ratio > max_fallback_ratio`：Map failed，或返回需要用户重试的错误

Phase 1 可以先只输出 `fallback_rate`，不强制 fail。Phase 2 再启用阈值决策。

### 7. Failed Listener

`event_listen.go` 中 `startSubWorkflowFailedListener` 对 Map 节点增加 partial 分支：

```go
case ParentFanoutNodeMap:
    failurePolicy := getMapFailurePolicy(parentRuntime.Checkpoint)
    if failurePolicy == MapFailurePolicyPartial {
        e.resumeParentTask(parentID, parentNode, nil)
        continue
    }

    // existing fail_fast behavior
```

语义：

- partial Map 的 child failed 不触发 `permanentFailParent`。
- 只唤醒 parent，由 Map 聚合逻辑写 fallback result。
- 旧任务 / 无 checkpoint 策略时，保持 fail_fast。

### 8. max_child_retries

Phase 1 支持最小语义：

- `max_child_retries = 0`：partial 模式下 child failed 后立即 fallback，不继续自动重试。
- `max_child_retries = -1`：沿用当前全局默认。

复杂 per-index retry 计数放到 Phase 2：

```json
{
  "child_retry_counts": {
    "0": 1,
    "2": 0
  }
}
```

Phase 2 再支持：

- 每个 index 独立 retry count。
- 达到 `max_child_retries` 后写 fallback。
- non-retryable error 直接 fallback。

## augment_images_multi 配置

`goods_video_timeline_v1_dsl.go` 和 `goods_video_pro_dsl.go` 中的 `augment_images_multi` 建议配置：

```go
Config: map[string]any{
    "items":             "augment_product_images.augment_specs",
    "iterator":          "spec",
    "workflow":          "image_to_image",
    "parallel":          2,
    "failure_policy":    "partial",
    "max_child_retries": 0,
    "fallback_source":   "item",
    "max_fallback_ratio": 0.5,
}
```

实际字段名需要对照当前 DSL，可能是：

```go
"items":    "augment_product_images.requests",
"iterator": "request",
```

以现有代码为准。

## 兼容性

- 默认 `failure_policy=fail_fast`，现有 Map 行为不变。
- 只有显式配置 `partial` 的 Map 节点启用新语义。
- child task 失败状态保留，不影响审计。
- Map parent node 可以 success(partial)，父任务可以 success。
- 下游需要能识别 fallback result。
- output 会显式标记 `partial_success`、`fallback_rate`、`quality`，避免质量问题被普通 success 掩盖。

## 幂等性

必须保证：

- 同一个 failed child 被重复事件唤醒时，只写一次 fallback result。
- 已有 success result 的 index 不会被 fallback 覆盖。
- `done` 不会重复增加。
- warnings 不会无限重复追加。
- checkpoint 更新失败后再次恢复可以重试。
- `success_indexes` / `failed_indexes` / `fallback_indexes` 按 index 去重。

建议以 index 为幂等 key。

## 测试用例

### 1. 默认行为不变

Map 默认 `fail_fast`，1 个 child failed：

- Map 返回 error。
- Map node failed。
- output 不含 partial metadata。

### 2. partial：2 success + 1 failed

3 个 child：

- index 0 success
- index 1 failed
- index 2 success

期望：

- Map node success。
- results 长度为 3。
- index 1 为 fallback result。
- `partial_success=true`
- `failed_count=1`
- `fallback_count=1`
- `fallback_rate=0.3333`
- meta 中 `fallback_indexes=[1]`
- fallback result 带 `status=fallback`、`quality=low`、`degraded=true`

### 3. partial 幂等

同一个 failed child 事件重复触发两次：

- checkpoint done 只增加一次。
- warnings 只保留一条或按 index 去重。

### 4. 已有 success 不被 fallback 覆盖

index 已有 success result 后，child failed 事件迟到：

- 保留 success result。
- 不写 fallback。

### 5. augment_images_multi 集成

模拟 `augment_images_multi`：

- 2 个 image_to_image child success
- 1 个参数错误 child failed

期望：

- `augment_images_multi` success(partial)
- `merge_augmented_images` 可以执行
- 成功增强结果被使用
- 失败 item 使用原图 fallback

### 6. fallback ratio 阈值

3 个 child 全部 failed，`max_fallback_ratio=0.5`：

- Phase 1：输出 `fallback_rate=1.0`，保留 warning。
- Phase 2：Map failed 或要求用户重试。

## 风险

1. fallback result 字段不兼容下游。

   缓解：先审计 `merge_augmented_images` 消费字段，按需补齐 `primary_file_url` / `image_url` / `preview_url`。

2. partial success 掩盖严重错误。

   缓解：只对显式配置 partial 的可选增强节点启用，默认 fail_fast；输出 `partial_success`、`fallback_rate`、`quality`。

3. checkpoint done 计数错误。

   缓解：以 index 为幂等 key，写入前检查 result 是否已存在。

4. 子任务 failed 但父任务 success 造成审计困惑。

   缓解：Map output 暴露 `partial_success`、`warnings`、`failed_count`、`fallback_count`。

5. 成功率指标虚高。

   缓解：增加 partial / fallback / high quality 指标，区分 task success 和 high quality success。

## 分阶段计划

### Phase 1

- Map 支持 `failure_policy=partial`。
- partial child failed 直接写 fallback。
- event listener 对 partial Map 不 permanent fail parent。
- augment_images_multi 启用 partial。
- 输出 partial metadata。
- 输出 result status / quality / degraded。
- 输出 fallback indexes 和 fallback rate。
- 覆盖核心测试。

### Phase 2

- 支持 per-index `max_child_retries`。
- 支持 non-retryable error 分类。
- 支持更多 fallback source。
- UI 展示 partial warnings。
- 启用 `max_fallback_ratio` 阈值决策。
- 建立 workflow quality metrics。

## 结论

`augment_images_multi` 是可选增强节点，应该使用 partial success 语义，而不是 all-or-nothing。Map 节点增加 `failure_policy=partial` 后，可以保留成功子任务结果，并对失败 item 使用原始素材 fallback，避免成功增强结果被浪费，也避免父任务因可选增强失败而整体失败。

同时，partial success 不能停留在“让任务成功”这一层。它必须配套 result semantics、quality tier、fallback metrics 和 decision layer，否则系统会出现成功率虚高但结果质量不可控的问题。最终目标是形成一套可商用的 AI Workflow 弹性执行模型：有韧性，也有质量边界。
