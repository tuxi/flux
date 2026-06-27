# Flux v3 — Development Plan

> 基于 [architecture-v3.md](architecture-v3.md) 的实施计划。
> 关键约束：V3-M0.5 到 V3-M4 全部可以在 DreamAI 当前架构（flux 作为嵌入式库）中完成，不等待 code-agent 多租户升级。

---

## Phase 0: Store 接口定义 + Adapter（V3-M0.5）

**目标**：将 flux 的数据访问从 engine 内部实现解耦为接口 + adapter 模式。

**为什么排第一**：这是所有后续步骤的基础。Store 接口定义好后，LLMPlanner 删除、MCP 暴露、DreamAI PG 接入都有了统一的持久化边界。

### 0.1 — 定义 WorkflowStore 接口

新建 `flux/store/workflow_store.go`（或放在 `flux/runtime/` 内，取决于是否需要独立包）：

```go
package store

type WorkflowStore interface {
    CreateRun(ctx context.Context, meta RunMeta) (string, error)
    LoadRun(ctx context.Context, runID string) (*WorkflowRun, error)

    CreateTask(ctx context.Context, runID string, meta TaskMeta) (string, error)
    LoadTask(ctx context.Context, taskID string) (*Task, error)

    UpdateNodeState(ctx context.Context, taskID string, nodeName string, state NodeState, output map[string]any) error
    LoadNodeStates(ctx context.Context, taskID string) (map[string]NodeState, error)

    SavePlan(ctx context.Context, taskID string, plan *Plan) error
    LoadPlan(ctx context.Context, taskID string) (*Plan, error)
}
```

关键决策点：
- `Plan` 类型来自 `runtime` 包。Store 可以依赖 `runtime.Plan`（单向，runtime 不依赖 store）
- `NodeState` 类型已在 `runtime/state.go` 中定义，复用即可

### 0.2 — 定义 AwaitStore 接口

```go
type AwaitStore interface {
    CreateBinding(ctx context.Context, binding AwaitBinding) error
    ResolveBinding(ctx context.Context, bindingID string) (claimed bool, err error)
    FindByProviderTaskID(ctx context.Context, providerTaskID string) (*AwaitBinding, error)
    ListPending(ctx context.Context) ([]AwaitBinding, error)
}
```

### 0.3 — 定义 TraceStore 接口

```go
type TraceStore interface {
    AppendTrace(ctx context.Context, taskID string, events []TraceEvent) error
    ReplayTrace(ctx context.Context, taskID string, sinceSeq int64) ([]TraceEvent, error)
}
```

### 0.4 — 实现 Memory Adapter（测试用）

`flux/adapter/memory/workflow_store.go`
`flux/adapter/memory/await_store.go`
`flux/adapter/memory/trace_store.go`

纯内存实现，用于单元测试和集成测试。

### 0.5 — 实现 SQLite Adapter

`flux/adapter/sqlite/workflow_store.go`
`flux/adapter/sqlite/await_store.go`
`flux/adapter/sqlite/trace_store.go`

基于现有 engine 中的 SQLite 访问代码提取。使用 `modernc.org/sqlite`（纯 Go，无 cgo）。

### 0.6 — 重构 Engine 接收 Store 接口

修改 `engine.go` 的 `Config`：

```go
type Config struct {
    Backend       Backend        // 保留兼容
    WorkflowStore store.WorkflowStore  // 新增
    AwaitStore    store.AwaitStore     // 新增
    TraceStore    store.TraceStore     // 新增（可选）
}
```

`Backend` 接口保留但标记为 deprecated，内部默认适配到 Store 接口。

### 0.7 — 验证

- [ ] 同一套 Engine 代码，注入 Memory adapter → 集成测试通过
- [ ] 同一套 Engine 代码，注入 SQLite adapter → 行为一致
- [ ] B-M0 (async_hello)：AwaitBinding 通过 AwaitStore 跨 Turn 持久化

**预计工作量**：3-5 天
**风险**：低。接口定义清晰，现有代码提取为 adapter 是纯重构。
**对 DreamAI 的影响**：零。DreamAI 继续用现有 Backend。Store 接口是新增可选路径。

---

## Phase 1: LLMPlanner 删除（V3-M1）

**目标**：删除 flux 中所有 Agent Runtime 职责的代码。

### 1.1 — 删除文件

```
planner/llm_planner.go          # 增量 control loop
planner/llm_planner_test.go     # 对应测试
planner/code_agent_test.go      # 迁移到 Agent Runtime 侧或删除
```

### 1.2 — 删除/简化

```
planner/invoker.go              # 删除 LLMPlanner 专用适配器，保留 ToolInvoker
planner/llm_planner.go          # GaveUpError, giveUpTool, NoProgressLimit
session/session.go              # 删除（Conversation 管理由 Agent Runtime 负责）
session/sqlite_store.go         # 删除
model/types.go                  # 删除 Message/ToolCall/Request/Response/Completer
model/openai_compatible.go      # 删除
cmd/flux-agent/                 # 整个目录删除
```

### 1.3 — 保留但标记

```
model/types.go                  # 保留 ToolDefinition（工具定义层用）
```

### 1.4 — 验证

- [ ] `go build ./...` 通过
- [ ] `go test ./runtime/...` 通过
- [ ] `go test ./planner/` — DAGPlanner 测试仍通过
- [ ] DreamAI 编译通过（DreamAI 从未 import LLMPlanner）
- [ ] DAGPlanner + Scheduler 独立可测试（通过 StaticSource）

**预计工作量**：1-2 天
**风险**：低。DreamAI 从未使用 LLMPlanner，删除零影响。
**对 DreamAI 的影响**：零。

---

## Phase 2: MCP 服务封装（V3-M2）

**目标**：flux 作为独立 MCP server 暴露 `plan_workflow` tool。

### 2.1 — 创建 flux-mcp-server

新建 `cmd/flux-mcp-server/main.go`：

```
flux-mcp-server
  ├── MCP stdio/SSE transport
  ├── tools/list → [plan_workflow]
  └── tools/call plan_workflow → DAGPlanner → Scheduler → 结果
```

### 2.2 — plan_workflow tool 契约

```json
{
  "name": "plan_workflow",
  "description": "给定目标和工具目录，生成并执行一个多步 Workflow DAG。",
  "inputSchema": {
    "type": "object",
    "properties": {
      "goal": {"type": "string", "description": "要完成的目标"},
      "tools": {"type": "array", "items": {"$ref": "#/definitions/tool"}},
      "max_repairs": {"type": "integer", "default": 3}
    },
    "required": ["goal", "tools"]
  }
}
```

### 2.3 — 验证

- [ ] Claude Code 通过 MCP 配置连接 flux-mcp-server
- [ ] "帮我生成一个三步骤的 pipeline" → DAGPlanner 生成 DAG → Scheduler 执行 → 返回结果
- [ ] DreamAI 可同时用模式 A（库依赖）+ 模式 B（MCP 调用）

**预计工作量**：2-3 天
**风险**：低。MCP 协议已成熟，flux 侧只需薄包装。
**对 DreamAI 的影响**：可选。不影响现有模式 A。

---

## Phase 3: flux-agent CLI 退役（V3-M4）

**目标**：正式删除独立 CLI，更新文档。

### 3.1 — 删除

```
cmd/flux-agent/                 # 整个目录
flux-agent 二进制               # 从仓库根目录删除
```

### 3.2 — 文档

- 迁移指南：flux-agent CLI 用户 → 使用 Agent Runtime CLI 或 MCP client
- README 更新

**预计工作量**：0.5 天
**风险**：低。

---

## Phase 4: PostgreSQL Adapter + DreamAI 验证（V3-M5）

**目标**：为 DreamAI 提供服务端级 Store 实现。

### 4.1 — 实现 PG Adapter

```
flux/adapter/postgres/workflow_store.go
flux/adapter/postgres/await_store.go
flux/adapter/postgres/trace_store.go
```

基于 `pgx/v5`，连接池管理。

### 4.2 — DreamAI 集成验证

- [ ] DreamAI 注入 PGWorkflowStore + PGAwaitStore
- [ ] 现有 Workflow 功能行为不变
- [ ] Async 任务（NodeAwaiting → Notify → Resume）跨进程重启正常

**预计工作量**：3-5 天
**风险**：中。PG schema 设计需要与 DreamAI 现有数据库协调。

---

## 远期（独立轨道，不阻塞 flux v3）

- code-agent ConversationStore 协议化（见 [code-agent-brief.md](code-agent-brief.md)）
- code-agent 多租户升级
- 多 Agent 场景验证（CI Agent、Research Agent 等）
- DreamAI 评估是否使用 code-agent 作为 Agent Runtime

---

## 里程碑总览

```
Phase 0  ████████  Store 接口 + Memory/SQLite adapter    3-5 天
Phase 1  ████      LLMPlanner 删除                        1-2 天
Phase 2  ██████    MCP server (flux-mcp-server)           2-3 天
Phase 3  ██        flux-agent CLI 退役                    0.5 天
Phase 4  ████████  PostgreSQL adapter + DreamAI 验证      3-5 天
────────────────────────────────────────────────────────
Total              ~10-16 天
```

所有 Phase 可在 DreamAI 当前架构（flux 作为库）中独立完成，不等待外部依赖。
