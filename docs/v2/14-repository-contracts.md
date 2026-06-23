# 14 · Repository 接口契约（Service / Repository 边界锁定）

> 产品方向（00–08）与数据契约（09 API / 10 Manifest / 11 状态机 / 12 数据模型）都已定型。**下一步不是继续讲产品，而是锁 Service / Repository 边界**——否则工程实现时各写各的：有人把序号生成放 service，有人放 repo；有人在引擎回调里直接写会话表。本文把这条边界钉死。
>
> 包路径沿用现有分层：接口在 `ai-engine/agent/repository/`，GORM 实现在 `ai-engine/agent/repository/query/`，与 `ai-engine/repository` + `repository/query` 同构（接口收 `ctx context.Context`、返回 `agent/domain` 结构体、实现里做 domain↔entity 映射、ID 用 `utils.GenSnowflakeID()`）。

## 0. 四条红线（实现期不可逾越）

```text
红线 1：Message.sequence 必须在事务内递增分配——禁止应用层自己算 seq。
红线 2：AgentState 更新必须走 version CAS——禁止读后无条件覆盖。
红线 3：TaskEvent 不得直接写 ConversationMessage——必须经 AgentObserver/Translator。
红线 4：跨服务副作用（建任务/通知/webhook…）一律走 Outbox——事务内只入箱，禁止在 UoW 事务内直接发起。
```

第 3 条是**架构边界**：`Conversation` 与 `TaskEvent` 是两套独立模型。引擎只管发事件（[12](12-data-model-ddl.md) 红线：不动引擎任何表）；会话层只管订阅、翻译、落库。两者**唯一**的桥是 `AgentObserver`（§5）。
第 4 条是**事务边界**：本地 DB 事务（Conversation/AgentState/Message/Plan）与跨边界副作用（CreateTask 等）必须分离，**Post-Commit 投递**，否则产生最难修的孤儿任务（§4.2）。

## 1. 依赖方向（编译期就该挡住越界）

```text
   ┌─────────────────────────────────────────────────────────┐
   │                  ai-engine/agent/                         │
   │   service (ConversationService / AgentRuntime / Observer) │
   │        │  调用                                            │
   │        ▼                                                  │
   │   repository(接口) ◀── query(GORM 实现)                   │
   └───────────┬─────────────────────────────────┬───────────┘
               │ 单向只读引用 tasks.id             │ 订阅(只读事件)
               ▼                                  ▼
        ┌────────────┐                    ┌────────────────┐
        │ 引擎 tasks  │                    │  EventBus       │
        │ (不改)      │                    │ (TaskEvent 源)  │
        └────────────┘                    └────────────────┘
```

- `agent` 包 **MAY** import 引擎的 `domain`/`eventbus`/`engine`（建任务、订阅事件、发 signal）。
- 引擎包 **MUST NOT** import `agent`——引擎对「有没有会话在听」零感知（[02](02-architecture-overview.md) 非侵入边界）。这条用 import 方向即可在 CI 静态检查。

## 2. 领域类型（`agent/domain`）

接口签名引用这些结构（[11 §2](11-agent-state-machine.md#2-agentstate-结构) / [12](12-data-model-ddl.md) 已定义其字段，这里只列类型名）：

```go
package domain // ai-engine/agent/domain

type Conversation struct { /* id,user_id,title,status,entry,intent,current_plan_id,context_task_id,last_sequence,... */ }
type Message struct      { /* id,conversation_id,sequence,role,kind,text,content_json,task_id,reply_to,client_msg_id,grade,created_at */ }
type Plan struct         { /* id,conversation_id,version,intent,skill_key,slots,stages,estimated_cost,status,... */ }
type TaskLink struct     { /* id,conversation_id,task_id,plan_id,relation,forked_from_task_id,created_at */ }
type AgentState struct   { /* 见文档 11 §2 */ }

type Stage string  // idle|collecting|planning|confirming|executing|reviewing|awaiting_user|completed|failed
type Status string // active|awaiting_user|running|completed|archived
```

### 哨兵错误（跨实现统一）
```go
var (
    ErrNotFound        = errors.New("agent: not found")
    ErrForbidden       = errors.New("agent: forbidden")          // 越权
    ErrConversationArchived = errors.New("agent: conversation archived")
    ErrDuplicate       = errors.New("agent: duplicate client_msg_id") // 幂等命中
    ErrVersionConflict = errors.New("agent: agent_state version conflict") // CAS 失败
    ErrSignalStale     = errors.New("agent: signal stale")        // 卡片已被消费
)
```

## 3. 五个 Repository 接口

> 约定：所有方法 `ctx` 优先；写方法在**实现内部完成 domain→entity 映射**；**这些接口既被独立实例（读路径，持 `r.db`）使用，也被 UoW 注入的 tx 绑定实例（写路径，持 `tx`）使用**——同一接口，两种绑定（§4）。

### 3.1 `ConversationRepository`
```go
type ConversationRepository interface {
    Create(ctx context.Context, c *domain.Conversation) error
    GetByIDForUser(ctx context.Context, id, userID int64) (*domain.Conversation, error) // 归属校验：非本人→ErrForbidden
    List(ctx context.Context, userID int64, status string, cursor string, limit int) (items []*domain.Conversation, next string, err error)

    UpdateTitle(ctx context.Context, id, userID int64, title string) error
    Archive(ctx context.Context, id, userID int64) error

    // SetStatus 仅由 UoW 内部调用，同步自 AgentState.stage（红线 3 的派生写）
    SetStatus(ctx context.Context, id int64, status domain.Status) error
    // SetCurrentPlan 指向当前生效 Plan
    SetCurrentPlan(ctx context.Context, id, planID int64) error

    // NextSequence 在事务内对会话行加锁并 +1 返回新序号（红线 1 的唯一合法入口）
    // 实现：UPDATE conversations SET last_sequence=last_sequence+1 ... RETURNING last_sequence
    // 仅允许在 UoW 事务内调用（独立实例调用应 panic/报错，防误用）
    NextSequence(ctx context.Context, id int64) (int64, error)
    // UpdatePreview 列表页直出用
    UpdatePreview(ctx context.Context, id int64, preview string) error
}
```

### 3.2 `MessageRepository`
```go
type MessageRepository interface {
    // Append 追加一条消息。MUST 在 UoW 事务内调用：
    //   1) 调 ConversationRepository.NextSequence 取号（红线 1）
    //   2) 写入 client_msg_id 唯一约束；冲突→返回已存在消息 + ErrDuplicate（幂等）
    // 返回带 sequence 的落库消息。
    Append(ctx context.Context, m *domain.Message) (*domain.Message, error)

    // UpsertTransient 进度类 transient 消息按 task_id 原地覆盖，不占 sequence、可不入库（红线 1 不适用）
    UpsertTransient(ctx context.Context, m *domain.Message) error

    FindByClientMsgID(ctx context.Context, conversationID int64, clientMsgID string) (*domain.Message, error) // 幂等预检
    GetByID(ctx context.Context, id int64) (*domain.Message, error)

    // ListAfter 拉取/断线增量：sequence > afterSeq，升序，limit（[09 §2.5]）
    ListAfter(ctx context.Context, conversationID, afterSeq int64, limit int) (items []*domain.Message, lastSeq int64, hasMore bool, err error)
}
```

### 3.3 `AgentStateRepository`
```go
type AgentStateRepository interface {
    Get(ctx context.Context, conversationID int64) (*domain.AgentState, error) // rehydrate（[11 §8]）
    Init(ctx context.Context, s *domain.AgentState) error                       // 建会话时 stage=idle, version=0

    // CompareAndSwap 乐观锁更新（红线 2）：
    //   UPDATE agent_states SET ..., version=version+1 WHERE conversation_id=? AND version=?
    //   影响 0 行 → ErrVersionConflict（调用方重读重试）
    CompareAndSwap(ctx context.Context, next *domain.AgentState, expectedVersion int) error

    // ListStuck 扫描长期阻塞会话做主动提醒（[11 §9]）；stage ∈ {confirming,awaiting_user,reviewing}
    ListStuck(ctx context.Context, stages []domain.Stage, olderThan time.Time, limit int) ([]*domain.AgentState, error)
}
```

### 3.4 `PlanRepository`
```go
type PlanRepository interface {
    Create(ctx context.Context, p *domain.Plan) error                 // version 由调用方传入（= 上一版本+1）
    GetByID(ctx context.Context, id int64) (*domain.Plan, error)
    LatestVersion(ctx context.Context, conversationID int64) (int, error) // 取当前最大 version；无则 0
    SetStatus(ctx context.Context, id int64, status string) error      // draft→confirmed→executing→done|revised
}
```

### 3.5 `TaskLinkRepository`
```go
type TaskLinkRepository interface {
    Create(ctx context.Context, l *domain.TaskLink) error               // uniq(conversation_id,task_id) 兜底
    FindByTaskID(ctx context.Context, taskID int64) (*domain.TaskLink, error) // ★ AgentObserver 反查会话的入口
    ListByConversation(ctx context.Context, conversationID int64) ([]*domain.TaskLink, error)
}
```

> `FindByTaskID` 是红线 3 能成立的关键：引擎事件只带 `task_id`，Observer 靠它反查 `conversation_id`，再决定往哪个会话写消息。

### 3.6 `OutboxRepository`（副作用出箱，见 §4.2）
```go
type OutboxRepository interface {
    // Enqueue 在 UoW 事务内插入一条副作用记录（与本地写一起提交，红线 4）
    Enqueue(ctx context.Context, rec *domain.OutboxRecord) error
    // ClaimBatch 出箱 Worker 抢一批待处理记录（status=pending，CAS 占用，防多 Worker 重复）
    ClaimBatch(ctx context.Context, workerID string, limit int) ([]*domain.OutboxRecord, error)
    MarkDone(ctx context.Context, id int64) error
    MarkFailed(ctx context.Context, id int64, errMsg string, nextRetryAt time.Time) error
}
```

## 4. `ConversationUnitOfWork`（最关键）

很多操作必须事务一致，不能散在多个 repo 各自提交。UoW 提供「在一个事务内拿到全部 tx 绑定仓储」的原语：

```go
// Repos 一组共享同一事务的仓储句柄（tx-scoped）
type Repos struct {
    Conversations ConversationRepository
    Messages      MessageRepository
    AgentStates   AgentStateRepository
    Plans         PlanRepository
    TaskLinks     TaskLinkRepository
    Outbox        OutboxRepository   // 副作用入箱（§4.2）
}

type ConversationUnitOfWork interface {
    // Within 在单个 DB 事务内执行 fn。fn 返回 error→回滚；nil→提交。
    // 实现：db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    //          return fn(newTxBoundRepos(tx))
    //       })
    Within(ctx context.Context, fn func(r Repos) error) error
}
```

### 4.1 标准「推进一轮」配方（canonical turn）
> **关键纠正**：决策 `Decide` 可能调用 LLM/RAG（秒级），**绝不能放在打开的 DB 事务内**——否则事务长期占锁。因此顺序是「**库外决策 → 事务内落本地写 → 提交后做副作用**」。

```go
func (s *ConversationService) advanceTurn(ctx context.Context, conv *domain.Conversation, userMsg *domain.Message) error {
  // 0) 幂等预检（库外）：命中则直接返回首条，不再决策、不再推进
  if dup, _ := s.msgs.FindByClientMsgID(ctx, conv.ID, userMsg.ClientMsgID); dup != nil { return nil }

  // 1) 读当前状态（库外，rehydrate）
  cur, err := s.states.Get(ctx, conv.ID); if err != nil { return err }

  // 2) 决策：可含 LLM/RAG，纯读、不落库，产出 AgentDecision（见文档 15）
  decision, err := s.runtime.Decide(ctx, cur, userMsg); if err != nil { return err }

  // 3) 事务内：只做「快速本地写」，不触碰跨服务副作用
  err = s.uow.Within(ctx, func(r Repos) error {
    if _, e := r.Messages.Append(ctx, userMsg); e != nil { return e }              // ① 取号（红线 1）/ 幂等
    if e := r.AgentStates.CompareAndSwap(ctx, decision.NextState, cur.Version); e != nil { return e } // ② CAS（红线 2）
    for _, am := range decision.Outbound { if _, e := r.Messages.Append(ctx, am); e != nil { return e } } // ③ Agent 消息
    if decision.NewPlan != nil {                                                    // ④ 必要时建 Plan
        if e := r.Plans.Create(ctx, decision.NewPlan); e != nil { return e }
        _ = r.Conversations.SetCurrentPlan(ctx, conv.ID, decision.NewPlan.ID)
    }
    if decision.Launch != nil {                                                     // ④' 副作用入箱（红线 4）
        if e := r.Outbox.Enqueue(ctx, outbox.CreateTask(conv.ID, decision.Launch)); e != nil { return e }
    }
    return r.Conversations.SetStatus(ctx, conv.ID, mapStageToStatus(decision.NextState.Stage)) // ⑤ 同步 status
  })
  // 4) 提交成功 → 副作用由 Outbox Worker 异步执行（CreateTask → CreateTaskLink），见 §4.2
  return err
}
```

要点：
- **库外决策**：`Decide` 在事务外，避免把 LLM 延迟压进 DB 事务。
- **sequence 事务内分配**（①③）——红线 1。
- **AgentState 走 CAS**（②）——红线 2；`ErrVersionConflict` → 回到步骤 1 **重读重 Decide**（幂等键保证不重复建用户消息；并发多见于「用户信号」与「引擎事件」同时改同一会话，CAS 天然串行化）。
- **建引擎任务不在事务内**（④'）——见 §4.2，已拍板的硬规则。

### 4.2 Post-Commit 创建任务 + Outbox（已拍板）
**决策（锁定）**：`CreateTask()` **必须 Post-Commit**，不得放进 UoW 事务。

`Conversation / AgentState / Message / Plan` 是**本地 DB 事务**；`CreateTask()` 是**跨边界副作用**（Workflow Engine API + Redis 队列 + EventBus + Scheduler）。两者混在一个事务里会产生**最难修的孤儿任务**：

```text
tx.Begin → CreateConversation → CreatePlan → CreateTask(成功) → tx.Commit(失败)
结果：Task 已存在，但 Conversation 不存在 → 孤儿任务
```

正确顺序：

```text
tx.Begin → CreateConversation → CreatePlan → CreateMessage → tx.Commit
        提交成功后 → CreateTask() → CreateTaskLink()
```

**推荐进一步演进为 Outbox Pattern**（本文按此设计 §3.6 / `Repos.Outbox`）：事务内只**入箱**一条 `create_task` 记录，与本地写一起原子提交；提交后由 **Outbox Worker** 消费：

```text
事务内：CreateConversation + CreatePlan + CreateMessage + Outbox.Enqueue(type=create_task)  ── 一起 Commit
提交后：Outbox Worker 取 create_task → engine.CreateTask()(成功) → TaskLinks.Create() → outbox.MarkDone
                                       失败 → MarkFailed + 退避重试（至上限）
```

为什么第一版就值得上 Outbox：Agent 的副作用**不止建任务**——还会发推送、发 webhook、写审计、调三方、发通知。一旦确立 Outbox，这些副作用都「事务内入箱、提交后投递、失败可重试」，统一且可靠。`agent_outbox` 表见 [12 §8.2](12-data-model-ddl.md#82-agent_outbox推荐副作用出箱)。

> **红线 4**（本节确立）：跨服务副作用（建任务/通知/webhook…）一律走 Outbox，事务内只入箱，**禁止在 UoW 事务内直接发起**。

## 5. AgentObserver / Translator（红线 3 的落点）

引擎事件→会话消息的**唯一通道**。它订阅 EventBus，自身就是「会话层的写入者」，引擎对它无感。

```go
// AgentObserver 订阅 EventBus，把 TaskEvent 翻译为 ConversationMessage / 推进 AgentState。
// 它是 Conversation 与 TaskEvent 之间唯一的桥（红线 3）。
type AgentObserver interface {
    // OnTaskEvent 由 EventBus 回调（每条引擎事件一次）
    OnTaskEvent(ctx context.Context, ev domain.TaskEvent) error
}
```

实现职责（全部经 UoW，不绕过仓储直接写表）：
```text
OnTaskEvent(ev):
  link ← TaskLinks.FindByTaskID(ev.TaskID)      // 反查会话；查不到=非会话发起的任务→忽略
  if link == nil: return                          // 引擎任务可独立存在，Observer 不强行接管
  skill ← SkillRegistry.Get(plan.skill_key)
  text  ← skill.TranslateStage(ev)                // 白名单翻译（[10 §6]）；未匹配→不出消息
  uow.Within:
     switch ev.Type:
       progress      → Messages.UpsertTransient(progressMsg)             // 不占 seq
       stage_changed → Messages.Append(stageMsg, grade=persistent)       // 占 seq（红线 1）
       await_user_action → Append(review_card) + AgentStates.CAS(executing→reviewing)  // 红线 2
       task_succeeded    → Append(result_card) + CAS(executing→completed) + Conversations.SetStatus(completed)
       task_failed       → Append(error_card)  + CAS(executing→failed)    + SetStatus(active)
```

**边界强调**：
- 引擎代码里**不出现** `MessageRepository` / `ConversationRepository` 任何调用。grep `agent/repository` 在 `engine`/`worker`/`tool` 包应为 0 命中——可写一个 CI 断言。
- Observer 是会话层组件，运行在会话层进程上下文；它**读** TaskEvent、**写** 会话表，方向单一。
- 用户对卡片的回应若需回流引擎（reviewing 闸门），走 [09 §2.7](09-conversation-api.md#27-用户信号卡片回应) 的 `routed_to=engine` → `await.HandleSignal`，**不经 Observer**（那是入站方向，由 ConversationService 处理）。

## 6. Service 层（谁来调 UoW）

仓储之上是 service，对应 [09](09-conversation-api.md) 的 handler：

| Service 方法 | 触发 | 事务编排 |
|-------------|------|---------|
| `ConversationService.Create` | POST /conversations | `Within`：建 conv + `AgentStates.Init(idle)`；带 first_message 则接 `advanceTurn` |
| `ConversationService.PostMessage` | POST /messages | 幂等预检 → `advanceTurn`（§4.1） |
| `ConversationService.PostSignal` | POST /signals | 判 `routed_to`：`agent`→`advanceTurn`；`engine`→校验 `pending_message_id` 未消费（否则 `ErrSignalStale`）后转 `await.HandleSignal` |
| `ConversationService.Get` / `ListMessages` / `List` | GET 系列 | 只读，独立仓储实例（非 UoW） |
| `AgentObserver.OnTaskEvent` | EventBus 回调 | §5 |

`AgentRuntime.Decide(ctx, state, input) → AgentDecision` 是**决策核心**（意图识别/选 Skill/槽位/翻译都在这；可含 LLM/RAG 只读调用，但**不落库、不发副作用**），**在 UoW 事务外**调用；其产出 `AgentDecision` 由 service 在 `Within` 内一次性持久化（§4.1）。决策与持久化分离 = 易测（[11 §8] 的「无状态处理器」由此成立）。完整决策契约见 [15 · AgentRuntime Decision Engine](15-agent-decision-engine.md)。

## 7. 待定决策（实现前须拍板）
> 已拍板：**建任务 Post-Commit + Outbox**（§4.2，红线 4），不再列为待定。

1. **CAS 重试上限**：`ErrVersionConflict` 重读重 Decide 的最大次数（建议 3，超限返回 5xx，避免活锁）；注意重试会重跑 `Decide`（含 LLM），需配合 `client_msg_id` 去重避免重复副作用。
2. **Outbox 投递语义**：至少一次（at-least-once），故 `engine.CreateTask` 需用 Outbox 记录 ID 做幂等键，防 Worker 重试重复建任务。
3. **Outbox Worker 形态**：独立 goroutine 轮询 vs 复用现有 worker/scanner 框架（建议复用，与 `AwaitPollWorker`/`RecoveryScanner` 同构）。
4. **Observer 与 service 写 AgentState 的竞争**：用户信号与引擎事件可能并发改同一会话——靠 CAS（红线 2）天然串行化，但需确认重试路径不会重复 Append（靠 `client_msg_id` / 事件去重键）。
5. **只读路径的仓储实例来源**：独立实例（持 `db`）与 UoW 实例（持 `tx`）由同一构造函数产出，注意 `NextSequence` 等「仅限事务」方法在独立实例上的防误用。

## 8. 落地清单
1. `ai-engine/agent/domain/`：5 领域结构 + `OutboxRecord` + 枚举 + 哨兵错误。
2. `ai-engine/agent/repository/`：本文 6 接口（5 Repo + `OutboxRepository`）+ `ConversationUnitOfWork` + `AgentObserver`。
3. `ai-engine/agent/repository/query/`：GORM 实现（domain↔entity 映射、`NextSequence` 行锁、`CompareAndSwap`、部分唯一索引冲突→`ErrDuplicate`）。
4. `ai-engine/agent/service/`：`ConversationService` / `AgentRuntime`（[15](15-agent-decision-engine.md)）/ `AgentObserver` 实现。
5. **Outbox Worker**（红线 4）：消费 `agent_outbox`，提交后投递副作用（先做 `create_task`），失败退避重试；建议复用现有 worker/scanner 框架。
6. wiring：在 `ai-engine/server/server.go` 注册仓储、UoW、Observer（订阅 EventBus）、Outbox Worker，挂 `/agent/*` 路由（[09 §6](09-conversation-api.md#6-落地清单服务端)）。
7. CI 断言：`engine|worker|tool` 包不得 import `agent/repository`（红线 3 的编译期护栏）。

---

返回：[v2/README.md](README.md) · 相关：[09 API](09-conversation-api.md) · [11 状态机](11-agent-state-machine.md) · [12 数据模型](12-data-model-ddl.md) · [04 Agent Runtime](04-agent-runtime.md)
