# Code-Agent 侧需要做的事情

> 这是 Flux v3 架构收敛中，code-agent 需要配合推进的部分。
> **全部是独立项目，不阻塞 flux v3 的任何 Phase。**

---

## 背景（为什么有这个文档）

Flux 正在进行 v3 架构收敛，核心变化：

1. flux 删掉自己的 Agent Loop（`LLMPlanner`），从 "Agent Runtime" 重新定位为 "Agent 的 Workflow Engine"
2. Agent Runtime（Conversation、Turn、Streaming、Approval 等）统一由 code-agent 提供
3. flux 通过三种模式被 Agent 调用：嵌入式库、MCP 服务、Tool 嵌入
4. 两边都走 **"Runtime 定义接口，消费者提供 Adapter"** 的持久化模式

这份文档是从 flux 视角出发，对 code-agent 侧提出的建议。**不是需求文档，是架构对齐建议。**

---

## 1. 协议化 ConversationStore（核心建议）

### 1.1 当前状态

code-agent 的会话数据直接写入 SQLite：

```
agent/loop.go
  → session.Session.Messages
  → session.Store (SQLite 直写)
```

### 1.2 建议目标

将 `session.Store` 从 "SQLite 实现" 提升为 "ConversationStore 接口 + SQLite/PG/Memory adapter"：

```
agent/loop.go                    code-agent 定义接口
  → ConversationStore (interface)
       ├── SQLiteConversationStore   (AgentKit 端侧，默认)
       ├── PGConversationStore       (DreamAI 服务端)
       └── MemoryConversationStore   (测试)
                        ↑
                   消费者提供实现
```

### 1.3 建议接口

```go
// ConversationStore 是 Agent Runtime 的持久化端口。
// code-agent Runtime 只依赖此接口，不依赖 SQLite/PostgreSQL/任何具体存储。
type ConversationStore interface {
    // ── Conversation 生命周期 ──
    CreateConversation(ctx context.Context, meta ConversationMeta) (*Conversation, error)
    LoadConversation(ctx context.Context, id string) (*Conversation, error)
    ListConversations(ctx context.Context, opts ListOptions) ([]ConversationSummary, error)
    DeleteConversation(ctx context.Context, id string) error

    // ── Message / Turn ──
    AppendMessage(ctx context.Context, conversationID string, msg Message) error
    AppendMessages(ctx context.Context, conversationID string, msgs []Message) error

    // ── Compaction ──
    SaveSummary(ctx context.Context, conversationID string, summary string, droppedCount int) error

    // ── Event (append-only, 用于 replay) ──
    AppendEvent(ctx context.Context, conversationID string, ev EventRecord) error
    ReplayEvents(ctx context.Context, conversationID string, sinceSeq int64) ([]EventRecord, error)
}
```

### 1.4 为什么这样做

与 flux 的 `WorkflowStore` / `AwaitStore` 完全对称：

| | flux | code-agent（建议） |
|---|---|---|
| Runtime 定义 | `WorkflowStore` 接口 | `ConversationStore` 接口 |
| 端侧实现 | SQLite Adapter | SQLiteConversationStore |
| 服务端实现 | PG Adapter | PGConversationStore |
| 测试实现 | Memory Adapter | MemoryConversationStore |
| 谁提供实现 | 消费者（AgentKit/DreamAI） | 消费者（AgentKit/DreamAI） |

这样 DreamAI 不需要 "集成 code-agent"，而是提供自己的 `PGConversationStore` 注入到 code-agent Runtime。**数据归 DreamAI，Runtime 归 code-agent。**

### 1.5 不在当前范围

- 多租户/用户系统/Auth — 这是独立的大项目，不阻塞 flux v3
- 现有 SQLite 实现保持不变作为默认 adapter，不影响当前用户

---

## 2. 与 Flux 的关系（不需要做的事）

### 2.1 code-agent 不需要实现 Workflow Engine

以下能力属于 flux，code-agent **不应该**实现：

- ❌ DAG 执行 / 依赖求解
- ❌ Async/Await / Suspend/Resume
- ❌ Trace / Replay / 确定性回放
- ❌ Workflow DSL 编译
- ❌ Retry policy（节点级确定性重试）

当 Agent 遇到需要这些能力的任务时，调 flux 的 `plan_workflow` tool（通过 MCP 或 Tool 嵌入）。

### 2.2 code-agent 不需要绑定 DreamAI

code-agent 保持独立产品定位。DreamAI 是 code-agent 的一个消费者，不是宿主。两者通过 Store 接口对接，而非代码级耦合。

---

## 3. Tool 集成（可选，远期）

如果需要同进程级低延迟集成（AgentKit 场景），可以在 code-agent 侧实现一个 `FluxWorkflowTool`：

```go
// code-agent 侧
type FluxWorkflowTool struct {
    engine *flux.Engine
}

func (t *FluxWorkflowTool) Name() string        { return "plan_workflow" }
func (t *FluxWorkflowTool) Description() string { return "生成并执行多步 Workflow DAG" }
func (t *FluxWorkflowTool) InputSchema() json.RawMessage { /* ... */ }

func (t *FluxWorkflowTool) Execute(ctx context.Context, ec tools.ExecutionContext, input json.RawMessage) (tools.ToolResult, error) {
    // 调 flux DAGPlanner + Scheduler
}
```

注册进 code-agent 的 `tools.Registry` 后，Agent Loop 自动能调。

但这不是必需的。**MCP 模式（flux-mcp-server）对 code-agent 同样可用，且更解耦。**

---

## 4. 优先级建议

| 优先级 | 内容 | 理由 |
|---|---|---|
| **P0** | ConversationStore 接口定义 | 这是持久化协议化的第一步，后续所有 adapter 基于此 |
| **P0** | SQLite adapter（现有代码提取） | 保持向后兼容，当前用户不受影响 |
| **P1** | Memory adapter（测试用） | 让 CI/测试不依赖任何外部数据库 |
| **P2** | PG adapter | DreamAI 服务端场景 |
| **远期** | 多租户/用户系统 | 独立大项目 |

---

## 5. 与 Flux v3 的依赖关系

```
Flux v3                               Code-Agent
───────                               ──────────
Phase 0: Store 接口 (flux 侧)         ← 同期推进，互不依赖
Phase 1: LLMPlanner 删除              ← 对 code-agent 无影响
Phase 2: flux-mcp-server              ← code-agent 可立即作为 MCP client 使用
Phase 3: flux-agent CLI 退役          ← 无影响
Phase 4: PG adapter + DreamAI 验证     ← 与 code-agent PG adapter 同期讨论
```

**没有任何阻塞关系。** 两边独立推进，在 Store 接口模式上保持对称即可。
