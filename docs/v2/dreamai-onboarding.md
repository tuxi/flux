# DreamAI → Flux 接入指南

> 目标读者：DreamAI 后端开发者。这份文档告诉你需要实现什么、怎么对接、先后顺序。

---

## 0. 前置理解

Flux 现在是 DreamAI 的执行内核。你要做的事情不是"把 workflow 迁走"，而是：

**让 DreamAI 的服务启动时创建一个 `flux.Engine`，把所有 workflow 和 tool 注册进去，然后用 `engine.Run()` 替代原有的 `engine.RunWithResult()`。**

核心原则：
- **WorkflowDefinition 不用改。** 你现有的 DSL 结构就是 Flux 的输入格式。
- **Tool 接口完全一致。** 你的 `tool.Tool` 实现直接注册到 Flux。
- **只实现一个接口：`flux.Backend`。** 用你现有的 DB 表。

---

## 1. 第一步：引入依赖

在 DreamAI 的 `go.mod` 中添加 Flux 依赖：

```
require flux v0.0.0

replace flux => ../flux
```

（Phase 1 用本地路径；稳定后切到 tag。）

---

## 2. 第二步：实现 `flux.Backend`

这是你唯一需要写的新代码。5 个方法，都用你现有的 DB 表实现。

### 2.1 整体结构

```go
package backend

import (
    "context"
    "flux"
)

type DreamBackend struct {
    db        *gorm.DB
    nodeRepo  NodeRuntimeRepo   // 你现有的
    awaitRepo AwaitBindingRepo  // 你现有的
    locker    *LockManager      // 你现有的分布式锁
}

func NewDreamBackend(db *gorm.DB) *DreamBackend { ... }
```

### 2.2 PersistNode

```go
func (b *DreamBackend) PersistNode(ctx context.Context, taskID, node string, state flux.NodeState, output map[string]any) error {
    // 映射 Flux NodeState → DreamAI NodeState
    //   flux.NodePending   → domain.NodePending
    //   flux.NodeRunning   → domain.NodeRunning
    //   flux.NodeAwaiting  → domain.NodeAwaiting
    //   flux.NodeSuccess   → domain.NodeSuccess
    //   flux.NodeFailed    → domain.NodeFailed
    //   flux.NodeSkipped   → domain.NodeSkipped

    taskIDInt, _ := strconv.ParseInt(taskID, 10, 64)

    return b.nodeRepo.Upsert(ctx, &domain.NodeRuntime{
        TaskID: taskIDInt,
        Name:   node,
        State:  mapFluxStateToDreamAI(state),
        Output: output,
    })
}
```

**关键点**：
- Flux 的 `NodeState` 枚举值和 DreamAI 的 `domain.NodeState` 值一一对应——直接 cast 即可。
- Flux 传的 `taskID` 是 string。Phase 1 可以用 workflow name 作为 taskID。Phase 2 改为雪花 ID。

### 2.3 CreateAwait

```go
func (b *DreamBackend) CreateAwait(ctx context.Context, taskID, node, providerTaskID, input map[string]any) (string, error) {
    taskIDInt, _ := strconv.ParseInt(taskID, 10, 64)

    binding := &domain.AwaitBinding{
        TaskID:       taskIDInt,
        NodeName:     node,
        AwaitType:    domain.AwaitTypeExternalTask,
        Source:       domain.AwaitSourceWebhookOrPoll,
        Status:       domain.AwaitBindingWaiting,
        Config:       map[string]any{"input": input},
    }

    if err := b.awaitRepo.Create(ctx, binding); err != nil {
        return "", err
    }

    return strconv.FormatInt(binding.ID, 10), nil
}
```

**关键点**：
- Flux 调用这个方法时，节点已经进入 async 执行路径。你只需要创建 `AwaitBinding` 记录。
- 返回的 `bindingID` 是一个字符串——Flux 用它关联后续的 `CompleteAwait`。

### 2.4 CompleteAwait

```go
func (b *DreamBackend) CompleteAwait(ctx context.Context, bindingID string) (bool, error) {
    id, _ := strconv.ParseInt(bindingID, 10, 64)

    // 原子 CAS：只有 Waiting → Completing 成功才返回 true
    claimed, err := b.awaitRepo.ClaimCompleting(ctx, id, []domain.AwaitBindingStatus{
        domain.AwaitBindingWaiting,
    })
    if err != nil {
        return false, err
    }

    return claimed, nil
}
```

**关键点**：
- 这是 B-M0 验证过的幂等保护——同一个 binding 被多个 webhook/poll 同时完成时，只有一个成功。
- `ClaimCompleting` 必须用 `WHERE status IN (...)` 的原子更新（你的 `awaitRepo` 应该已经有了）。

### 2.5 Lock

```go
func (b *DreamBackend) Lock(ctx context.Context, key string) (func(), error) {
    // Phase 1 可以降级：直接返回 noop unlock
    // Phase 2 用你的 dLocker（Redis 分布式锁）
    return func() {}, nil
}
```

**Phase 1 简化**：如果暂时没有并发 Resume 的场景（单 worker），Lock 可以返回空操作。

### 2.6 LoadState

```go
func (b *DreamBackend) LoadState(ctx context.Context, taskID string) (*flux.TaskState, error) {
    taskIDInt, _ := strconv.ParseInt(taskID, 10, 64)

    nodes, err := b.nodeRepo.FindByTaskID(ctx, taskIDInt)
    if err != nil {
        return nil, err
    }

    state := &flux.TaskState{
        Nodes: map[string]flux.NodeSnapshot{},
    }

    for _, n := range nodes {
        state.Nodes[n.Name] = flux.NodeSnapshot{
            State:  mapDreamAIStateToFlux(n.State),
            Output: n.Output,
        }
    }

    return state, nil
}
```

---

## 3. 第三步：启动时注册

在 DreamAI 服务启动时（`cmd/server/init.go` 或类似位置）：

```go
import (
    "flux"
    "your-project/backend"
)

func initEngine() *flux.Engine {
    // 1. 创建 Backend
    dreamBackend := backend.NewDreamBackend(db)

    // 2. 创建 Engine
    engine, _ := flux.New(flux.Config{Backend: dreamBackend})

    // 3. 注册所有 Workflow（32 个）
    engine.Register(flux.Workflow(commerceAssetPrepareDef))
    engine.Register(flux.Workflow(goodsVideoProV3Def))
    engine.Register(flux.Workflow(shortDramaDef))
    engine.Register(flux.Workflow(aiHotNewsVideoDef))
    engine.Register(flux.Workflow(textToImageDef))
    engine.Register(flux.Workflow(imageToVideoDef))
    // ... 全部注册

    // 4. 注册所有 Tool（200+ 个）
    engine.Register(flux.Tool(ttsSpeechGenerateTool))
    engine.Register(flux.Tool(singleUploadStorageTool))
    engine.Register(flux.Tool(singleResourceDownloadTool))
    // ... 全部注册

    return engine
}
```

---

## 4. 第四步：业务层调用

原有代码：

```go
result := e.RunWithResult(ctx, task, def)
```

替换为：

```go
result, err := engine.Run(ctx, flux.RunRequest{
    Asset:  "goods_video_pro_v3",
    Input:  taskInput,
    TaskID: strconv.FormatInt(task.ID, 10), // 传入 DreamAI 已创建的 Task.ID
})

switch result.Status {
case flux.StatusCompleted:
    // 任务完成，result.Output 是最终产出
case flux.StatusSuspended:
    // 任务挂起，等外部回调
case flux.StatusFailed:
    // 任务失败
}
```

---

## 5. 第五步：异步回调

当外部 Provider（TTS、图片生成、视频生成）回调到达时：

原有代码：

```go
e.CompleteAwaitNode(bindingID, output, "", "webhook")
```

替换为：

```go
engine.Notify(ctx, flux.Event{
    Provider:       "tts",
    ProviderTaskID: "job_67890",
    Output: map[string]any{
        "audio_url": "...",
    },
})
```

Flux 内部自动完成：查找 binding → 原子 claim → 标记节点成功 → Resume DAG → 返回 `RunResult`。

---

## 6. 迁移顺序（重要：不要一次全迁）

按风险从低到高：

| 阶段 | 做什么 | 验证标准 |
|---|---|---|
| **P0**（立即） | 实现 `Backend` 接口，写好单元测试 | 5 个方法能正确读写现有 DB 表 |
| **P1** | 拿 `commerce_asset_prepare`（5 节点，全同步）跑通 `engine.Run` | 在测试里：Compile → Run → 3 节点全部 NodeSuccess |
| **P2** | 拿 `goods_tts_segment_generate`（含 TTS submit→wait）验证 async | `engine.Run` → Suspended → `Notify` → Completed |
| **P3** | 注册全部 32 个 workflow + 200 个 tool | 启动不报错，所有 workflow 在 Registry 中 |
| **P4** | 逐步替换业务层的 `RunWithResult` 调用 | 一次换一个 workflow，观察线上行为 |
| **P5** | 下线旧 engine 代码 | `ai-engine/engine/` 目录可以删了 |

---

## 7. 你不需要做的事

- ❌ **不需要改 WorkflowDefinition。** DSL 结构不变。
- ❌ **不需要改 Tool 接口。** `tool.Tool` 在两个项目里完全一致。
- ❌ **不需要理解 `runtime.Scheduler`。** Engine 内部管理。
- ❌ **不需要自己调 `Compile`。** `engine.Run` 内部编译。
- ❌ **不需要手动调 `Resume`。** 用 `Notify` 代替。

---

## 8. 对接人联系方式

- **Flux API 问题** → 看 `docs/v2/public-api-v1.md`
- **Backend 实现参考** → 看 Flux 的 `config.go`（接口定义）
- **编译适配** → 看 `adapters/dreamai/compile.go`
- **待定问题** → 通过项目管理员转达
