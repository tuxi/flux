# Code-Agent 集成方案

> Flux v3/v4 已完成，现在是纯 Workflow Engine。Code-Agent 如何接入 Flux。

## 现状

| 组件 | 状态 | 说明 |
|---|---|---|
| Flux 内核 | ✅ 14 包，8 测试全绿 | 纯 Workflow Engine，零 DreamAI 依赖 |
| Flux MCP Server | ✅ | 任何 MCP Agent 可调用 plan_workflow |
| FluxWorkflowTool | ✅ | Go 库嵌入模式 |
| Code-Agent ConversationStore | ✅ | 接口已协议化 |
| Code-Agent MCP Client | ✅ | 已验证可连 flux-mcp-server |
| Flux → v1 engine 桥接 | ✅ | Agent 生成的 DAG 走完整生产链路 |

## 三种集成模式

```
模式 A: MCP（已跑通）
  code-agent ──MCP──▶ flux-mcp-server ──▶ DAGPlanner + Scheduler
  改动: 零代码，配置即可
  延迟: 进程间 stdio
  适用: 快速集成、通用场景

模式 B: Tool 嵌入（Go import）
  code-agent ──import──▶ flux.WorkflowTool ──▶ DAGPlanner + Scheduler
  改动: code-agent go.mod 加 flux 依赖 + 注册 tool
  延迟: 同进程函数调用
  适用: AgentKit 低延迟场景

模式 C: DreamAI 全栈（Agent + v1 engine）
  code-agent ──HTTP──▶ DreamAI /plan-workflow ──▶ v1 engine 全链路
  改动: DreamAI 暴露端点（已完成）
  适用: 需要 Task/Node 持久化、Async Poll、崩溃恢复的生产场景
```

## 推进步骤

### C1: MCP 模式正式化（1 天）

Code-Agent 已支持 MCP client，只需配置。

**Code-Agent 侧：**

```yaml
# config.yaml
mcp:
  servers:
    - name: flux
      command: /path/to/flux-mcp-server
```

启动后 `mcp__flux__plan_workflow` 工具自动可用。Agent 说 "生成一张图片" → LLM 决定调 plan_workflow → Flux 生成并执行 DAG → 返回结果。

**验证标准**：和之前 Claude Code 测试一样，code-agent 调 plan_workflow 并行执行两个 shell 命令并 merge。

---

### C2: Tool 嵌入模式（2 天）

Code-Agent import flux，注册 FluxWorkflowTool。

**Code-Agent 侧改动：**

```go
// go.mod
require flux v4.0.0

// 注册 tool
import "flux"

wt := flux.NewWorkflowTool(flux.WorkflowToolConfig{
    Provider:   llmProvider,     // 共用 code-agent 的 LLM
    ModelName:  "deepseek-v4-pro",
    ToolReg:    myFluxToolReg,   // flux 专用工具注册表
    WFStore:    myWFStore,       // 消费者的 Store 实现
    AwaitStore: myAwaitStore,
})
agentRegistry.Register(wt)
```

**优势**：
- LLM 共享：flux 的 DAGPlanner 用 code-agent 的 provider，同一 API key
- 事件桥接：flux TraceSink → code-agent Emitter，Agent 实时看到执行进度
- 低延迟：同进程调用

---

### C3: Store 接口打通（1 天）

两边都有 Store 接口，对齐使用方式。

| Store 接口 | 定义方 | 实现方 |
|---|---|---|
| ConversationStore | code-agent | AgentKit(SQLite) / DreamAI(PG) |
| WorkflowStore | flux | AgentKit(SQLite) / DreamAI(PG) |
| AwaitStore | flux | AgentKit(SQLite) / DreamAI(PG) |

```go
// AgentKit 场景: 全部 SQLite
convStore := sqlite.NewConversationStore("~/.agentkit/sessions.db")
wfStore   := sqlite.NewWorkflowStore("~/.agentkit/workflows.db")
awaitStore := sqlite.NewAwaitStore("~/.agentkit/workflows.db")

// DreamAI 场景: 全部 PG
convStore := postgres.NewConversationStore(pool)
wfStore   := postgres.NewWorkflowStore(pool)
awaitStore := postgres.NewAwaitStore(pool)
```

---

### C4: End-to-End 验证（1 天）

```
AgentKit UI
  ↓ Agent Wire Protocol
code-agent (Agent Loop)
  ↓ plan_workflow tool (Tool 嵌入模式)
Flux (DAGPlanner + Scheduler)
  ↓ 生成 DAG + 执行
结果 → code-agent Emitter → UI 实时展示
```

**验证场景**：Agent 说 "分析这个项目并生成一个三步骤的执行计划" → DAGPlanner 生成 DAG → Scheduler 执行 → 结果返回 Agent。

---

## 三种模式的适用场景

| 场景 | 推荐模式 | 理由 |
|---|---|---|
| Claude Code 调 flux | A (MCP) | 零代码，通用 |
| Code-Agent CLI 调 flux | A 或 B | MCP 简单，Tool 低延迟 |
| AgentKit 端侧 | B (Tool 嵌入) | 同进程，共享 LLM，事件桥接 |
| DreamAI 生产 | C (全栈) | Task/Node 持久化，Async Poll，崩溃恢复 |

---

## 不做什么

- ❌ 不让 code-agent 实现 DAG 执行引擎 → flux 负责
- ❌ 不让 flux 管理 Conversation/Turn → code-agent 负责
- ❌ 不强制统一数据库 → Store 接口解耦
- ❌ 不等 DreamAI import 完全修复 → 先验证 MCP + Tool 嵌入模式
