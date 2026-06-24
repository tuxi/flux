# Flux

**Tool-First Agent Runtime** — LLM 规划、kernel 执行、工具即 MCP 服务。

不再手写 workflow DAG。Flux 让 LLM 看着工具清单自己决定调用顺序，由一个与编排方式解耦的内核（kernel）确定性执行。

## 两条主线

### 主线一：Agent 规划

LLM 在运行时生成编排，取代人手写 WorkflowDefinition。

| 形态 | 模式 | 场景 |
|---|---|---|
| **类型 A** — Control Loop | `IncrementalPlanSource`，每步看反馈再决定下一步 | 写代码→编译→看报错→改 |
| **类型 B** — Dataflow DAG | `StaticPlanSource`，一次性生成完整 DAG | 视频生成 pipeline |

同一个 kernel，只差 `Next`/`done` 语义（FR3）。

### 主线二：工具 = MCP 服务

所有工具做成 MCP 服务注册进 Flux。本地直做（快速）+ 远端补充（无限扩展）。

- **A consume**：Flux 调外部 MCP 工具
- **B expose**：Flux 工具被外部 MCP 客户端调用
- **C 定义层**：`ToolDefinition` + JSON Schema 统一出口

## 快速开始

### 代码 agent（第一个产品）

```bash
export LLM_API_KEY="your-key"
go run ./cmd/flux-agent "帮我分析一下 eventbus 的实现"
```

详见 [flux-agent README](cmd/flux-agent/README.md)。

### 内核

```go
import "flux/runtime"
```
[`flux/runtime`](runtime/) 是只依赖 stdlib 的执行内核：Plan IR、依赖求解、async/await、事件流。不 import 任何 Flux 包。

## 架构

```
编排层    workflow compiler / LLM planner / SDK
              ↓  runtime.Plan / PlanSource 接缝
内核      flux/runtime（definition-free，纯 stdlib）
              ↓  ports（Invoker / AwaitController / Store / Emitter）
工具层    LocalTool / MCPTool / HTTPTool
```

详见 [docs/v2](docs/v2/)。

## 文档

- [v2 概览](docs/v2/README.md)
- [架构](docs/v2/architecture.md)
- [需求](docs/v2/requirements.md)
- [路线图](docs/v2/roadmap.md)
- [会话模型](docs/v2/session-model.md)
- [代码 agent](cmd/flux-agent/README.md)
