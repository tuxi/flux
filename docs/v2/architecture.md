# Flux v2 架构

本文记录 v2 的分层、内核设计、关键接缝，以及一组**不应被重新争论的决策**及其理由。

---

## 1. 核心原则

### 机制 vs 编排

> 内核（kernel）只认"机制"（依赖、并行、异步等待、状态、哈希复用、回放、分叉）；
> "编排概念"（节点类型、input_mapping 表达式、边条件、map/loop/子流程）是上层语义，会被**编译**成机制。

人写图也好、LLM planner 生成也好，最终都编译成同一个 `runtime.Plan` 喂给内核。

### state system vs event system

这是 v2 最深的一条划分：

| 系统 | 本质 | 角色 |
|---|---|---|
| `ExecState` / `NodeRuntime` | 当前状态快照（current truth） | 可 O(1) 查询，供调度 |
| trace（event log） | 历史真相演化（truth evolution） | 完整、有序、可回放，未来的唯一真源 |

二者不是替代关系：**state = event log 的物化投影（left-fold）**。这就是 event sourcing / CQRS。

---

## 2. 分层

```
        ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
前端：   │ workflow     │   │ planner      │   │ SDK / API   │
(编排)   │ 编译人写DSL   │   │ Goal→Plan    │   │ 代码直拼     │
        │ →runtime.Plan│   │ (LLM,增量)    │   │ →Plan       │
        └──────┬──────┘   └──────┬──────┘   └──────┬──────┘
               └─────────── 同一契约 ──────────────┘
                            ▼  runtime.Plan / PlanSource  ◄── 接缝
        ┌─────────────────────────────────────────────────┐
内核：   │ flux/runtime （definition-free，纯 stdlib）        │
(机制)   │  scheduler  依赖求解 / 就绪集 / 动态前沿            │
        │  executor   invoke Tool · sync/async 分叉 · retry  │
        │  await      挂起/恢复 · poll · 外部唤醒             │
        │  state      节点状态机 · ExecState                 │
        │  trace      event log（M2：确定性/回放/分叉的真源） │
        └─────────────────────────────────────────────────┘
                            ▼  端口（ports）
        ┌─────────────────────────────────────────────────┐
工具：   │ Tool registry → Invoker                          │
        │  LocalTool / MCPTool / HTTPTool / AgentTool       │
        │  （主线二：工具 = MCP 服务）                       │
        └─────────────────────────────────────────────────┘
```

**依赖方向（已验证）**：`runtime` 不 import `definition`、不 import `expr`、不 import `tool`。
`workflow.Compile` 负责把 `definition` 翻译进 `runtime`。`go list -deps ./runtime | grep flux/` 输出仅 `flux/runtime` 自身。

---

## 3. 内核接缝：`runtime.Plan` 与 `PlanSource`

### Plan IR（[runtime/plan.go](../../runtime/plan.go)）

`PlanNode` = 一次工具调用 + 纯依赖边 + 一个 `InputResolver` 回调。
- `definition.NodeType` 被退化为两个运行时原语：`Async`（是否走挂起路径）和 `Join`（依赖满足语义 all/any）。
- input_mapping 表达式 / 边条件**不进 IR**，在前端编译期解析为 `DependsOn` + `Resolve`。

### PlanSource —— 统一静态与动态（[runtime/source.go](../../runtime/source.go)）

```go
type PlanSource interface {
    Next(ctx, state) (added []*PlanNode, done bool, err error)
}
```

两种**策略**（见 [requirements.md](requirements.md) FR1–FR4），同一接口：

- **`StaticPlanSource`（类型 B / dataflow DAG）**：首次返回整图，`done=true`，无执行反馈。约束：执行前用工具 schema 校验（FR5）。
- **`IncrementalPlanSource`（类型 A / control loop）**：根据 `ExecState` 返回下一批节点，`done` 由 LLM 逐步判定，反馈驱动。约束：终止与收敛——迭代硬上限 / 预算 / 无进展检测（FR6）。
- **混合**：`Next` 返回 `[]*PlanNode`（一批、可带依赖），故"循环里每步铺一张子 DAG"天然覆盖——A/B 是连续谱，不是刚性二分。

> 这是主线一的技术支点：**同一个 `Scheduler.Run` 循环，两种策略，零分叉。** 唯一差异是 `Next` 实现 + `done` 语义；planner 只需实现 `Next`，内核把依赖求解/异步/重试全包了。

### 调度与异步（[runtime/scheduler.go](../../runtime/scheduler.go)）

- `Mode==Async` → `AwaitController.Begin` 登记等待 → 节点落 `NodeAwaiting` → 整体 `Suspended`；外部事件经 `Resume` 唤醒（对应 v1 的 [engine/executor.go:133](../../engine/executor.go) + `CompleteAwaitNode`）。
- 端口：`Invoker` / `AwaitController` / `Store` / `Emitter` / `ExecState`。内核定义接口，基础设施做适配器。

---

## 4. 工具层 = MCP（主线二）

### 收敛策略：定义向 MCP 看齐，执行保持 Flux 超集

| | 处置 |
|---|---|
| **定义层** | 向 MCP 看齐：JSON Schema 输入/输出、`structuredContent`、annotations。替换 v1 弱的 `DataSchema{Type,Required,Desc}`。MCP 自 2025-06-18 起支持 `outputSchema`，故 OutputSchema 保留并对齐。 |
| **执行层** | 保持 Flux 超集：`Invoke` + 事件流 + async/await/poll。MCP 没有 async-job 原语、没有 token 流原语、没有自定义领域事件——这些经 `_meta` 桥接。 |

### 统一抽象

```go
type Tool interface {
    Definition() ToolDefinition  // MCP 形状（name/desc/inputSchema/outputSchema/annotations）
    Invoke(ctx, call, emitter) (*Result, error)  // Flux 形状（超集）
}
```

`LocalTool` / `MCPTool` / `HTTPTool` / `AgentTool` 共享此抽象，统一进一个 registry。**MCP 是接缝后的一种 binding，不是接口本身。**

### 目标形态

所有工具做成 **MCP 服务注册进 Flux**，像 Claude Code / Codex 接入 MCP 那样。Flux 内核与工具领域彻底解耦——当前 `tool/builtin/` 仅保留通用的 `merge_result`。

---

## 5. 确定性 / 回放 / 分叉（trace，M2）

三性（可确定性 / 可回溯 / 可分叉）是 engine 的真正价值，agent 比 workflow **更**需要它（LLM 非确定性、重试、部分失败）。实现策略：

- **trace = event log，是三性的唯一真源**；fork/replay/determinism 是它的三种读法（一份日志，三个读者）。
- **trace ≠ telemetry**：回放级 trace（完整、有序、持久）必须与给人看的遥测事件流（`ToolEvent`/`TaskEvent`，可丢、瞬时）严格分开。
- **双 class，单 sequence**：execution 流（确定性，tool I/O）与 control 流（非确定性，`plan_extend` = planner 决策）共享同一条单调 `Seq`，跨流因果才可回放。
- **agent 确定性 = 记录 planner 决策**：workflow 回放只需记 tool I/O（图固定）；agent 回放必须**也**记录 `PlanSource.Next` 产出了哪些节点（`plan_extend` 事件），因为图是 LLM 生成的。
- **收敛路径**：Phase 1 sidecar 双写（已落地，[runtime/trace.go](../../runtime/trace.go)，默认关闭、零副作用）→ Phase 2 trace 成为 replay 源 → Phase 3 trace 成为唯一真源（`NodeRuntime`/`TaskEvent` 变 projection）。

---

## 6. 契约冻结状态（两次冻结，不是一次）

| 冻结**现在**（已验证、与 trace 无关） | 冻结**trace 之后**（trace 定义其形状） |
|---|---|
| `Plan` / `PlanNode`（IR） | `ExecState` ⛔ |
| `PlanSource` | `Store` ⛔ |
| `Scheduler.Run` / `Resume` 签名 | `AwaitController`（resume-keying 可能变） |
| `Invoker` | |

> 不能在 trace 之前冻结 `ExecState`/`Store`——三性需要哈希、checkpoint、事件日志，这些尚不在这两个契约里。先冻**调度面**，做 trace，再冻**状态面**。

---

## 7. 已验证证据

- `flux/runtime`：零 `flux/*` 依赖（`go list -deps`）。
- 冒烟测试：sync DAG、async suspend→resume、单序列 trace + 决策无副作用（[runtime/smoke_test.go](../../runtime/smoke_test.go)、[runtime/trace_test.go](../../runtime/trace_test.go)）。
- 真实 `WorkflowDefinition` → `workflow.Compile` → 在内核上跑通，真实 `nodes.Context` 充当 `ExecState`（[workflow/compile_test.go](../../workflow/compile_test.go)），未改动 `nodes` 包。
- `go build ./...` 通过，现有路径无回归。
