# B 方向：异步长任务 Agent

> 写作时间：2026-06-24。A（代码 agent）已闭环，按计划切 B。
> 2026-06-24 修订：B 的核心不是视频，是 async。不要让视频来验证 async——让 async 自己验证 async。

---

## 0. 核心原则

**B 不验证"视频生成能不能做成"。B 验证的是这条链路：**

```
submit
  ↓
await（NodeAwaiting + AwaitBinding）
  ↓
进程退出
  ↓
重新启动
  ↓
poll / callback
  ↓
resume（CompleteAwaitNode）
  ↓
继续执行
```

**如果这个闭环成立：视频成立、图像成立、长文成立、TTS 成立、电商成立。**
**等 suspend→resume 被证明以后，再接 Provider。失败时你知道坏的是 async 层还是 provider 层——而不是两者混在一起。**

---

## 1. B 与 A 的本质差异

| | A（代码 agent） | B（异步 agent） |
|---|---|---|
| 任务时长 | 秒级 | **分钟～小时**（外部 API 异步返回） |
| 执行模型 | 同步，内存内 | **suspend→persist→resume**，可跨进程重启 |
| 工具来源 | 本地 + MCP 文件系统 | **外部 Provider**（图片/视频/LLM API） |
| 内核 | 轻 kernel（纯内存） | 轻 kernel + **engine async layer**（持久化 await/poll/callback） |
| 上下文 | 代码文件（确定性） | **外部 task_id、媒体 URL**（非确定性） |
| 规划形态 | 纯 control loop（A） | **混合**：control loop + 并行 DAG 扇出（FR4） |

**A 证明了"内核能托管 LLM 同步规划"。B 要回答："内核 + engine async layer 能托管异步长任务"。**

---

## 2. B-M0：async 地基（第一个 MVP，不是视频）

### 2.1 验证目标

> 一个 stub async 工具（`async_hello`），走完 **suspend → persist → resume** 的完整闭环。不接任何外部 Provider。

```
async_hello
  ↓ submit 返回 task_id
  ↓ NodeAwaiting
  ↓ WorkflowSuspendedError
  ↓ 进程退出 / 整体挂起
  ↓ worker 恢复 / poll worker 扫描
  ↓ CompleteAwaitNode
  ↓ Resume
  ↓ hello result
```

**验证通过标准**：`Scheduler.Run()` 遇到 `WorkflowSuspendedError` 后正确挂起；外部恢复后 `Resume` 正确继续执行；全程跨一次进程重启（或模拟重启）。

### 2.2 为什么这是整个 B 的地基

- 如果 B-M0 不通，后面所有 Provider 都没意义
- B-M0 失败时，你知道坏的是 async 层——而不是"是 async 坏了还是 Provider 坏了"
- 这和我们一路走过来的方法完全一致：**先验证机制，再烧钱**

---

## 3. B 的里程碑（重排）

```
B-M0  async_hello          ← 当前：只验证 await→persist→resume，不碰 Provider
B-M1  接一个真实 Provider    ← 最便宜的图片生成 API，验证 submit→poll→resume
B-M2  image→video 完整链路   ← 端到端：analyze→generate_image→image_to_video→publish
B-M3  成本 / session UI      ← cost 是优化，不是闭环——放在 MVP 之后
B-M4  map fanout（FR4）      ← 多候选并行生成 → 择优，首次触碰 FR4
```

> trace 继续冻结。没有规模/分布式压力 = 没有 replay 痛点。

---

## 4. 核心需求

### FR-B1 — 异步长任务（地基，最优先）

**B 的任务不可在纯内存 kernel 里跑。** 一个外部 API 调用可能 5 分钟才回调——纯内存方案进程重启就丢状态。

→ **必须用老 engine 的持久化 async 层**：
- `node.State = NodeAwaiting`（挂起，退出心跳扫描）
- `AwaitBinding`（持久化等待凭证：provider_task_id + poll/callback 配置）
- `CompleteAwaitNode`（外部事件/轮询到达后恢复执行）
- `WorkflowSuspendedError`（整体挂起，等外部唤醒后 `Resume`）

### FR-B1.1 — kernel 可能需要改（先验证，再决定）

A 从未真正碰过 `Scheduler.Run()` 遇到 `WorkflowSuspendedError` 时的语义。未来可能需要扩展 `RunResult`：

```go
type RunResult struct {
    Status        RunStatus
    Suspended     bool
    AwaitBindings []AwaitBinding
}
```

**不要在 B-M0 跑通之前宣布"kernel 不需要改"。让 async_hello 先跑起来，再决定。**

### FR-B2 — 外部 Provider 工具（B-M1）

B 的工具是调用外部 AI 服务，做成 `tool.Tool` + `DefinitionOf`——与本地 read_file/grep 一个接口：

| 工具 | 调用方式 | 异步？ |
|---|---|---|
| `generate_image` | Provider API（提交任务→等回调/poll） | **是** |
| `image_to_video` | Provider API | **是** |
| `analyze_copy` | LLM | 否 |

### FR-B3 — 失败可恢复

A 的控制环失败 = LLM 看报错重试。B 的失败可能是：
- 外部 API 超时/限流
- provider_task_id 丢失
- 回调没到达

→ **每种失败都需要可恢复路径**：AwaitBinding 状态机 + polling 兜底 + 整体 task 可 `ResumeFrom`。

### FR-B4 — Session ↔ Task 贯通

B 的模型与 A 完全一致：

```
A:  session ── task1 ── task2        （每轮 LLM 决策 = 一个 task）

B:  session ── task1                  （LLM 决策 = 一个 task）
                   ├── image          （子 task，async provider）
                   ├── video          （子 task，async provider）
                   └── publish        （子 task，sync）
```

→ `entry_type = "agent"`，`tasks.session_id → sessions.id`，`tasks.parent_id`/`root_id` 串起子调用。

**A 和 B 在数据模型上不分裂。session 是真正的稳定抽象。**

### FR-B5 — Agent ↔ Workflow 互调

- agent 产出的 DAG 可存入 `workflows`/`workflow_versions` 作为可复用资产
- 已有 workflow 作为 agent 工具被调用 → `subworkflow_step` spawn 子 task
- `tasks.parent_id`/`root_id` 已有语义的兑现

### FR-B6 — 成本计量（B-M3，后移）

没有人会因为"session 页面看不到花了多少钱"而无法使用产品。但会因为"callback 丢了"而整个任务废掉。

→ 排序：**async → 恢复 → Provider → MVP → cost**。cost 是优化，不是闭环。

---

## 5. B 可能是混合形态（不是纯 control loop）

B 的典型执行不是纯"一步一步"，而是混合：

```
用户目标
  ↓
LLM planner（control loop，几轮思考）
  ↓ 决定："并行生成 4 个候选镜头"
  ↓ 一次返回 []PlanNode（map fanout）
  ↓
4 个并行的 image task（async）
  ↓ join 等齐
  ↓ 选择最好的一个
  ↓ 继续 control loop（下一步：图生视频）
```

→ **B 会第一次真正触碰 FR4（循环里铺子 DAG）**。这是好事——这恰好是 `PlanSource.Next` 返回 `[]*PlanNode` 天生支持的形态。

---

## 6. M2.4 的答案

不是一个系统 vs 另一个系统。而是 **scheduler 后面挂了一个 durable backend**：

```
        PlanSource               ← 复用 A
            ↓
        Scheduler                ← 复用在多数情况下（async 语义待 B-M0 验证）
            ↓
        Invoker Tool
            ↓
   WorkflowSuspendedError        ← A 从未碰过的边界
            ↓
╔═══════════════════════════╗
║   engine async layer      ║   ← B 的持久化后端
║   AwaitBinding            ║
║   Persist                 ║
║   PollWorker              ║
║   Callback                ║
║   Resume (CompleteAwait)  ║
╚═══════════════════════════╝
            ↓
   Scheduler.Resume
```

**仍然是同一个系统。只是 scheduler 后面接了一个 durable backend。**

---

## 7. 与 A 的共享面（不重造，不分叉）

| 共享 | 说明 |
|---|---|
| `runtime.Plan` + `Scheduler` | B 也用同一内核 |
| `PlanSource` 接缝 | B 的 LLMPlanner 复用 A 的类型 A |
| `tool.Tool` + `DefinitionOf` | Provider 工具与本地工具一个接口 |
| `session.Store` | A 的会话模型在 B 中原样成立 |
| `model.OpenAICompatibleProvider` | LLM 调用复用 |

---

## 8. 不做

- ❌ **不让视频来验证 async**（B-M0 是 async_hello）
- ❌ 不做完整 DAM / 权限 / GUI
- ❌ 不做完整电商平台对接
- ❌ trace / replay（继续冻结）
- ❌ **在 B-M0 跑通前，不宣布 kernel 需要改还是不需要改**

---

## 9. 对照 v2 需求文档

| 需求 | B 覆盖 |
|---|---|
| FR1 两种 PlanSource | 复用 A |
| FR2 共享底座 | A 的 kernel 不分为 B |
| FR4 混合形态（子 DAG） | B-M4 map fanout 首次触碰 |
| FR5 执行前校验 | Provider 工具 JSON Schema 校验 |
| FR6 终止 | 复用 A 的 give_up + 无进展 |
| FR7 工具经 MCP | Provider 工具可经 MCP expose |
| FR8 回放 | 暂不需要 |
