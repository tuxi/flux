# RunInspectorHandler API 文档

路由前缀: `/runs`（注册于 `ai-engine/server/server.go:684-696`）

## Handler 列表

| # | Handler | 方法 | 路径 | 行号 |
|---|---------|------|------|------|
| 1 | `ListRuns` | GET | `/runs` | :67 |
| 2 | `GetRunInspector` | GET | `/runs/:id/inspect` | :107 |
| 3 | `GetRunDAG` | GET | `/runs/:id/dag` | :138 |
| 4 | `GetRunTimeline` | GET | `/runs/:id/timeline` | :163 |
| 5 | `GetRunNodeDetail` | GET | `/runs/:id/nodes/:node` | :212 |
| 6 | `GetRunNodeDiff` | GET | `/runs/:id/nodes/:node/diff` | :307 |
| 7 | `PatchPreview` | POST | `/runs/:id/patch-preview` | :356 |
| 8 | `RedoRun` | POST | `/runs/:id/redo` | :1356 |
| 9 | `GetRunNodeExpansion` | GET | `/runs/:id/nodes/:node/expansion` | :1468 |

## 依赖注入

通过 `NewRunInspectorHandler` 注入 9 个依赖：

```
Engine, TaskRepository, NodeRuntimeRepository, EventRepository,
AwaitBindingRepository, WorkflowRepository, WorkflowVersionRepository,
Builder, RunRedoService
```

核心方法 `loadRunBundle` (:422) 一次性加载 task → WorkflowVersion → WorkflowDefinition → 编译后的 Workflow → 所有 NodeRuntime，被多个 handler 复用。

## API 调用关系

```
/runs                           GET   → 运行列表（分页）
/runs/:id/inspect               GET   → 聚合视图（一切的主入口）
  ├─ /runs/:id/dag              GET   → 独立获取 DAG 图
  ├─ /runs/:id/timeline         GET   → 独立获取时间线
  ├─ /runs/:id/nodes/:node      GET   → 节点详情（含 diff/timeline/await）
  │   ├─ /:node/diff            GET   → 独立获取 fork parent 节点 diff
  │   └─ /:node/expansion       GET   → map/subworkflow 展开图
  ├─ /runs/:id/patch-preview    POST  → 预览 patch 执行计划
  └─ /runs/:id/redo             POST  → 创建 fork + 局部重做
```

---

## 1. GET /runs — 运行列表

查询参数: `page`, `page_size`

返回:
```json
{
  "runs": [{ "task_id": 1, "status": "success", "progress": 1, ... }],
  "total": 100
}
```

复用 `taskRepo.ListByUser` 按用户分页查询，返回 `RunSummaryDTO[]`。

## 2. GET /runs/:id/inspect — 聚合视图（主入口）

返回 `RunInspectorResp`，包含 8 个模块：

| 字段 | 类型 | 说明 |
|------|------|------|
| `run` | RunSummaryDTO | 运行摘要（状态、进度、fork 关系、时间戳） |
| `workflow` | WorkflowSummaryDTO | 工作流名称、版本 |
| `dag` | RunDAGDTO | 完整 DAG（nodes + edges + activatedEdges + stats） |
| `snapshot` | RunSnapshotDTO | 输入 & 最终输出快照 |
| `lineage` | RunLineageSummaryDTO | 血缘（base/fork 关系，ancestor/child 预留） |
| `patches` | RuntimePatchDTO[] | 运行时 patch 列表 |
| `resume` | ResumeSpecSummaryDTO | resume_from 节点 + patch 数量 |
| `await_bindings` | RunAwaitBindingDTO[] | 所有 await 绑定及其状态推断 |

## 3. GET /runs/:id/dag — DAG 图

返回 `RunDAGDTO`：

- **nodes**: 每个节点标注 state、action (execute/reuse/patch)、isDirty、isPatched、reuseKind、mapItemReuse 等
- **edges**: 条件边、case_key、label、activated 状态
- **stats**: total/success/failed/running/skipped/patched/reused/executed 节点计数
- **activatedEdges**: 实际激活的边集合

## 4. GET /runs/:id/timeline — 时间线

查询参数: `type`（可选，逗号分隔的类型前缀过滤，如 `node_,await_`）

- 默认只返回 Persistent 事件（Transient 事件走 WebSocket 实时推送）
- Phase 自动推导规则：
  - `await_replay_*` → `await_replay`
  - 含 `planned`/`patch` → `planning`
  - 含 `materialize`/`reuse` → `materialization`
  - 其他 → `execution`

## 5. GET /runs/:id/nodes/:node — 节点详情

返回 `RunNodeDetailResp`：

| 字段 | 说明 |
|------|------|
| `run` | 所属运行摘要 |
| `node` | 节点详情（resolvedInput/output/checkpoint/activatedEdges/heartbeat 等） |
| `parent` | fork parent 同名节点（仅在 fork 时存在） |
| `patches` | 该节点的 patch 列表 |
| `timeline` | 该节点的事件时间线 |
| `diff` | 与 fork parent 同名节点的 input/output/checkpoint diff |
| `await_binding` | 该节点的 await 绑定（含 statusCategory/nextAction 推断） |

## 6. GET /runs/:id/nodes/:node/diff — 节点 Diff

返回 `RunNodeDiffDTO`，对比当前节点与 fork parent 同名节点：

- `input_diff` / `output_diff` / `checkpoint_diff` — field-level 差异
- 使用 flattenMap 递归展平嵌套对象，按路径做 added/removed/modified 对比
- 值相等性用 JSON marshal 后字符串比较

## 7. POST /runs/:id/patch-preview — Patch 预览

请求体：
```json
{
  "resume_from": "node_name",
  "patches": [{ "target": "runtime_state", "node": "x", "path": "output.url", "op": "replace", "value": "..." }],
  "override_input": {}
}
```

返回 `PatchPreviewResp`：
```json
{
  "valid": true,
  "message": "ok",
  "run_plan": {
    "mode": "fork",
    "resume_from": "node_name",
    "summary": { "execute_count": 3, "reuse_count": 2, "patch_count": 1, "resume_boundary_count": 1 },
    "nodes": [{ "name": "x", "action": "patch", "reason": "...", "is_patched": true }]
  }
}
```

调用 `engine.PreviewRunPlan()` 生成预览计划。

## 8. POST /runs/:id/redo — 重做

请求体：
```json
{
  "resume_from": "node_name",
  "patches": [],
  "override_input": {},
  "edit_action": "redo_partial",
  "edit_label": "fix output",
  "note": "retry after input correction"
}
```

返回：
```json
{
  "task_id": 2,
  "status": "pending",
  "parent_task_id": 1,
  "resume_from": "node_name"
}
```

调用 `redoService.RedoRun()` 创建新的 fork task，从 `resume_from` 开始局部重执行。

## 9. GET /runs/:id/nodes/:node/expansion — 节点展开图

仅支持 `map` 和 `subworkflow` 类型节点。返回 `RunNodeExpansionResp`：

**map 展开**:
- 包含虚拟 fan-out / fan-in 节点
- 每个 item 一条 lane（含 ItemContext）
- 分支感知：识别 condition fork → 每个 branch 独立复制 → merge boundary 汇合
- item 状态从 checkpoint 中 `item_states` 和 `reused_items` 推断

**subworkflow 展开**:
- 展示子工作流的完整 DAG 定义图
- 节点状态继承自父节点 runtime

---

## AwaitBinding 推断逻辑

`toRunAwaitBindingDTO` (:501) 在转换时自动计算以下辅助字段：

| 字段 | 推断规则 |
|------|---------|
| `status_category` | pending/waiting/completing → `active`；completed → `success`；failed/timed_out → `error`；canceled → `neutral` |
| `status_label` | 枚举直译：Pending/Waiting/Completing/Completed/Failed/Timed Out/Canceled |
| `waiting_for` | 根据 source 拼接：`webhook:<provider>` / `signal:<name>` / `message:<name>` / `poll:<tool>` |
| `next_action` | 根据 status + timeout + poll 状态推断：activate_wait/poll_due/timeout_due/wait_signal/wait_webhook/resume_task/done/failed/timed_out/canceled |
| `is_terminal` | completed / failed / timed_out / canceled |
| `poll_summary.is_due` | nextPollAt 已过且状态为 waiting |
| `poll_summary.has_capacity` | MaxPollAttempts <= 0（无限制）或当前 attempts < max |

## Patch 操作

Patch 通过 `domain.RuntimePatch` 表示，字段：

- `target`: 目标类型（如 `runtime_state`）
- `node`: 目标节点名
- `path`: JSON 路径
- `op`: 操作（replace/add/remove）
- `value`: 新值
- `label`: 可读标签
