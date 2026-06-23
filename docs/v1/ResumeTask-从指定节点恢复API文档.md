# ResumeTask — 从指定节点恢复 API 文档

## 概述

`POST /api/v1/user/works/resume` 原用于手动恢复已失败/暂停/取消的任务。现在扩展支持**从指定节点开始恢复**（`resume_from`），不想从当前失败节点恢复时可以从上游某个节点重新执行，**不创建新任务**，原地重跑。

与 Fork/Redo 的区别：

| | Fork/Redo | ResumeTask（新） |
|---|---|---|
| 创建新任务 | ✅ 新建 fork task | ❌ 原地重跑 |
| 产生新 task_id | ✅ | ❌ 复用原 task_id |
| 可用状态 | terminal / suspended | failed / suspended / canceled |
| 上游节点复用 | planning 阶段自动判断 | 不重置 = 自动复用 |
| patch 支持 | ✅ | ✅ |
| 适用场景 | 完成后想局部修改 | 失败后想从上游重新开始 |

## API

### POST /api/v1/user/works/resume

#### 请求体

```json
{
  "task_id": 2053069726698979328,
  "resume_from": "validate_shot_plan",
  "patches": [
    {
      "target": "node_output",
      "node": "parse_intent",
      "path": "intent",
      "op": "set",
      "value": { "scene": "海边日落", "style": "电影感" },
      "label": "修正意图理解结果"
    }
  ]
}
```

字段说明：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `task_id` | int64 | ✅ | 要恢复的任务 ID |
| `resume_from` | string | ❌ | 从哪个节点开始重新执行。省略则沿用原有逻辑（从失败/未完成节点恢复） |
| `patches` | array | ❌ | 修正上游节点 output 的 patch 列表，结构同 fork API |

`patches[].target` 仅支持 `"node_output"`（resume 场景不需要修改 checkpoint）。

#### 响应体

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "task_id": 2053069726698979328,
    "status": "pending",
    "resume_from": "validate_shot_plan"
  }
}
```

#### 错误响应

```json
{
  "code": -1,
  "msg": "resume_from node not found in workflow: unknown_node",
  "data": null
}
```

常见错误：

| 错误信息 | 原因 |
|---------|------|
| `task is not retryable` | 任务状态不是 failed / suspended / canceled |
| `resume_from node not found in workflow` | 指定的节点名在工作流定义中不存在 |
| `resume_from node has no runtime` | 节点尚未初始化（任务未执行到该节点） |
| `task is being retried` | 任务正在被 worker 处理中（30 秒内有心跳） |

## 使用场景

### 场景 1：从上游节点重跑（不修改任何输出）

任务在 `generate_creative_brief` 失败，但问题根因是更上游的 `build_visual_profile` 输出了错误数据。直接重试 `generate_creative_brief` 会再次失败。此时从 `build_visual_profile` 开始恢复：

```bash
curl -X POST /api/v1/user/works/resume \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "task_id": 2053069726698979328,
    "resume_from": "build_visual_profile"
  }'
```

效果：
- `build_visual_profile` 及之前的所有节点保持 success，不被重置
- `build_visual_profile` 及所有下游节点重置为 pending，重新执行
- 不创建新任务，不产生新扣费记录

### 场景 2：修正上游输出 + 从中间节点重跑

`generate_creative_brief` 的输出有问题，先 patch 修正它的 output，再从下游 `build_shot_execution_policy` 开始重跑：

```bash
curl -X POST /api/v1/user/works/resume \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "task_id": 2053069726698979328,
    "resume_from": "build_shot_execution_policy",
    "patches": [
      {
        "target": "node_output",
        "node": "generate_creative_brief",
        "path": "creative_brief.narrative_beats[0].description",
        "op": "set",
        "value": "一个女孩走在海边，夕阳洒在沙滩上",
        "label": "修正节拍描述"
      }
    ]
  }'
```

效果：
- `generate_creative_brief` 的 output 被 patch 修正（状态保持 success，不会重跑）
- `build_shot_execution_policy` 及所有下游节点重置为 pending，重新执行
- 下游节点的 `input_mapping` 表达式会读到 patch 后的新值

### 场景 3：只 patch 不指定 resume_from（沿用原有行为）

```bash
curl -X POST /api/v1/user/works/resume \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "task_id": 2053069726698979328,
    "patches": [
      {
        "target": "node_output",
        "node": "build_visual_profile",
        "path": "visual_profile.style",
        "op": "set",
        "value": " cinematic",
        "label": "修正风格"
      }
    ]
  }'
```

此时不指定 `resume_from`，系统会按原有逻辑找失败/未完成的节点作为重试根，同时应用 patches。适用于需要修正某个上游节点输出后继续从断点恢复的场景。

## Patch Path 规则

与 fork API 一致：
- `[0]` bracket 数字 = 数组索引
- `.0` 点号后数字 = 字符串 key "0"（不是数组索引）
- `["key"]` bracket 字符串 = 显式指定字符串 key

```
正确: "results[2].caption"      → output["results"][2]["caption"]
错误: "results.2.caption"        → output["results"]["2"]["caption"] (2 被当成 key)
```

## 与 Fork/Redo 的选择建议

| 场景 | 推荐 |
|------|------|
| 任务失败，从断点恢复即可 | `resume` 不传 resume_from |
| 任务失败，断点无法恢复，需从上游重试 | `resume` + resume_from |
| 任务成功，想修改某个节点后看效果 | `fork` + resume_from |
| 任务成功，想对比修改前后的结果 | `fork`（保留原任务做对比）|
