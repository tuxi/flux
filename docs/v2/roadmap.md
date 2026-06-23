# Flux v2 路线图

## 关键路径

```
M1  LLM PlanSource + agent loop   ← 证明主线的核心交付物（先做这个）
        ↑ 需要：工具能被 LLM 读懂（schema，可先用现有的）
M2  trace / replay / determinism  ← agent 跑起来后的可靠性层
M3  MCP 工具接入 / 规模化
```

> **关键纪律：trace 是 M2，不是 M1。** 哪怕方向选了 agent，先要证明的是"LLM 真能驱动 kernel 干活"，而不是"干的活能回放"。回放是等 loop 跑起来、开始 debug agent 非确定性时才需要的快速跟进。

---

## M1 — 验证类型 A（control loop / IncrementalPlanSource）

> 用**低成本的 code→compile** 验证 loop 模式（不用昂贵视频）。类型 B（StaticPlanSource / DAG）作为并行需求，单独验证（见 requirements.md FR1、验证一节）。

**完成定义（DoD）**：给一个目标（如"写一个能编译通过的 Go 程序做 X"），无任何手写 `WorkflowDefinition`，LLM 看工具 + 历史（含上次编译报错）→ 决定下一步动作 → kernel 执行 → 结果回喂 → 循环到编译通过或触顶。验证：kernel 能托管 control loop，全程无手写 workflow。

> 类型 A 必须带停机保证（max-iteration / 预算 / 无进展检测，见 FR6）——这也是把 `Scheduler` 里 `!done` busy-wait 占位改对的地方。

要做的：

1. 用 Claude 实现一个 `PlanSource`：
   - 把工具清单（name + description + input schema）喂给 LLM；
   - LLM 看目标 + 已有结果 → 决定下一个调哪个工具 → 返回 `PlanNode`（用 tool-use）；
   - kernel 执行 → 结果回喂 → 循环，直到 LLM 判定 `done`。
2. 对接点已就绪：`PlanSource.Next` 接缝、增量返回节点、`done` 由 LLM 判定。kernel **一行不用改**（`!done` 的 busy-wait 占位也自然消失，因为 `Next` 阻塞在 LLM 调用上）。
3. 先用 **stub / 廉价工具 + `merge_result`** 跑通机制，不烧真实外部接口；机制对了再指向真实 MCP 工具。

**排序决定**：**先 loop，后 schema**。现有的 `Name/Description/InputSchema` 已够喂 LLM 跑通 loop；schema 升级成 JSON Schema/MCP（让规划更准）放到 loop 证明之后。

> 实现时使用 claude-api 技能确认模型 ID / SDK / tool-use 用法。

---

## M2 — Trace / Replay / Determinism（可靠性层）

agent 跑起来后再做。三性是一份 event log 的三种读法。

- ✅ **Phase 1（sidecar 双写）已落地**：`runtime/trace.go`，默认关闭、零依赖、零副作用，已验证单序列 + 决策无副作用。**已暂停在此**。
- ⬜ Phase 2：trace 成为 replay 源（`replay_engine` 加开关，`NodeRuntime` 降为 cache）。
- ⬜ Phase 3：trace 成为唯一真源（`NodeRuntime`/`TaskEvent` 变 projection）。

进入 M2 前的验证目标：**当前 sidecar trace 的事件粒度是否足以重建一次完整执行**（写一个只读回放原型喂 trace 重建 `ExecState`）。粒度不够现在补，比 Phase 2 切换时发现便宜得多。

---

## M3 — MCP 工具接入 / 规模化（主线二落地）

- 定义层向 MCP 看齐：`ToolDefinition` + JSON Schema + annotations，替换弱 `DataSchema`。
- `MCPTool` adapter：包装远程 `tools/call`，映射 emitter↔notifications、Result↔CallToolResult。
- 把工具做成 MCP 服务注册进 Flux（像 Claude Code / Codex）。
- 长任务经 `*_wait`/`*_poll_once` 拆分暴露给同步 MCP 客户端。

---

## 已完成 / 已暂存

| 项 | 状态 |
|---|---|
| `flux/runtime` 内核（plan/state/source/scheduler）| ✅ 已建、已验证、未 commit |
| `workflow.Compile`（definition→Plan，含真实 expr）| ✅ 已建、已验证 |
| `runtime/trace.go`（sidecar 双写）| ✅ Phase 1 已建，⏸️ 暂停 |
| 冒烟 / 集成测试 | ✅ 全绿 |
| LLM PlanSource（M1）| ⬜ 下一步 |

## 残留清理（v1 遗留）

当前仍有 v1 视频/图像工具的零散引用待清理：`tool/poll_tool_resolution.go`（`*_wait`→`*_poll_once` 别名）、`internal/config/config.go`、`definition/node_definition.go`（注释）、`service/await_replay_service.go`、`handler/await_handler.go`。不阻塞 M1，择机清除。
