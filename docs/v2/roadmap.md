# Flux v2 路线图

> 2026-06-24 重排。架构验证阶段结束——两条主线都已生产模型级闭环。
> 现在进入**产品化取舍阶段**：不再问"能不能"，而问"值不值得深挖"。
> 核心纪律：**让真实产品来暴露真正缺的东西**，不为想象中的未来提前建设。

```
M1  双形态 planner          ✅ 已实证（type A + type B，含数据流）
M2  Productization          ← 当前：选一个真实产品，逼它暴露缺口
M3  Trace / Replay          ⏸️ 继续后移（无规模/分布式压力 = 还没痛点）
M4  MCP 生态                后续
```

---

## M1 — 双形态 planner（完成）

requirements FR1 的两种 PlanSource 策略都已用真实 LLM（deepseek）实证：

| 形态 | 机制 | 证据 |
|---|---|---|
| **类型 A** control loop | `IncrementalPlanSource`：看反馈→决定下一步 | SeededBug：看编译报错→改→过 |
| **类型 B** dataflow DAG | `StaticPlanSource`：一次生成整图 | 并行 DAG + `depends_on` + `$from` 数据流 + FR5 校验 |

同一个 kernel，只差 `Next`/`done`（FR3）。kernel 始终零 flux 依赖。

---

## M2 — Productization（当前）：第一个产品 = 代码 agent

**不是技术深化，是把 v2 收敛成真正可用的东西。**
**方法论**：建**最薄的真实代码 agent**，跑在真实的现有代码任务上，**让失败告诉我们 M2.x 里哪些真要做**——而不是把下面的清单当待办逐个建。

### 已有的杠杆（复用，不重建）
- 类型 A loop（`LLMPlanner`）✅
- **MCP filesystem server 已集成**（stage A）→ read_file / list_directory / write_file / edit_file / search_files **现成可用**
- `compile` 工具 ✅ ；`tool.DefinitionOf` 统一定义出口 ✅
- observability 的种子：`runtime.Emitter` / `ToolEmitter` 端口 ✅

### 唯一明显缺的工具
- **shell / run-command**（跑测试、跑程序）——M2.6 工具箱里目前缺的一块。

### 候选深化项（**由产品暴露后再决定做不做**，不预先建）
- **M2.1 统一 Planner**：其实**已存在** = `runtime.PlanSource`。type B 只差一个"首次 Next 时 Generate"的薄壳，对外只暴露一个 PlanSource。几十行，不是里程碑。
- **M2.2 Tool schema 升级**：`ToolDefinition` + 完整 JSON Schema + examples + annotations。这是**提升 LLM 质量**，不是架构。
- **M2.3 Tool selection**：工具一多（filesystem 14 + compile + shell…）→ 全塞 prompt → token 爆炸 → 需要 goal→retriever→top-k。**产品问题**，菜单真大了再做。
- **M2.4 kernel vs engine**（最关键，且**被产品决定**）：LLM 生成的 DAG 走新 kernel 还是老 `engine.runDAG`？
  - 老 engine 给全套 v1 特性（async/await/poll、retry、reuse/fork、cost、map/loop），**代价是它的持久化/Task 机器**。
  - **代码 agent**（同步、秒级、内存内）→ **轻 kernel 够，不需要 engine**。
  - 内容/电商 agent（async 长任务）→ **必须** engine 的持久化 await/poll。
  - 故 M2.4 是产品的下游，不在选产品前回答。
- **M2.5 HTTP/SSE observability**：让 planner 可观察（thinking/planning/executing）——是**产品 UX**，不是 trace/replay。种子已在 `Emitter` 端口，是"接出来"不是"新建"。
- **M2.6 Builtin 工具箱**：write_file / read_file / shell / http_fetch 等，形成最小 agent toolbox（read/write 已由 filesystem MCP 提供，缺 shell）。

---

## M3 — Trace / Replay / Determinism（继续后移）

trace 解决 debug / replay / fork / determinism——这些是**规模 + 长时长 + 分布式失败**催生的痛点（Temporal 那种：上万 workflow、跨天、重试、分布式失败）。**现在一个都不存在**，做 trace 大概率仍是 architecture-driven。

- ✅ Phase 1 sidecar 双写已落地（`runtime/trace.go`，默认关闭、零副作用），**停在此**。
- 何时解冻：等真实产品跑出"任务崩了需要精确重放/分叉"的实际痛点。由产品决定，不预先建。

---

## M4 — MCP 生态（后续）

consume / expose / 定义层三段已闭环（stage A/B/C）。后续：HTTP/SSE transport、resources/prompts/sampling、多模态 content 保真、把更多工具做成 MCP 服务。

---

## 残留清理（v1 遗留，择机）

`tool/poll_tool_resolution.go`、`internal/config/config.go`、`definition/node_definition.go`（注释）、`service/await_replay_service.go`、`handler/await_handler.go` 仍引用已删的视频/图像工具。不阻塞主线。
