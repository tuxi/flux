# Flux v2 需求

> 本文记录 v2 的功能需求（WHAT must be true）。架构（HOW）见 [architecture.md](architecture.md)，里程碑（WHEN）见 [roadmap.md](roadmap.md)。

---

## 核心需求：一个 kernel，两种规划形态

Flux 面对的是**两种完全不同形态的问题**，它们必须由**同一个 kernel** 支持，作为 `PlanSource` 的两种**策略**——**不是两个系统**。

```
PlanSource（接缝）
  ├── StaticPlanSource      ← 类型 B：dataflow DAG
  └── IncrementalPlanSource ← 类型 A：closed-loop control
        ↓ 都喂给同一个
   Scheduler / Invoker / ExecState / async / trace   ← 完全共享，零分叉
```

| | 类型 A | 类型 B |
|---|---|---|
| 例子 | 写代码 / 编译 / 调试 | 视频生成 / 媒体 pipeline |
| 本质 | 反馈控制系统（control loop） | 数据流 DAG（dataflow DAG） |
| 步数 | 不定，依赖执行结果 | 固定，规划期已知 |
| LLM 角色 | 每步看结果再决定下一步 | 一次性生成整张图 |
| 不确定性 | 弥漫在执行期 | 关在规划期 |
| PlanSource 策略 | `IncrementalPlanSource` | `StaticPlanSource` |

---

## 功能需求

### FR1 — 两种 PlanSource 策略
kernel 必须同时托管 `StaticPlanSource`（B）与 `IncrementalPlanSource`（A），二者经同一 `PlanSource` 接口接入。

### FR2 — 共享底座不得分叉
两种策略**共享且不得各自实现**：`runtime.Plan` IR、`Scheduler`、`Invoker`、`ExecState`、async/await、trace、tool registry。

### FR3 — 差异面收敛到一点
两种策略**唯一的区别**是 `Next` 的实现 + `done` 的语义：
- `StaticPlanSource`：首次返回整图，`done=true`，无执行反馈。
- `IncrementalPlanSource`：根据观察到的 `ExecState` 返回下一批节点，`done` 由 planner（LLM）逐步判定，反馈驱动。

加新规划形态 = 写一个新 `PlanSource`，**不准动 kernel**。

### FR4 — 必须支持混合形态
A/B 是连续谱的两端，常见的是混合：**循环里每一步铺一张子 DAG**（如"并行生成 N 个候选再择优"）。`Next` 返回 `[]*PlanNode`（一批、可带依赖）已天然覆盖——不得把 A/B 建成刚性二分。

### FR5 — 类型 B 的正确性约束：执行前校验
LLM 生成的 DAG 可能引用不存在的工具、输入输出对不上、有环。因此 **B 的计划必须先用工具 schema 校验、再执行**；校验失败把错误反馈给 LLM 重生（generate → validate → repair，**规划期循环，非执行期**）。这是 MCP/JSON Schema 工具定义的核心用途之一。

### FR6 — 类型 A 的正确性约束：终止与收敛
类型 A 是控制系统，必须有**停机保证**，否则 runaway（震荡、卡死在同一错误上无限重试）：
- **迭代硬上限**：独立于"planner 说 done"的 max-iteration 上限（防 LLM 永不终止）。
- **预算上限**：token / 成本 / 时间 ceiling。
- **无进展检测**：连续若干步无实质进展时强制停止。

### FR7 — 工具经 MCP 供给（主线二）
LLM 的工具菜单来自 MCP `tools/list`（name + 描述 + inputSchema）。工具不内置、可无限扩展。菜单膨胀时需 **tool selection**（按目标筛选给 LLM 看哪些工具），防 token 成本与规划准确度劣化。

### FR8 — 两种模式的回放要求不同（M2 / trace）
- B：回放只需记录 tool 输出（图固定）。
- A：回放须**额外**记录每步 planner 决策（`plan_extend` 控制事件），因为图是 LLM 长出来的。

---

## 非目标 / 边界（防 scope 膨胀）

- **不做**完整 agent harness / memory engine。类型 A 的 loop 仅限"action/observation 累积进 prompt + 安全上限"。
- **不做**类型 B 的执行期 replan：生成 → 执行 → 失败则整体重生（仍是"生成 DAG"，非步进 loop）。
- kernel **不得**因为新增编排形态而改动（FR3 的反向陈述）。

---

## 验证（用低成本域，不用昂贵算力）

- **类型 A 验证**：写代码 → 编译成功（`write_file` + `compile`，本地/MCP，几乎零成本，编译器天然确定）。验证命题：kernel 能托管 control loop；LLM 带执行反馈驱动工具达成"可编译"目标；全程无手写 workflow。
- **类型 B 验证**：一条廉价的多步 pipeline（**不用**昂贵的视频生成）。验证命题：LLM 一次性生成合法 DAG，经 schema 校验后由 kernel 确定性执行。

> 详见 [roadmap.md](roadmap.md)：M1 先验证类型 A（loop）。
