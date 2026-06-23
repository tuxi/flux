# AI Engine Event Infrastructure V2

## 概述

将 Workflow Engine 的事件总线从"所有事件统一入库 + WebSocket 推送"的单层架构，重构为 **Transient / Persistent / Audit** 三层架构。

### 背景

旧架构下，所有节点和工具发出的事件（`tool_stream`, `tool_log`, `tool_progress`, `node_debug`, `task_started`, `scene_created` 等）走同一条链路：入库 + WebSocket 推送 + 内部订阅。这导致：

- `task_events` 表膨胀（大量 stream/log/progress 无意义持久化）
- WebSocket 消息风暴
- 客户端 replay/恢复越来越慢
- 未来 Timeline / Agent / 多模态场景下事件量爆炸

**核心问题**：没有区分"实时 UI 反馈"和"任务状态变更"。

## 三层架构

```
┌──────────────┬──────────────────────────────────┬────────────────────┐
│    等级       │            行为                   │      典型事件        │
├──────────────┼──────────────────────────────────┼────────────────────┤
│ Transient    │ WS only, 不入库, 不 replay        │ tool_stream        │
│              │ 允许丢失, 高频                     │ tool_progress      │
│              │                                   │ tool_log           │
│              │                                   │ node_debug         │
│              │                                   │ task_progress      │
├──────────────┼──────────────────────────────────┼────────────────────┤
│ Persistent   │ DB + Sequence + WS               │ task_started       │
│              │ 支持状态恢复 & Timeline 重建        │ task_succeeded     │
│              │                                   │ task_failed        │
│              │                                   │ scene_created      │
│              │                                   │ timeline_patch     │
│              │                                   │ node_complete_async│
│              │                                   │ tool_started       │
│              │                                   │ tool_completed     │
│              │                                   │ node_cost_identified│
├──────────────┼──────────────────────────────────┼────────────────────┤
│ Audit        │ DB only, 不推送 WS                 │ task_points_refunded│
│              │ 可短期存储                          │ task_points_refund_failed│
└──────────────┴──────────────────────────────────┴────────────────────┘
```

### 路由逻辑 (EventBus)

```
event.Grade = emitter 显式设置 → fallback: inferGrade(event.Type)

  ├─ Transient  → push WS
  ├─ Persistent → DB.Create(分配 sequence) + push WS
  └─ Audit      → DB.Create
```

## 核心设计决策

### 1. 工具实现零修改

所有现有 `EmitToolEvent` / `EmitNodeEvent` 调用无需改动。`inferGrade(eventType)` 根据事件类型前缀自动推断等级。

Emitter 层（`Context.EmitToolEvent`, `AsyncEmitter.EmitToolEvent`）已显式设置 Grade，`inferGrade` 仅作为 fallback。Phase 2 计划移除 fallback。

### 2. Sequence 复用 DB 自增 ID

不引入独立的 sequence 列或 Redis 计数器。`task_events.id` 作为全局递增 sequence。

客户端查询：`WHERE root_task_id = ? AND id > ?` — 在 task 维度 scope，间隙不影响。

### 3. task_progress 不入库

任务进度属于实时 UI 反馈，归入 Transient。任务最终进度通过 `tasks.progress` 字段持久化，不在 `task_events` 表里留痕迹。

### 4. Timeline/Replay 只查 Persistent

`RunInspector` 和 `WorkflowHandler` 的查询改为 `FindPersistentByTaskID`，排除 Transient 噪音。

## DB 迁移

```sql
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS grade VARCHAR(16) NOT NULL DEFAULT 'persistent';
```

存量数据全部 `persistent`，无需迁移。

## API 变更

### GET /api/v1/ai/tasks/:id/events

新增参数：

| 参数 | 说明 |
|------|------|
| `grade` | `persistent`（默认）/ `all` / `transient` / `audit` |
| `after_sequence` | 增量恢复，仅返回 `id > after_sequence` 的 Persistent 事件 |

示例：`GET /api/v1/ai/tasks/123/events?after_sequence=500`

## Phase 2 规划

| 项目 | 说明 |
|------|------|
| timeline_patch 分级 | PatchImportance: structural（入库）/ visual / ephemeral（不入库） |
| 客户端增量恢复 | Snapshot + Sequence 增量，不再全量 replay |
| 移除 inferGrade | 所有 emitter 显式设置 Grade，去掉字符串匹配 fallback |

## 改动文件

| 文件 | 改动 |
|------|------|
| `domain/task.go` | 新增 `EventGrade` 类型，`TaskEvent` 增加 `Grade` + `Sequence` 字段 |
| `domain/entity/task.go` | `TaskEventModel` 增加 `Grade` 列映射 |
| `eventbus/eventbus.go` | 按 Grade 三分流路由，`inferGrade()` 自动推断 |
| `workflow/nodes/context.go` | `EmitToolEvent/EmitNodeEvent` 设置 Grade |
| `worker/emitter.go` | `AsyncEmitter` 支持 CustomType + Grade |
| `repository/event.go` | 接口新增 `FindPersistentByTaskID` |
| `repository/query/event.go` | `Create` 回填 sequence，实现新查询 |
| `handler/workflow_handler.go` | `GetEvents` 支持 `after_sequence` + `grade` 参数 |
| `handler/run_inspector_handler.go` | Timeline 查询改用 Persistent |
