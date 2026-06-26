# Flux Public API v1

> **Stability Note**: This API is considered STABLE for the DreamAI migration phase.
> It may evolve only by extension (new methods, new optional fields), not by modification
> (no signature changes, no removal, no semantic breakage). DreamAI's 32 workflows and
> 200+ tools depend on this contract — treat it as a migration anchor, not a design draft.
>
> 写作时间：2026-06-26。背景：DreamAI Discovery 完成，B-M2 async 内核验证完毕。
> 目的：在 DreamAI 开始迁移之前，冻结 Flux 作为嵌入式执行内核的公开 API。
> 这份 API 是 Flux 的第一个长期契约——不是设计稿，是不可逆转的产品面。

---

## 0. 为什么现在

Flux 已经过了"证明自己能跑"的阶段。B-M0 到 B-M2 验证了 suspend/resume/crash/fanout/partial failure 全部成立。DreamAI Discovery 证明了 `WorkflowDefinition` 已经是你我共同的 DSL。

但有一个东西还没定：**外部世界怎么调用 Flux。**

当前状态：调用方需要 import `runtime`、`workflow`、`planner`、手动构造 `StaticSource`、`MemState`、`Scheduler`。这是内部实现全部裸奔——DreamAI 要接进来得先理解 5 个包。

**如果现在不冻结 API，DreamAI、CodeAgent、未来的任何宿主每接入一次，就要跟着内部重构一起变。**

这份文档定义 Flux 的"包装纸"——外面的世界只看到这层，里面随便重构。

---

## 1. 设计原则

| # | 原则 | 反例 |
|---|---|---|
| P1 | DreamAI 只知道 `flux` 一个包 | ❌ `import "flux/runtime"` |
| P2 | 内部类型全部不可见 | ❌ 暴露 `PlanNode`、`StaticSource`、`Scheduler` |
| P3 | 配置是"能力"不是"端口" | ❌ `Config{Await: ..., Store: ...}` |
| P4 | 注册是统一的 | ❌ `RegisterWorkflow` + `RegisterTool` + `RegisterSkill` |
| P5 | 恢复不是业务代码的职责 | ❌ 业务代码调用 `Resume(node, output)` |
| P6 | v1 只暴露已被验证正确的东西 | ❌ 预建 retry policy、priority queue、metrics |

---

## 2. 公开 API 面

### 2.1 唯一入口：`Engine`

```go
package flux

// Engine 是 Flux 执行内核的外部面。
// DreamAI 创建一个 Engine，注册能力，然后只调 Run 和 Notify。
type Engine struct { ... }

// New 创建一个 Engine。Backend 由宿主提供（DreamAI 用自己的 DB 实现）。
func New(cfg Config) (*Engine, error)
```

### 2.2 配置：`Config` + `Backend`

```go
// Config 是 Engine 的配置。唯一的必填字段是 Backend——其余都有合理默认值。
type Config struct {
    Backend Backend         // 宿主提供的持久化 + 异步基础设施
    Emitter Emitter         // 可选：事件发射（默认 noop）
}

// Backend 是宿主必须实现的持久化契约。
// DreamAI 用自己现有的 DB 实现这个接口（NodeRuntime 表 + AwaitBinding 表）。
//
// 这是一个粗粒度接口：Flux 内部拆成 Store/Await/Lock，
// 但宿主只看到一个 Backend。
type Backend interface {
    // PersistNode 持久化单个节点的状态和输出。
    // node 是节点名，state 是 NodeState，output 是节点的公开产出。
    // 每次节点状态转换时调用（Pending→Running、Running→Awaiting、Running→Success 等）。
    PersistNode(ctx context.Context, taskID string, node string, state NodeState, output map[string]any) error

    // CreateAwait 为异步节点创建一个外部等待凭证。
    // 返回 bindingID：Flux 用它关联后续的 Notify。
    // providerTaskID 是外部 Provider 返回的任务 ID（如 tts job_id）。
    CreateAwait(ctx context.Context, taskID string, node string, providerTaskID string, input map[string]any) (bindingID string, err error)

    // CompleteAwait 原子地将 binding 标记为完成（从 waiting→completing）。
    // 返回 (claimed, error)。claimed=false 表示已被其他线程完成（幂等安全）。
    //
    // 这是 B-M0 验证过的 ClaimCompleting 模式——原子 CAS，阻止重复回调。
    CompleteAwait(ctx context.Context, bindingID string) (claimed bool, err error)

    // Lock 获取分布式锁。用于 Resume 时的并发控制。
    // 返回 unlock 函数和 error。获取失败时阻塞等待（带超时）。
    Lock(ctx context.Context, key string) (unlock func(), err error)

    // LoadState 加载任务的所有节点状态（crash 恢复用）。
    LoadState(ctx context.Context, taskID string) (*TaskState, error)
}

// TaskState 是任务的完整可恢复状态。
type TaskState struct {
    Input  map[string]any
    Nodes  map[string]NodeSnapshot
}

type NodeSnapshot struct {
    State  NodeState
    Output map[string]any
}
```

**关键的抽象决策**：`Backend` 是一个接口，不是一组接口。这意味着 Flux 内部可以根据需要拆成 `Store` + `AwaitController` + `LockManager`，但 **DreamAI 只实现一个东西**。将来增加 `Lease`、`Metrics`、`Checkpoint` 时，`Backend` 接口里加方法即可——DreamAI 的 `Config` 代码一行不变。

### 2.3 注册：`Register(Asset)`

```go
// Asset 是可以被 Engine 执行的能力单元。
// 它不关心底层是 workflow、tool 还是 skill——对 Engine 来说都是"一个可以 Run 的东西"。
type Asset interface {
    Name() string
    // 內部標記，不對外暴露
}

// Register 注册一个 Asset。同名重复注册会 panic（启动期错误，不应静默覆盖）。
func (e *Engine) Register(a Asset) error
```

三种 `Asset`：

```go
// WorkflowAsset 包装一个 WorkflowDefinition。
// 未来内部走 Compile → Plan → StaticSource → Scheduler。
func Workflow(def *definition.WorkflowDefinition) Asset

// ToolAsset 包装一个 tool.Tool。
// 作为叶子节点被 workflow 或 planner 调用。
func Tool(t tool.Tool) Asset

// SkillAsset 包装一个 SKILL.md。
// Skill 在 Engine 内部展开为 Workflow 或 Tool，对调用方透明。
func Skill(spec *skill.SkillSpec) Asset
```

**为什么是 `Asset` 而不是三个 `RegisterXxx` 方法**：因为 Planner 眼里它们没有区别。`goods_video_pro_v3`（workflow）、`tts_generate`（tool）、`weekly_news`（agent-created skill）在工具菜单里是平等的。统一的 `Register(Asset)` 直接反映这一事实。

### 2.4 执行：`Run`

```go
// RunRequest 是一次执行请求。
type RunRequest struct {
    Asset string         // 已注册的 Asset 名
    Input map[string]any // 任务输入
    // Phase 2 扩展：TaskID（续接已有任务）、Timeout、Priority
}

// RunResult 是一次执行的结果。
type RunResult struct {
    Status RunStatus      // Completed / Suspended / Failed
    Output map[string]any // 完成时的最终产出（Suspended 时为 nil）
    TaskID string         // 内部生成的 task ID（供 Notify 定位）
    Err    error          // 失败时的错误
}

type RunStatus int

const (
    StatusCompleted RunStatus = iota
    StatusSuspended          // 有异步节点在等待外部事件，后续通过 Notify 唤醒
    StatusFailed
)

// Run 执行一个已注册的 Asset。如果 Asset 包含异步节点，返回 StatusSuspended。
func (e *Engine) Run(ctx context.Context, req RunRequest) (*RunResult, error)
```

### 2.5 外部事件：`Notify`

```go
// Event 是外部世界发生的事件（webhook 到达、poll 返回、消息队列推送）。
type Event struct {
    // Provider 是外部服务标识（如 "tts"、"aliyun"、"volcengine"）。
    // Flux 用它匹配 AwaitBinding。
    Provider string

    // ProviderTaskID 是外部 Provider 返回的任务 ID。
    // Flux 用它查找对应的 AwaitBinding。
    ProviderTaskID string

    // Output 是外部任务完成的产出。
    Output map[string]any

    // Error 是外部任务失败的信息（为空表示成功）。
    Error string
}

// Notify 通知 Engine：一个外部异步任务已完成。
// Engine 内部完成流程：
//   1. 查找 binding（按 Provider + ProviderTaskID）
//   2. 原子 ClaimCompleting
//   3. 标记节点 NodeSuccess（或 NodeFailed，如果 Event.Error != ""）
//   4. Resume DAG 继续执行
//
// 调用方不需要知道 binding、node、scheduler——给一个 Event 就够了。
func (e *Engine) Notify(ctx context.Context, event Event) (*RunResult, error)
```

**为什么是 `Notify` 而不是 `Resume`**：`Resume(node, output)` 暴露了内部状态（哪个 node、什么 output）。`Notify` 说的是业务语言——"TTS 任务做完了"——Engine 自己找到对应的 await、恢复对应的 node。

### 2.6 异步 worker 注册（可选）

对于 poll-based Provider，宿主可以注册一个 poll worker：

```go
// PollWorker 定期巡检外部 Provider 的状态。
type PollWorker struct {
    Provider string
    Interval time.Duration
    Poll     func(ctx context.Context, providerTaskID string) (*Event, error)
}

// RegisterPollWorker 注册一个轮询工作器。
// Engine 会在后台启动 goroutine 定期轮询。
// Phase 1 可以不用——先用手动 Notify/webhook 验证。
func (e *Engine) RegisterPollWorker(w PollWorker)
```

---

## 3. 公开的状态类型

```go
// NodeState 是节点的生命周期状态（与现有 runtime.NodeState 一致）。
type NodeState int

const (
    NodePending   NodeState = iota // 尚未就绪
    NodeRunning                     // 正在执行
    NodeAwaiting                    // 等待外部事件
    NodeSuccess                     // 执行成功
    NodeFailed                      // 执行失败
    NodeSkipped                     // 被跳过（条件分支未激活）
)
```

---

## 4. 什么是 internal

这些包和类型 **不出现在公开 API 中**，DreamAI 不 import、不构造、不依赖：

| internal | 说明 |
|---|---|
| `runtime.Scheduler` | 核心调度器（Engine 内部持有） |
| `runtime.Plan` / `PlanNode` | 编译后的执行图（Engine 内部从 Asset 编译） |
| `runtime.StaticSource` | 静态 Plan 的一次性发射器 |
| `runtime.MemState` | 内存执行状态（Engine 内部从 Backend.LoadState 恢复） |
| `runtime.Invoker` | 工具调用（Engine 内部用 ToolAsset 构造） |
| `runtime.AwaitController` | 异步控制（Engine 内部适配 Backend） |
| `runtime.Store` | 持久化（Engine 内部适配 Backend） |
| `workflow.Compile` | 工作流编译（Engine 内部调用） |
| `adapters/dreamai.Compile` | 适配层（Engine 内部调用，统一为 Asset→Plan） |
| `planner.ToolInvoker` | Invoker 适配器（Engine 内部构造） |

---

## 5. DreamAI 的接入伪代码

```go
package main

import (
    "context"
    "flux"
    "flux/definition"
    "flux/tool"
)

// dreamBackend 用 DreamAI 现有的 DB 表实现 flux.Backend。
type dreamBackend struct {
    db          *gorm.DB
    nodeRepo    NodeRuntimeRepo
    awaitRepo   AwaitBindingRepo
    lockManager *LockManager
}

func (b *dreamBackend) PersistNode(ctx context.Context, taskID, node string, state flux.NodeState, output map[string]any) error {
    return b.nodeRepo.Upsert(ctx, taskID, node, state, output)
}
// ... 实现其余方法

func main() {
    backend := &dreamBackend{...}

    // 创建 Engine（应用生命周期内唯一）
    engine, _ := flux.New(flux.Config{Backend: backend})

    // 注册所有能力
    engine.Register(flux.Workflow(commerceAssetPrepareDef))
    engine.Register(flux.Workflow(goodsVideoProV3Def))
    engine.Register(flux.Workflow(shortDramaDef))
    // ... 32 workflows
    engine.Register(flux.Tool(ttsSpeechGenerateTool))
    engine.Register(flux.Tool(singleUploadStorageTool))
    // ... 200+ tools

    // 业务请求
    result, _ := engine.Run(ctx, flux.RunRequest{
        Asset: "goods_video_pro_v3",
        Input: map[string]any{"product_id": 12345},
    })

    if result.Status == flux.StatusSuspended {
        // 任务已挂起，等外部回调
        // 当 TTS webhook 到达时：
        engine.Notify(ctx, flux.Event{
            Provider:       "tts",
            ProviderTaskID: "job_67890",
            Output:         map[string]any{"audio_url": "https://..."},
        })
    }
}
```

---

## 6. v1 做什么、不做什么

### v1 包含

| 能力 | 验证状态 |
|---|---|
| `Engine.Run` 同步 DAG | ✅ commerce_asset_prepare POC |
| `Engine.Run` 异步 DAG（→Suspended） | ✅ B-M1 HTTP provider |
| `Engine.Notify` → 恢复 DAG | ✅ B-M1b crash resume |
| `Backend` 持久化/恢复 | ✅ B-M1b file ports |
| `Register(Asset)` 统一注册 | ✅ S0–S6 Skill Runtime |
| Fanout 并行执行 | ✅ B-M2 |
| Partial failure 传播 | ✅ B-M2 |

### v1 明确不做

| 不做 | 原因 |
|---|---|
| MCP 工具自动发现 | Phase 2：先让 DreamAI 的工具在 Flux 上跑通，再考虑协议升级 |
| `Register(skill)` 的 AgentSkill 执行 | S4 deferred：AgentSkill 需要 planner 先成熟 |
| Fork / Replay / Patch | DreamAI 的 fork 语义暂不迁移（Phase 1 不做快照/分支） |
| 内置 poll worker 实现 | 先用手动 Notify/webhook 验证 |
| 成本计量 | B-M3：cost 是优化，不是闭环 |

---

## 7. 从现状到 v1 的路径

当前 Flux 代码库中，公开 API 还不存在。需要新增一个 `flux/` 顶层包：

```
flux/
    engine.go          ← Engine 结构 + New()
    config.go          ← Config + Backend 接口
    asset.go           ← Asset 接口 + Workflow/Tool/Skill 构造函数
    run.go             ← RunRequest + RunResult + Run()
    notify.go          ← Event + Notify()
    state.go           ← NodeState 常量（公开版）
```

**注意**：`flux/` 目录当前已存在（作为 Go module root），里面有 `go.mod` 等文件。新增的 `.go` 文件与现有文件共存——`engine.go` 不碰现有的项目根文件。

**迁移顺序建议**（不是一次性完成）：

1. **先写 `flux/engine.go` + `flux/config.go`**（API 骨架，编译通过即可）
2. **DreamAI 实现 `Backend` 接口**（用现有的 `NodeRuntime` 表 + `AwaitBinding` 表）
3. **`Engine.Run` 接上 `workflow.Compile` + `runtime.Scheduler`**（内部实现）
4. **拿 `commerce_asset_prepare` 跑通第一个端到端**（DreamAI 代码调 `engine.Run`）
5. **验证异步：`goods_tts_segment_generate`**（submit→Notify→resume）
6. **逐步注册全部 32 个 workflow**

---

## 8. 这份文档的承诺

1. **`Engine` 的公开方法签名在 v1 生命周期内不变。** 新增方法可以（`RegisterPollWorker`），现有签名不改。
2. **`Backend` 接口只增不减。** 新增方法加默认实现（interface extension），已有方法不改签名。
3. **`Asset` 语义统一。** 不管底层是 workflow、tool 还是 skill，`Register` 和 `Run` 的行为一致。
4. **内部包可以重构。** `runtime`、`workflow`、`planner` 的内部重构不影响 `Engine` 的调用方。
