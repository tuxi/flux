# 03 · Conversation 层设计

> 本层是**状态层，不含智能**。它只负责持久化「对话、消息、计划、会话↔任务关联」，并通过 API/WS 把这些暴露给客户端与 Agent Runtime。所有决策逻辑在 [04 Agent Runtime](04-agent-runtime.md)。

## 1. 实体总览

```text
Conversation 1 ──── N Message
     │ 1
     ├──── 1 AgentState（当前工作记忆：进行到哪一步）   ← 见文档 11
     │ 1
     ├──── 1 Plan（当前计划，可被更新成新版本）
     │ 1
     └──── N TaskLink ──── 1 Task（现有引擎实体，不改）
```

- **Conversation**：一次创作过程（围绕一个目标的完整对话）。
- **Message**：会话里的一条消息（用户/Agent/系统/进度/结果）。
- **AgentState**：Agent「当前进行到哪一步」的**可变工作记忆**——意图、草稿态槽位（Plan 之前就开始填）、等待原因、恢复锚点。它解决「追问到一半用户离开、隔天回来 Agent 不知道问到哪」的问题。完整定义见 [11 · Agent 状态机](11-agent-state-machine.md)。
- **Plan**：Agent 对当前创作的结构化计划（意图 + 槽位 + 所选技能 + 阶段）——**已承诺的快照**，在槽位齐备后由 AgentState 快照而来。
- **TaskLink**：Conversation 与 Workflow `Task` 的关联（一对多；一个会话可发起多个任务，如初次生成 + 多次 fork）。

> **AgentState vs Plan（消除职责重叠）**：槽位在 *齐备之前* 的「草稿态」活在 **AgentState**（`CollectedSlots/MissingSlots`）；齐备后快照进 **Plan**（`slots_json`，定稿）。即 **AgentState=草稿，Plan=定稿**。这填补了原设计「Plan 生成前部分槽位无处安放」的空缺。本文 §2.3 的 `slots_json` 仅指**定稿**。

## 2. 数据模型

> 表名建议放在 `public` schema，与现有风格一致。下方为字段设计，落地时按现有 GORM entity 约定建模。**不修改现有 `tasks` 表**——仅通过 `conversation_task_links` 外联。

### 2.1 `conversations`

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | bigint PK (snowflake) | 会话 ID |
| `user_id` | bigint, index | 归属用户（匿名账号同样适用） |
| `title` | varchar(128) | 会话标题（Agent 依据首条消息自动生成，可改） |
| `status` | varchar(20), index | 见 §3 状态机：`active` / `awaiting_user` / `running` / `completed` / `archived` |
| `intent` | varchar(64), index, null | 当前主意图（如 `short_drama`），便于检索/统计 |
| `current_plan_id` | bigint, null | 指向当前 `plans.id` |
| `last_message_seq` | bigint | 最新消息 sequence（增量恢复用） |
| `entry` | varchar(32) | 入口来源：`home` / `suggestion` / `marketplace` / `work_continue` |
| `created_at` / `updated_at` | timestamptz | |
| `deleted_at` | timestamptz, null | 软删 |

### 2.2 `conversation_messages`

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | bigint PK | |
| `conversation_id` | bigint, index | |
| `sequence` | bigint, index | **会话内单调递增**（断线增量恢复的游标，仿 `TaskEvent.Sequence`） |
| `role` | varchar(16) | `user` / `agent` / `system` |
| `kind` | varchar(24), index | 见 §4 消息类型 |
| `text` | text, null | 文本内容（user 文本 / agent 话术） |
| `content_json` | jsonb, null | 结构化负载（卡片、Plan 摘要、附件、进度、结果引用…） |
| `task_id` | bigint, null, index | 关联任务（进度/结果类消息） |
| `reply_to` | bigint, null | 回复/对应的上一条消息（如审核回答对应审核卡） |
| `created_at` | timestamptz | |

> `kind + content_json` 是客户端渲染的核心：客户端按 `kind` 选渲染器，按 `content_json` 填内容（[06](06-client-architecture.md#3-agent-feed-渲染协议)）。

### 2.3 `conversation_plans`

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | bigint PK | |
| `conversation_id` | bigint, index | |
| `version` | int | 计划版本（每次重大修订 +1） |
| `intent` | varchar(64) | 识别出的意图 |
| `skill_key` | varchar(64), null | 选定的 Skill（= workflow name / route_key+mode_key） |
| `slots_json` | jsonb | 槽位填充结果（已填 + 缺失 + 默认值），见 §4.3 |
| `stages_json` | jsonb, null | 可见的阶段列表（用于「方案卡 / 进度卡」），来自 Skill Manifest |
| `estimated_cost` | numeric, null | 预计积分消耗（来自 Quote） |
| `status` | varchar(20) | `draft` / `confirmed` / `executing` / `done` / `revised` |
| `created_at` | timestamptz | |

> Plan 是**会话与引擎之间的中间表示**。第一版是浅结构；未来演进为可执行 Blueprint（[08](08-roadmap-and-milestones.md#blueprint-演进)），字段向后兼容地扩展。

### 2.4 `conversation_task_links`

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | bigint PK | |
| `conversation_id` | bigint, index | |
| `task_id` | bigint, index | 关联现有 `tasks.id` |
| `plan_id` | bigint, null | 由哪个 Plan 发起 |
| `relation` | varchar(20) | `primary`（主产出）/ `fork`（迭代版）/ `sub`（子能力） |
| `forked_from_task_id` | bigint, null | 迭代来源（与 `tasks.forked_from` 呼应） |
| `created_at` | timestamptz | |

> 通过 TaskLink，一个 Conversation 天然支持「初版 + 多个迭代版」的版本树——客户端「作品」Tab 与会话内「历史版本」都从这里取。

## 3. Conversation 状态机

```text
        创建
         │
         ▼
     ┌────────┐  缺信息/需确认   ┌──────────────┐
     │ active │────────────────▶│ awaiting_user │
     └───┬────┘◀────────────────└──────────────┘
         │ 启动任务         用户回复/确认
         ▼
     ┌─────────┐  任务完成    ┌───────────┐  用户长期不动/手动
     │ running │────────────▶│ completed │──────────────────▶ archived
     └────┬────┘             └─────┬─────┘
         │ 任务中途到 await 闸门    │ 用户「再改改」
         ▼                        ▼
   awaiting_user              active（新一轮，走 fork）
```

- `active`：可接收用户消息，Agent 在思考/可发起任务。
- `awaiting_user`：Agent 在等用户输入（追问 / 审核卡 / 方案确认）——对应引擎 `await` 或 Agent 主动追问。
- `running`：有任务在执行，Agent 在翻译进度。
- `completed`：本轮交付完成；仍可继续对话（触发新一轮 → 回 `active`）。
- `archived`：归档（用户操作或长期不活跃）。

> 注意：会话状态与 Task 状态**解耦**。一个会话可能有多个任务并行/串行；会话状态是「面向用户的对话状态」，由 Agent Runtime 维护。

## 4. 消息类型（`kind`）

客户端只认 `kind` 与 `content_json`，与底层引擎细节无关。

| `kind` | role | 含义 | `content_json` 关键字段 |
|--------|------|------|------------------------|
| `text` | user/agent | 纯文本 | — |
| `user_attachment` | user | 用户上传素材 | `assets: [{asset_id,url,type}]` |
| `clarify` | agent | 追问（补槽位） | `question`, `options?[]`, `slot`, `multi?` |
| `plan_card` | agent | 创作方案卡（确认闸门） | `plan_id`, `intent`, `slots`, `stages[]`, `estimated_cost`, `confirm_action` |
| `review_card` | agent | 会话内审核卡（来自 await） | `card_type`, `payload`(分镜/帧/音色…), `signal`, `actions[]` |
| `progress` | agent | 进度（翻译后的 TaskEvent） | `task_id`, `stage`, `percent`, `note` |
| `result_card` | agent | 成品卡 | `task_id`, `result_type`, `primary_file_url`, `cover_url`, `actions[]`(再来一版/改/发布) |
| `error_card` | agent | 失败 + 建议 | `task_id`, `reason_user`, `recoverable`, `actions[]`(重试/换方案) |
| `system` | system | 系统提示（积分不足、风控…） | `code`, `message` |

### 4.1 `clarify`（追问）示例

```json
{
  "kind": "clarify",
  "role": "agent",
  "text": "想要几个角色？是都市爱情还是悬疑风格？",
  "content_json": {
    "slot": "style",
    "question": "选择剧情风格",
    "options": [
      {"label": "都市爱情", "value": "urban_romance"},
      {"label": "悬疑", "value": "suspense"},
      {"label": "搞笑", "value": "comedy"}
    ],
    "multi": false
  }
}
```

### 4.2 `plan_card`（方案确认）示例

```json
{
  "kind": "plan_card",
  "role": "agent",
  "text": "我打算这样做，确认就开始 👇",
  "content_json": {
    "plan_id": 100234,
    "intent": "short_drama",
    "slots": {"duration_sec": 60, "characters": 2, "style": "urban_romance", "aspect_ratio": "9:16"},
    "stages": ["规划剧情", "生成分镜", "逐镜生成画面", "配音", "合成成片"],
    "estimated_cost": 320,
    "confirm_action": {"type": "signal", "signal": "confirm_plan", "label": "开始创作"}
  }
}
```

### 4.3 `slots_json`（Plan 内部）示例

```json
{
  "filled": {"duration_sec": 60, "style": "urban_romance"},
  "defaults_applied": {"aspect_ratio": "9:16", "voice": "default_female"},
  "missing_required": [],
  "missing_optional": ["bgm_mood"]
}
```

## 5. WebSocket 协议

复用现有 `WSHub`，新增一种 **conversation room**（room key = `conv:{conversation_id}`）。Agent Runtime 把新消息既落库（带 `sequence`）又通过 WSHub 推送，与现有 TaskEvent 推送同构。

### 5.1 连接与订阅

```text
WS  /api/v1/ai/ws   (复用现有 ws 鉴权)
→ client: {"op":"subscribe","room":"conv:100200"}
→ client(可选断线恢复): {"op":"subscribe","room":"conv:100200","after_sequence": 57}
```

### 5.2 服务端下行帧

```json
{ "op": "message", "room": "conv:100200", "data": { /* 一条 conversation_message 的完整 JSON */ } }
```

- **每条新消息**（含进度 `progress`）= 一帧 `message`。
- 客户端按 `sequence` 去重/排序；断线后用 `after_sequence` 拉缺口（HTTP 兜底，见 §6）。
- 进度类消息（`progress`）可按 `grade=transient` 处理：高频、可不全部入库（与现有 TaskEvent 分层一致）；关键里程碑（阶段切换、审核、结果）入库为 persistent。

> 复用现有事件分层经验（`transient/persistent/audit` + `sequence`，见 `domain/task.go`）：进度刷屏走 transient 只推 WS，里程碑走 persistent 入库可回放。客户端的断线恢复体验与现有任务页一致。

## 6. HTTP API（`/api/v1/ai/agent`）

> 鉴权复用现有 `ValidateAuth`，挂在 `protected` 组下（匿名账号亦可，按 `user_id` 隔离，与 `/user/works` 一致）。

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/agent/conversations` | 新建会话（可带首条消息 + 附件 + entry） |
| GET | `/agent/conversations` | 我的会话列表（分页） |
| GET | `/agent/conversations/:id` | 会话详情（含 plan 摘要 + 关联任务） |
| GET | `/agent/conversations/:id/messages?after_sequence=` | 增量拉消息（断线恢复 / 首次进入） |
| POST | `/agent/conversations/:id/messages` | 发送用户消息（文本/附件）→ 触发 Agent Loop |
| POST | `/agent/conversations/:id/signals` | 用户对卡片的回应（确认方案/审核选择）→ 转译为引擎 signal 或 Agent 决策 |
| POST | `/agent/conversations/:id/archive` | 归档 |
| PATCH | `/agent/conversations/:id` | 改标题等 |

### 6.1 发送消息（核心入口）

```http
POST /api/v1/ai/agent/conversations/100200/messages
{
  "text": "把第二幕改成夜晚",
  "attachments": [],
  "client_msg_id": "uuid-去重用"
}
→ 200 { "message": { ...回显的 user message... }, "accepted": true }
```

> 该接口**立即返回**（仅落库 user message + 唤醒 Agent Loop）。Agent 的后续回应（追问/方案/进度/结果）全部经 **WS 异步推送**——与现有任务执行的「先建任务后流式回传」一致，客户端无需轮询。

### 6.2 用户信号（确认/选择）

```http
POST /api/v1/ai/agent/conversations/100200/signals
{
  "signal": "confirm_plan",          // 或 "confirm_storyboard_prompt" 等
  "ref_message_id": 5567,            // 对应哪张卡
  "payload": { "...": "..." }        // 选项/编辑结果
}
```

Agent Runtime 收到后：若该 signal 对应**引擎 await**（如分镜确认），则转发到现有 `await.HandleSignal`；若是**会话级确认**（如方案确认），则 Agent 自行推进到 Act。两种情况对客户端表现一致。详见 [04](04-agent-runtime.md#6-用户信号的两种归宿)。

## 7. 与现有「作品 / 任务」体系的衔接

- **作品延续**：「作品」Tab 里点某个 Task → 若存在 TaskLink，则回到原 Conversation；若是 V1 老任务（无会话），则**新建一个会话并以该任务为上下文**（「在这个作品基础上继续」）。
- **会话内版本树**：同一 Conversation 下的 `primary` + 多个 `fork` TaskLink，构成可切换的版本历史。
- **不破坏旧接口**：`/user/works`、`/ai/tasks/*`、`/templates/tasks`、`/tools/tasks` 全部保留。Agent 只是它们之上的新发起者。

## 8. 本层不负责的事（边界自检）

- ❌ 不调用 LLM、不做意图识别（那是 Agent Runtime）。
- ❌ 不直接操作 DAG / Task 执行（通过 Skill Layer）。
- ❌ 不存储引擎内部状态（NodeRuntime/checkpoint 仍归引擎）。
- ✅ 只做：会话/消息/计划/关联的 CRUD + 增量推送。

---

下一篇：[04 · Agent Runtime 设计](04-agent-runtime.md) —— 大脑：意图、槽位、计划、技能选择、进度翻译。
