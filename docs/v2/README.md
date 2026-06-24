# Flux v2 — Tool-First Agent Execution Runtime

> 本目录是 Flux v2 的设计锚点。v2 的目标不是"重构 engine"，而是**把 Flux 从"工作流引擎"重新定位成"工具优先的 agent 执行运行时"**。
>
> 写作时间：2026-06。旧 v2 文档已废弃删除，本目录为重写。

---

## 一句话

> 不再由人手写工作流定义（DAG），而是让 **LLM planner 看着工具清单自己决定调用顺序**，由一个**与编排方式解耦的执行内核（kernel）**执行；工具本身都是 **MCP 服务**，像 Claude Code / Codex 那样注册进 Flux。

---

## 两条雷打不动的主线

这两条是 v2 的地基，后续所有设计都不得与之冲突。

### 🟢 主线一：Agent 规划取代手写 workflow

- v1：人写 `WorkflowDefinition`（节点 + 边 + input_mapping + 条件），engine 解释这张图。
- **v2：LLM planner 根据目标 + 工具清单 + 中间结果，运行时决定下一步调哪个工具。** "编排"从"人写的静态图"变成"planner 推理的产物"。
- engine 不删，而是**降级**：从"WorkflowDefinition 解释器" → "plan/agent 的执行底座（kernel）"。它最难、最值钱的能力（依赖求解、async/await、retry、确定性/回放/分叉）全部保留，被剥离的只是"人写图"这层语义。

### 🔵 主线二：工具收敛到 MCP

- 已论证可行（见 [architecture.md](architecture.md) 的"工具层"）。
- **所有工具将来都做成 MCP 服务注册进 Flux**，像 Claude Code / Codex 接入 MCP 那样。不局限于任何单一领域。
- 项目已清空领域工具（视频/图像生成工具已删除），当前 `tool/builtin/` 只剩通用的 `merge_result`。这是一个**干净的、工具无关的内核**。
- 收敛策略：**定义层向 MCP 看齐**（JSON Schema / outputSchema / annotations），**执行层保持 Flux 超集**（async/await/事件），MCP 作为接缝后的一种 binding。

---

## 证明主线的 Demo（mainline proof）

一旦下面这个 demo 跑通，主线即被证明成立：

> 给一个目标——例如「给这个商品生成一段展示视频」——**没有任何手写的 WorkflowDefinition**，LLM 看着工具清单自己决定：
> `生成商品图 → 图生视频 → 完成`，
> kernel 执行，产出和手写工作流一样的结果。

这是验证"需求是否成立、是否可实现"的唯一依据和动力。

> 注：M1 阶段先用 **stub / 廉价工具** 跑通"LLM 决策 → kernel 执行 → 结果回喂"的机制，不烧真实外部接口；机制对了再指向真实的 MCP 工具。

---

## v2 与 v1 的概念差异

| | v1 | v2 |
|---|---|---|
| 编排来源 | 人写 `WorkflowDefinition` | LLM planner 运行时生成 |
| engine 角色 | 图的解释器 | 与编排解耦的执行内核（kernel） |
| 工具 | 内置领域工具（视频/图像） | MCP 服务，注册进 Flux |
| 中间表示 | `WorkflowDefinition` → `RunPlan` | `runtime.Plan`（definition-free IR） |
| 确定性/回放 | snapshot replay | event-sourcing trace（M2） |

---

## 当前状态（截至 2026-06）

- ✅ **kernel 可行性已验证**：definition-free 的 `flux/runtime` 包（`go list -deps` 证明零 `flux/*` 依赖，纯 stdlib），sync / async-suspend-resume / 真实 `WorkflowDefinition` 编译运行全部冒烟通过。
- ✅ **接缝成立**：`PlanSource` 统一"静态编译器"与"未来 planner"，老 workflow 路径靠 `StaticSource` 垫片原样跑通。
- ⏸️ **trace（确定性/回放/分叉）**：Phase 1 sidecar 双写已落地但**已暂停**——它是 M2，不是 M1。
- ⬜ **M1（LLM planner loop）**：下一步要做的核心交付物。

---

## 两种规划形态（都是一等公民）

Flux 面对两种**完全不同形态**的问题，由**同一个 kernel** 作为 `PlanSource` 的两种策略支持——**不是两个系统**：

- **类型 A — control loop**（写代码/编译/调试）：反馈控制系统，`IncrementalPlanSource`，每步看结果再决定下一步。
- **类型 B — dataflow DAG**（视频/媒体 pipeline）：数据流图，`StaticPlanSource`，一次性生成整张图。

二者唯一的区别是 `Next` 实现 + `done` 语义；底座完全共享。详见 [requirements.md](requirements.md)。

## 文档索引

- [requirements.md](requirements.md) — 功能需求（WHAT）：两种 PlanSource 策略、共享底座、各自的正确性约束、验证方式。
- [architecture.md](architecture.md) — 分层架构（HOW）、kernel 设计、Plan IR / PlanSource 接缝、工具=MCP、关键决策与理由、已验证证据。
- [session-model.md](session-model.md) — 会话模型：sessions / session_messages / tasks 统一关系、SessionStore 端口（CLI JSON ↔ 服务端 Postgres 同一形状）。
- [B-plan.md](B-plan.md) — **B 方向规划**：内容/电商异步工作流 Agent（M2.4 kernel vs engine 的答案在此）。
- [roadmap.md](roadmap.md) — 里程碑（WHEN）、关键路径、已完成与暂存清单。
