# 📐 `plan_workflow` 媒体剪辑需求 — 实现对照评估

> 评估日期：2026-06-29 | 对照需求：[flux-plan_workflow-feature-request.md](./flux-plan_workflow-feature-request.md)
> 基线：当前 `main` 分支实现

---

## ⚠️ 前置发现：shell 工具未接入生产 MCP server

需求文档默认 `plan_workflow` 能执行 ffmpeg 这类 shell 命令，但生产 MCP server
[`cmd/flux-mcp/main.go`](../../cmd/flux-mcp/main.go) 只注册了三个工具：

```go
reg.Register(builtin.NewMergeResultTool())
reg.Register(builtin.NewWriteFileTool(dir))
reg.Register(builtin.NewCompileTool(dir))
```

`ShellTool`（[`tool/builtin/shell_tool.go`](../../tool/builtin/shell_tool.go)）已实现，但全仓没有任何
`reg.Register(builtin.NewShellTool(...))`。**因此视频剪辑场景目前端到端跑不通**——三个需求
全部建立在 shell 节点之上，必须先把 shell 工具暴露出去。

---

## 需求 1（P0）可配置超时 — 机制已存在，差「接线 + 引导」

**核对结论：不是缺能力，是缺 prompt 引导 + 默认值偏低。**

- **调度器层无超时**：[`runtime/scheduler.go`](../../runtime/scheduler.go) 的 `execNode` 直接把
  `ctx` 透传给工具，不设 deadline。
- 文档观察到的「硬 60 秒」完全来自 `ShellTool.defaultTimeout = 60 * time.Second`。
- **shell 工具早已支持 `timeout_seconds` 入参**，而 planner 节点 `arguments` 是自由 JSON，
  本就能写 `{"command": "ffmpeg ...", "timeout_seconds": 300}`。
- 工具 catalog（`toolInputSummary`）已自动把 `timeout_seconds(integer)` 列给 LLM 看，
  但 DAGPlanner 的 system prompt（[`planner/dag_planner.go`](../../planner/dag_planner.go)）从未
  提示何时该设置它，所以 LLM 不会主动用。

落地：
1. 在 DAGPlanner system prompt 增加一段超时引导（长命令显式给 `timeout_seconds`）。
2. 把 shell 默认超时提高（如 300s），适配媒体/长任务。

工作量：约半天。

---

## 需求 2（P1）文件产物一等公民 — 路径绕过已可用，真 file-ports 是新活

**核对结论：优先级判断准确，现状可绕过、体验打折。**

- 节点间数据流靠 `$from` 引用（`resolveRefs`），做的是上游 output 字段的**整值替换**，
  已能把上游产出的文件路径字符串喂给下游命令。
- 文档自己提的绕过方案（写盘 → stdout 传路径 → 下游 `$from` 取路径）用现有机制即可干净实现。
- 真正缺的是声明式 `outputs/inputs` + 中间产物自动清理 = 净新增（nodeSpec 加字段 +
  运行时文件生命周期管理）。
- ⚠️ 仓库里的 [`runtime/file_ports.go`](../../runtime/file_ports.go) 是 crash 恢复用的
  `FileStore`，**与文件传递无关**，勿被命名误导。

工作量（真 file-ports）：约 2-3 天。

---

## 需求 3（P2）硬件加速感知 — 注入 env 即可

**核对结论：纯增量，判断准确。**

- `ShellTool` 用 `exec.Command("sh","-c",cmd)` 且**未设 `cmd.Env`**，子进程默认继承父进程环境。
- 「方式 A（注入 `$FLUX_HWACCEL`）」只需 `cmd.Env = append(os.Environ(), "FLUX_HWACCEL=...")`
  外加一次性 GPU 探测（macOS `uname -m` / `sysctl` 判 Apple Silicon）。

工作量：约 1 天。

---

## 总体评估与落地顺序

| 需求 | 文档定级 | 核对 | 实际差距 |
|---|---|---|---|
| 0（隐含）shell 未接入 | 未提 | 真正拦路石 | 必须先 `Register(NewShellTool)` |
| 1 可配置超时 | P0 | 同意 | 机制已有，缺 prompt 引导 + 默认值，~半天 |
| 2 文件传递 | P1 | 同意 | `$from` 路径绕过即可用；真 file-ports 新活 ~2-3 天 |
| 3 硬件加速 | P2 | 同意 | 注入 `cmd.Env` + GPU 探测，~1 天 |

**建议顺序**：shell 接入 → 需求 1 prompt 引导（投入产出比最高，直接解锁长任务）→
需求 3（小且独立）→ 需求 2 真 file-ports（最大，可先用 `$from` 路径方案顶着）。

---

## 本次落地范围

本次提交完成前两项：

1. **shell 接入**：在 `cmd/flux-mcp/main.go` 注册 `ShellTool`。
2. **需求 1 prompt 引导**：
   - DAGPlanner system prompt 增加 `timeout_seconds` 使用引导。
   - `ShellTool` 默认超时由 60s 提升至 300s，适配媒体/长任务。
