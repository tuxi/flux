# V4 Code Migration + Code-Agent Integration Roadmap

## Phase 1: V4 — DreamAI 代码移出 Flux

### 移走（20 个目录/文件 → `dream-ai/ai-engine/` 下已有对应位置）

| Flux 当前位置 | DreamAI 目标位置 | 说明 |
|---|---|---|
| `engine/` | `ai-engine/engine/` | v1 engine（核心，最复杂） |
| `handler/` | `ai-engine/handler/` | HTTP handlers |
| `server/` | `ai-engine/server/` | gin server（已大部分在 dream-ai） |
| `service/` | `ai-engine/service/` | 业务服务 |
| `worker/` | `ai-engine/worker/` | 异步 worker |
| `domain/` | `ai-engine/domain/` | GORM domain 类型 |
| `repository/` + `query/` | `ai-engine/repository/` | DB 接口 + GORM 实现 |
| `websocket/` | `ai-engine/websocket/` | WS hub |
| `workflow/nodes/` | `ai-engine/workflow/nodes/` | v1 node step 实现 |
| `eventbus/` | `ai-engine/eventbus/` | 事件总线 |
| `cmd/flux-server/` | `cmd/server/` | DreamAI 启动入口 |
| `adapters/dreamai/` | `ai-engine/adapters/dreamai/` | DreamAI 适配器 |
| `registry/` | `ai-engine/registry/` | 工作流注册 |
| `cost/` | `ai-engine/cost/` | 成本追踪 |
| `runtimekeys/` | `ai-engine/runtimekeys/` | 运行时 key |
| `internal/` | `internal/` | 内部配置（已部分在 dream-ai） |
| `demo/` | `ai-engine/demo/` | 演示工作流 |
| `pkg/` | `ai-engine/pkg/` 或 `pkg/` | 公共库 |
| `dto/` | `ai-engine/dto/` | 数据传输对象 |
| `workflow/` (除 compile.go) | `ai-engine/workflow/` | v1 workflow builder 等 |

### 保留在 Flux（纯内核）

```
flux/
  runtime/           ← Plan 执行内核
  store/             ← 持久化端口
  planner/           ← DAGPlanner + SpecToWorkflow
  tool/              ← 工具系统
  adapter/
    memory/          ← 测试适配器
    postgres/        ← PG 适配器
  workflow/
    compile.go       ← DSL 编译器（留！）
  model/             ← LLM provider 类型
  definition/        ← WorkflowDefinition 类型（留！DSL 编译器依赖）
  cmd/flux-mcp-server/
  config.go          ← v3 公开 API
  engine.go          ← v3 引擎入口
  internal.go        ← 内部适配器
  workflow_tool.go   ← FluxWorkflowTool
```

### 实施步骤

**V4-M1: workflow/compile.go 留在 flux**
- `workflow/` 下的 `compile.go` 是 DSL 编译器，零 DreamAI 依赖
- 其余 builder/factory 文件移走

**V4-M2: definition/ 留在 flux**
- `WorkflowDefinition`、`NodeDefinition`、`EdgeDefinition` 是 DSL 的核心类型
- 移走后 dream-ai import 这些类型

**V4-M3: 批量移动**
- 其余 18 个目录/文件整体移动到 dream-ai
- dream-ai 的 go.mod 已有 `replace flux => ...`，移动后 import 路径从 `flux/xxx` 变为 `dream-ai/ai-engine/xxx`

**V4-M4: 构建验证**
- flux: `go build ./...` 只编译内核
- dream-ai: `go build ./...` 编译完整服务

---

## Phase 2: Code-Agent 集成

V4 完成后，flux 是一个干净的独立 Go module。code-agent 可以直接 import 它。

### 集成路径

```
code-agent (Agent Runtime)
  │
  ├─ MCP 模式 (已验证)
  │   code-agent → MCP client → flux-mcp-server → plan_workflow
  │   适用：快速集成，任何 MCP Agent 通用
  │
  ├─ Tool 嵌入模式 (AgentKit 场景)
  │   code-agent import "flux"
  │   → FluxWorkflowTool → code-agent tools.Registry
  │   适用：低延迟，事件桥接，共享 provider
  │
  └─ DreamAI 场景
      code-agent (Agent Runtime) → POST /plan-workflow → v1 engine
      或者：code-agent import flux → plan_workflow tool → v1 engine
```

### 集成步骤

**C1: code-agent import flux**
- go.mod 加 `require flux v3.x`
- 验证：`FluxWorkflowTool` 可被 code-agent 注册

**C2: code-agent 的 Agent Loop 调 plan_workflow**
- 注册 `FluxWorkflowTool` 到 code-agent 的工具注册表
- LLM 看到 `plan_workflow` 工具 → 可自主调用

**C3: 事件桥接**
- flux `TraceSink` → code-agent `Emitter`
- Agent 实时看到 DAG 执行进度

**C4: Store 接口打通**
- code-agent `ConversationStore` → DreamAI PG adapter（已协议化）
- flux `WorkflowStore` → DreamAI PG adapter（已完成）

---

## 最终架构

```
┌─────────────────────────────────────────┐
│  AgentKit UI (iOS / macOS)              │
├─────────────────────────────────────────┤
│  code-agent (Agent Runtime)             │  ← 独立仓库
│  Conversation / Turn / Streaming / WS   │
├─────────────────────────────────────────┤
│  Tool Layer                             │
│  ├── search / filesystem / shell / MCP  │
│  ├── FluxWorkflowTool (plan_workflow)   │  ← import "flux"
│  └── DreamAI tools (50+ Provider)       │
├─────────────────────────────────────────┤
│  Flux (Workflow Engine)                 │  ← 独立仓库
│  DAGPlanner / Scheduler / Store / Trace │
├─────────────────────────────────────────┤
│  DreamAI (v1 engine + 业务)            │  ← 独立仓库
│  Task Queue / Poll Worker / AwaitBinding│
└─────────────────────────────────────────┘
```
