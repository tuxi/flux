# 11 · Agent 状态机（AgentState）

> 评审反馈点名的缺口：会话只有 `Conversation / Message / Plan / TaskLink`，**少了一个 `AgentState`**——「Agent 当前进行到哪一步」的工作记忆。没有它，会出现典型的失败场景：
>
> > Agent 追问 → 用户离开 → 过一天回来 → Agent **不知道自己问到哪了**。
>
> 本文补上这个一等实体，并定义它的 **9 状态机**、持久化、恢复与并发语义。

## 1. 为什么 Plan 不够，必须有 AgentState

- **Plan 是「已承诺的快照」**：它在槽位**齐备之后**才生成（[04 §4.5](04-agent-runtime.md#45-plan-生成)）。
- **但在槽位齐备之前**，Agent 已经在工作了：识别了意图、收集了一部分槽位、正等用户回答某个追问。**这些「半成品」状态此前无处安放**——这正是评审发现的洞。

```text
用户说目标 ──▶ [收集中：intent 已定，slots 半满，正等 style] ──▶ Plan ──▶ 执行
                ↑
          这段「进行中」的状态 = AgentState（Plan 还不存在）
```

AgentState 是 Agent 的**可持久化工作记忆**：随时落库，重启/断线/隔天回来都能**精确续上**。

| 实体 | 性质 | 生命周期 |
|------|------|---------|
| Message | 事件流，**普通消息 append-only** | append-only |
| ↳ activity 消息 | **唯一例外：可原地更新**（占固定 sequence，content_json 原地改） | append 一次后随事件增长（[16](16-activity-stream.md)） |
| Plan | 已承诺的快照（可多版本） | 槽位齐备后产生 |
| **AgentState** | **可变的当前工作记忆** | 每个会话 1 份，原地更新 |
| TaskLink | 会话↔任务关联 | 每发起一个任务 +1 |

> ⚠️ **实现校正（2026-06）**：原设计「Message 一律不可变」已调整——`kind=activity` 是**唯一允许原地更新**的消息（执行期过程块）。仓储侧由 `MessageRepository.UpdateActivity` 承载，普通消息仍严格 append-only。详见 [17](17-implementation-status-and-roadmap.md) §3。

## 2. AgentState 结构

9 状态模型下，**阻塞原因由 stage 本身编码**（`confirming`/`reviewing`/`awaiting_user` 三个独立态），因此不再需要 `wait_reason`；恢复目标按阻塞态确定性推导（§6 表），也不必存 `resume_to`。结构因此更精简：

```go
type AgentStage string

const (
    StageIdle         AgentStage = "idle"          // 无活跃目标，可继续接新目标
    StageCollecting   AgentStage = "collecting"    // 识别意图 + 填槽位
    StagePlanning     AgentStage = "planning"      // 生成 Plan + 报价
    StageConfirming   AgentStage = "confirming"    // 阻塞：等用户确认方案
    StageExecuting    AgentStage = "executing"     // 任务运行中，翻译进度
    StageReviewing    AgentStage = "reviewing"     // 阻塞：等用户过引擎审核闸门
    StageAwaitingUser AgentStage = "awaiting_user" // 阻塞：等用户答追问（补槽）
    StageCompleted    AgentStage = "completed"     // 某个 goal/plan/task 完成
    StageFailed       AgentStage = "failed"        // 本轮目标失败（可恢复）
)

type AgentState struct {
    ConversationID int64                 // 1:1 会话

    Stage          AgentStage            // 见 §4：9 个状态之一
    PendingMessageID *int64              // 阻塞态：用户当前需要回应的那张卡（clarify/plan_card/review_card）

    Intent         string                // 当前意图（如 short_drama）
    Goal           string                // 用户目标的归一化描述（用于标题/上下文）
    SkillKey       string                // 已选定的 Skill（未选定前为空）

    CollectedSlots map[string]any        // ★ 已收集的槽位（Plan 之前就开始填）
    MissingSlots   []string              // ★ 仍缺的必填槽位（决定还要不要追问）
    DefaultsApplied map[string]any       // 已用默认值的槽位（方案卡里告知“可调整”）

    CurrentPlanID  *int64                // 已生成的 Plan（planning 之后）
    CurrentTaskID  *int64                // 正在执行/审核的任务（executing/reviewing 时）
    LastMessageSeq int64                 // 已处理到的消息 sequence（防重复处理）

    Version        int                   // 乐观锁，防并发写覆盖
    UpdatedAt      time.Time
}
```

> `CollectedSlots`/`MissingSlots` 与 `Plan.slots_json` 的关系（**关键、消除重复**）：
> - **AgentState 持有「进行中」的槽位**（collecting/awaiting_user 阶段，Plan 尚不存在）。
> - 槽位齐备、`buildPlan` 时，把它们**快照**进 `Plan.slots_json`（已承诺）。
> - 之后若用户改参数 → 更新 AgentState → 生成新 Plan 版本。
> - 一句话：**AgentState = 草稿，Plan = 定稿。**

## 3. 持久化

### 3.1 `agent_states`（当前快照，1:1 会话）
| 字段 | 类型 | 说明 |
|------|------|------|
| `conversation_id` | bigint PK | 1:1 |
| `stage` | varchar(20), index | 当前阶段（9 值之一） |
| `pending_message_id` | bigint, null | 待回应卡 |
| `intent` / `goal` / `skill_key` | varchar | 当前目标 |
| `collected_slots` / `missing_slots` / `defaults_applied` | jsonb | 槽位工作记忆 |
| `current_plan_id` / `current_task_id` | bigint, null | 锚点 |
| `last_message_seq` | bigint | 处理游标 |
| `version` | int | 乐观锁 |
| `updated_at` | timestamptz | |

> 建表 DDL 与 GORM Entity 见 [12 · 数据模型](12-data-model-ddl.md)。独立表（而非塞进 `conversations`）的价值：可对 `stage` 建索引，批量检索「卡在 confirming/awaiting_user/reviewing 的会话」做主动唤醒/提醒（§9）。

### 3.2 `agent_state_transitions`（可选·审计日志）
append-only 记录每次状态跃迁（`from_stage, to_stage, trigger, at`），用于调试与「Agent 为什么卡住」的可观测性。第一版 MAY 省略，但强烈建议保留——它是排查 Agent 行为的黑匣子。

## 4. 9 个状态（按语义分组）

> 顺序对应评审给定的枚举：`idle / collecting / planning / confirming / executing / awaiting_user / reviewing / completed / failed`。下面按语义归三类，便于理解。

**A. 工作态（Agent 在干活，不阻塞用户）**
| 状态 | 含义 |
|------|------|
| `idle` | 无活跃目标，等用户表达（初始 / 上一目标交付后的稳定态） |
| `collecting` | 识别意图 + 抽取/补全槽位 |
| `planning` | 生成 Plan + 调 Quote 报价 |
| `executing` | 任务在引擎里跑，Agent 翻译进度 |

**B. 阻塞态（等用户操作，stage 即原因）**
| 状态 | 阻塞于 | 唤醒后回到 | 对应卡片 |
|------|--------|-----------|---------|
| `awaiting_user` | 等用户**答追问/补槽** | `collecting` | clarify |
| `confirming` | 等用户**确认方案/报价** | `executing` | plan_card |
| `reviewing` | 等用户**过引擎审核闸门**（如分镜确认） | `executing` | review_card |

**C. 目标终态（本轮目标结束，会话仍可继续）**
| 状态 | 含义 |
|------|------|
| `completed` | 某个 goal/plan/task 完成（展示成品卡，停在此） |
| `failed` | 本轮目标失败（可重试/换方案/fork 恢复） |

### 4.1 为什么 `idle` 与 `completed` 不合并（评审已定调，记录于此）
> `completed` = 上一个目标**已完成**；`idle` = 当前会话**没有活跃目标但仍可继续**。二者语义不同：
> - 短剧生成完（`completed`，停在成品卡），用户说「再做个带货视频」→ 进入 `collecting` 开新目标，**而不是**从一个语义含糊的「完成又重新开始」态出发；
> - `completed` 偏「某次交付的终态」，但 Agent 会话不因此终止；
> - `idle` 是**多轮多目标会话的稳定态**，让「继续创作 / 再做一个 / 改一下」都自然挂载。
>
> 因此**两者都是「等下一句」的休息态，但携带不同语义**，不可合并。

## 5. 状态机图

```text
                         ┌──────────────────────────────────────────────┐
   (新会话)               │ 用户新消息(新目标/换话题)                       │
  ─────────────▶  ┌────────┐                                            │
                  │  idle  │◀──────────────────────────────┐           │
                  └───┬────┘  目标明确                       │ (上一目标 │
                      │                                     │  已交付,  │
                      ▼                                     │  无新输入)│
                 ┌─────────────┐   缺必填槽   ┌───────────────┐         │
        ┌───────▶│ collecting   │────────────▶│ awaiting_user  │         │
        │        └──────┬──────┘◀────────────│  (补槽 clarify) │         │
        │ 用户改参数      │ 槽位齐备  用户答槽   └───────────────┘         │
        │        ┌──────▼──────┐                                       │
        │        │  planning    │                                       │
        │        └──┬───────┬──┘                                       │
        │   需确认   │       │ 免确认(轻量Skill)直跑                       │
        │           ▼       │                                          │
        │     ┌────────────┐│                                          │
        └─────│ confirming │││                                          │
              └─────┬──────┘│ confirm_plan                              │
                    │       │                                          │
                    ▼       ▼                                          │
                 ┌──────────────┐  引擎 await 闸门  ┌───────────────┐    │
                 │  executing    │─────────────────▶│  reviewing     │    │
                 │               │◀────signal───────│ (审核闸门)      │    │
                 └──┬─────────┬──┘  引擎继续         └───────────────┘    │
       task成功      │         │ task失败                                 │
                    ▼         ▼                                         │
              ┌───────────┐ ┌────────┐ 重试/换方案                        │
              │ completed │ │ failed │──────────▶ planning / collecting   │
              └─────┬─────┘ └────────┘                                   │
                    └────────────────────────────────────────────────────┘
                                 (停在成品卡；下一条用户消息→collecting，或settle→idle)
```

## 6. 跃迁表（from → trigger → to → 副作用）

| From | Trigger | To | 副作用 |
|------|---------|----|--------|
| idle | 用户消息含可识别目标 | collecting | 置 `intent`，开始抽槽 |
| idle | 寒暄/能力询问 | idle | 回 text，不建目标 |
| collecting | 抽到意图但**缺必填槽** | awaiting_user | 写 clarify 卡，置 `pending_message_id` |
| collecting | **槽位齐备** | planning | 写 Plan（snapshot CollectedSlots → slots_json） |
| awaiting_user | 用户答槽（signal/msg） | collecting | 合并槽位，清 pending，重判缺口 |
| planning | Skill `needs_plan_confirmation=true` | confirming | 写 plan_card（带 Quote），置 `pending_message_id` |
| planning | 免确认（轻量 Skill） | executing | 直接 act |
| confirming | `confirm_plan` | executing | 建 Task + TaskLink，置 `current_task_id`，conv→running |
| confirming | 用户改参数 | planning | 更新槽位 → 新 Plan 版本 |
| executing | 引擎 `await_user_action` | reviewing | 写 review_card，置 `pending_message_id` |
| reviewing | 用户确认（signal） | executing | `routed_to=engine`，转 `await.HandleSignal`，引擎继续 |
| executing | `task_succeeded` | completed | 写 result_card，conv→completed |
| executing | `task_failed`/`final_failed` | failed | 写 error_card（含退款告知），conv→active |
| completed | 用户「改一下/再来一版」 | collecting | 新一轮（修改→走 fork，见 [04 §7](04-agent-runtime.md#7-迭代式修改--复用-forkpatch)） |
| completed | 用户「换个新东西」 | collecting | 新一轮（新 intent） |
| completed | 用户「结束了/谢谢」 | idle | 结束本目标，回稳定态 |
| failed | 用户「重试/换方案」 | planning / executing | 走 resume/fork 或重选 Skill |
| failed | 用户新消息（其他目标） | collecting | 新一轮 |
| 任意阻塞态 | 用户「算了，换个事」 | collecting | 清 pending，重置目标相关字段，保留历史 Message |

**确定性恢复目标**（替代 `resume_to` 字段）：`awaiting_user → collecting`、`confirming → executing`、`reviewing → executing`。

> **为什么第一版不存 `wait_reason` / `resume_to`**：9 状态模型下，「在等什么」已由 stage 自身编码（`awaiting_user`=等补信息、`confirming`=等确认方案、`reviewing`=等审核结果），恢复目标又是确定性的（上表）。再加这两个字段只会让 AgentState 变胖，且容易出现 stage 与 reason 不一致。**如未来出现同一阻塞态下存在多条恢复路径（例如 `confirming` 之后可能跳 `reviewing` 而非 `executing`），再引入 `resume_to`。** 在此之前不保留。

## 7. AgentState.stage ↔ Conversation.status 映射

两者是**不同粒度**，不矛盾：`AgentState.stage`（9 值）是 Agent 内部细粒度位置；`Conversation.status`（5 值）是给列表 UI 的粗粒度投影（[03 §3](03-conversation-layer.md#3-conversation-状态机)）。

| AgentState.stage | Conversation.status | 列表 UI 呈现 |
|------------------|---------------------|-------------|
| idle / collecting / planning | `active` | 进行中 |
| awaiting_user / confirming / reviewing | `awaiting_user` | 待你确认 |
| executing | `running` | 创作中… |
| completed | `completed` | 已完成 |
| failed | `active` | 进行中（上轮失败，可继续） |
| （归档操作） | `archived` | 已归档 |

> 实现：每次 AgentState 跃迁后，按本表用一个纯函数同步 `Conversation.status`，保证两者一致。列表页只读 `status`；会话详情读完整 `agent_state`（[09 §2.3](09-conversation-api.md#23-会话详情)）。三个阻塞态统一投影为 `awaiting_user`，因为对用户而言它们都是「该你操作了」。

## 8. 恢复语义（「过一天回来」）

这是 AgentState 存在的根本理由。客户端重新进入会话时：

```text
GET /conversations/:id
  → 读 agent_state.{stage, pending_message_id, missing_slots}
  → if stage ∈ {awaiting_user, confirming, reviewing}:   // 三个阻塞态
        客户端直接定位渲染 pending_message_id 这张卡，
        用户接着回答即可——Agent“记得问到哪了”
  → if stage == executing:
        WS 订阅 + 拉最新进度，继续看进度
  → if stage ∈ {completed, failed}:
        展示成品卡 / 失败卡 + 后续动作（再来一版 / 重试）
  → if stage == idle:
        干净的输入态，等用户说下一个目标
```

服务端侧，Agent Loop 是**无状态处理器**：每次被唤醒（用户消息 / 引擎事件）都先**从 `agent_states` 重新水合（rehydrate）**当前状态，再决策。因此进程重启、负载迁移、隔天回来都**等价**——状态在库里，不在内存。

- `last_message_seq` 防重复处理：只处理 `sequence > last_message_seq` 的消息。
- `version` 乐观锁：并发写（用户消息与引擎事件几乎同时到）时，CAS 失败方重读重试，避免覆盖。

## 9. 并发与边界

- **一个会话，一个 AgentState**：即便会话下有多个 Task（主任务 + 多个 fork），AgentState 的 `current_task_id` 指向「当前用户正在关注/操作」的那个；其余任务的进度仍照常翻译为 message，但不改变主 stage 的语义。
- **执行中收到新用户消息**：若是「修改/追加」→ 进入新一轮（fork）；若是闲聊 → 回 text 不改 stage；若要求中止 → 走现有 Cancel。规则由 Agent Loop 判定，AgentState 记录结果。
- **阻塞态超时**：扫描器可发现长期处于 `confirming/awaiting_user/reviewing` 的会话，主动推送提醒（「你的短剧还差一步确认～」）——这正是把 `stage` 建索引、独立成表的价值（§3.1）。
- **幂等**：同一 signal 重复提交（弱网重发）→ 依据 `pending_message_id` 已被消费则返回 `SIGNAL_STALE`（[09 §5](09-conversation-api.md#5-错误码表rest-code)），不重复推进。

## 10. 一次完整轨迹（状态视角）

```text
stage         事件                                    关键字段
─────────────────────────────────────────────────────────────────
idle          POST /conversations "做个都市爱情短剧"
collecting    识别 intent=short_drama，抽到 style       intent=short_drama
                                                       collected={style:urban_romance}
                                                       missing=[idea]
awaiting_user 缺 idea → 写 clarify                     pending_message_id=5567
collecting    用户答 "程序员转行开咖啡馆"                 collected={style,idea}; missing=[]
planning      buildPlan + Quote=320                    current_plan_id=100234
confirming    写 plan_card 等确认                       pending_message_id=5570
executing     confirm_plan → 建 Task                   current_task_id=70010
                                                       (conv.status=running)
reviewing     引擎到 storyboard 闸门                    pending_message_id=5588
executing     用户确认分镜 → 引擎继续                    (routed_to=engine)
completed     task_succeeded → result_card             (conv.status=completed)
collecting    用户 "第二幕改成夜晚"                      新一轮：is_modification=true
              → PatchPreview → 修改预览卡 → Fork        relation=fork, current_task_id=70021
```

## 11. 落地清单
1. `agent_states` 表 + 仓储（CAS 更新）；可选 `agent_state_transitions` 审计。DDL/Entity 见 [12](12-data-model-ddl.md)。
2. Agent Loop 改为「**每次唤醒先 rehydrate AgentState**，决策后 CAS 落库 + 同步 Conversation.status」。
3. `GET /conversations/:id` 返回 `agent_state`（[09 §2.3](09-conversation-api.md#23-会话详情)）。
4. 扫描器（可选）：发现长期阻塞态会话做主动提醒（复用现有 worker/scanner 范式）。

---

返回：[v2/README.md](README.md) · 相关：[03 数据模型](03-conversation-layer.md) · [04 Agent Runtime](04-agent-runtime.md) · [09 API](09-conversation-api.md) · [12 DDL](12-data-model-ddl.md)
