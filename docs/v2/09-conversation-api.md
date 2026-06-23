# 09 · Conversation API 设计（实现级契约）

> 本文是 **可直接实现的接口契约**。[03](03-conversation-layer.md) 给的是数据模型与设计动机；本文给的是「后端照着写、客户端照着接」的精确 REST + WS 规格。两者如有出入，**以本文的字段/状态码为准**，并回填 03。

## 0. 约定（Conventions）

### 0.1 基础
- **Base path**：`/api/v1/ai/agent`（挂在现有 `protected` 组下，复用 `ValidateAuth`；匿名账号亦可，按 `user_id` 隔离，与 `/user/works` 一致）。
- 用户口语里的 `POST /conversations` = 本文 `POST /api/v1/ai/agent/conversations`，下同。
- **Content-Type**：`application/json; charset=utf-8`。

### 0.2 响应信封（复用现有 `pkg/response`）
所有 REST 响应统一为 `dto.ApiResponse`：

```json
{ "trace_id": "abc123", "code": 0, "message": "success", "data": { /* 业务数据 */ } }
```

- `code == 0` 表示成功（`response.Success`）。非 0 为业务错误码（`response.Error`）。
- 鉴权失败 HTTP 401（`code:401`）；越权 HTTP 403（`code:403`）；校验失败 HTTP 400。
- **本文各接口的 `data` 即下文「响应」描述的对象。**

### 0.3 ID 序列化
- 所有新实体 ID（conversation/message/plan/task_link）为 snowflake `int64`，**在 JSON 中序列化为 string**，避免 JS/Swift 的 53-bit 精度丢失。
- `task_id` 沿用引擎现状（数值）；客户端在 agent 域内统一**按字符串处理**更安全。

### 0.4 时间与游标
- 时间：Unix 秒（int64），字段后缀 `_at`。
- **`sequence`**：会话内单调递增整数，是断线增量恢复的唯一游标（仿 `TaskEvent.Sequence`）。客户端持久化 `last_sequence`。

### 0.5 幂等
- 所有「写」请求支持 `client_msg_id`（UUID）。同一 `(conversation_id, client_msg_id)` 重复提交返回首次结果，不重复执行。

## 1. REST 接口总览

| 方法 | 路径 | 作用 | 触发 Agent |
|------|------|------|:---:|
| POST | `/conversations` | 新建会话（可带首条消息） | ✅ |
| GET | `/conversations` | 我的会话列表（分页） | |
| GET | `/conversations/:id` | 会话详情（含 agent_state + plan + 关联任务） | |
| PATCH | `/conversations/:id` | 改标题 | |
| POST | `/conversations/:id/archive` | 归档 | |
| GET | `/conversations/:id/messages` | 拉消息（首次 / 断线增量） | |
| POST | `/conversations/:id/messages` | 发送用户消息 | ✅ |
| POST | `/conversations/:id/signals` | 卡片回应（确认/选择/审核） | ✅ |

> 写接口（发消息/信号）**立即返回回执**，Agent 的后续产出（追问/方案/进度/结果）全部经 **WS 异步推送**（§3）。这与现有「先建任务、后流式回传」完全一致——客户端不轮询。

## 2. REST 接口详述

### 2.1 新建会话
```http
POST /api/v1/ai/agent/conversations
{
  "entry": "home",                       // home | suggestion | marketplace | work_continue
  "client_msg_id": "uuid",
  "first_message": {                      // 可选；带上则等价于建会话后立刻发一条 user 消息
    "text": "帮我做个一分钟都市爱情短剧",
    "attachments": [
      { "asset_id": "9988", "type": "image", "url": "https://..." }
    ]
  },
  "context_task_id": "70001"             // 可选；work_continue 时携带，作为「基于此作品继续」的上下文
}
```
**响应 `data`**：
```json
{
  "conversation": {
    "id": "100200", "title": "都市爱情短剧", "status": "active",
    "intent": null, "current_plan_id": null, "last_sequence": 1,
    "entry": "home", "created_at": 1733200000, "updated_at": 1733200000
  },
  "messages": [ /* 若带 first_message，则含回显的 user 消息（sequence=1） */ ]
}
```
- 带 `first_message` 时：同步落库 user 消息并**唤醒 Agent Loop**；Agent 的回应随后走 WS。
- 客户端拿到 `conversation.id` 后应立即 **WS 订阅** `conversation:100200`（§3）。

### 2.2 会话列表
```http
GET /api/v1/ai/agent/conversations?status=active&cursor=&limit=20
```
- `status` 可选过滤：`active|awaiting_user|running|completed|archived`。
- **游标分页**：响应含 `next_cursor`（空串表示到底）。
- **响应 `data`**：`{ "items": [ {conversation 摘要 + last_message_preview} ], "next_cursor": "" }`。

### 2.3 会话详情
```http
GET /api/v1/ai/agent/conversations/100200
```
**响应 `data`**：
```json
{
  "conversation": { ...同 2.1... },
  "agent_state": {                        // 见文档 11；客户端用它决定「现在该让用户做什么」
    "stage": "awaiting_user",             // 9 值之一；三个阻塞态：awaiting_user(补槽)|confirming(确认方案)|reviewing(审核闸门)
    "pending_message_id": "5567",         // 阻塞态：用户当前需要回应的那张卡
    "intent": "short_drama",
    "skill_key": "short_drama",
    "collected_slots": {"duration_sec": 60},
    "missing_slots": ["style"]
  },
  "current_plan": { ...见 2.7 plan 对象... },
  "task_links": [
    { "id":"1","task_id":"70010","plan_id":"100234","relation":"primary","forked_from_task_id":null,"created_at":1733200100 }
  ]
}
```
> `agent_state` 让「过一天回来」的客户端**一眼知道当前该渲染哪张待办卡**，无需重放整段历史。

### 2.4 改标题 / 归档
```http
PATCH  /conversations/100200      { "title": "新的标题" }
POST   /conversations/100200/archive
```
归档后 `status=archived`；归档会话只读，发消息返回 `409 CONVERSATION_ARCHIVED`。

### 2.5 拉消息（首次进入 / 断线增量）
```http
GET /api/v1/ai/agent/conversations/100200/messages?after_sequence=57&limit=100
```
- `after_sequence` 省略 = 从头拉（首次进入会话）。
- 只返回 `sequence > after_sequence` 的消息，**按 sequence 升序**。
- **响应 `data`**：`{ "items": [ <message> ... ], "last_sequence": 88, "has_more": false }`。
- 这是 WS 的 **HTTP 兜底补偿源**（与现有任务 `GET /tasks/:id` 快照同义，见 [WS 恢复规范](../../../docs/workflow_ws_subscription_recovery_spec.md)）。

`<message>` 对象（与 [03 §4](03-conversation-layer.md#4-消息类型kind) 一致）：
```json
{
  "id": "5567", "conversation_id": "100200", "sequence": 58,
  "role": "agent", "kind": "clarify",
  "text": "想要什么风格？", "content_json": { ... },
  "task_id": null, "reply_to": null, "created_at": 1733200080
}
```

### 2.6 发送用户消息（核心入口）
```http
POST /api/v1/ai/agent/conversations/100200/messages
{
  "client_msg_id": "uuid",
  "text": "第二幕改成夜晚",
  "attachments": []
}
```
**响应 `data`**（**立即返回**，仅回显 user 消息 + 受理）：
```json
{ "message": { "id":"5570","sequence":59,"role":"user","kind":"text","text":"第二幕改成夜晚", ... }, "accepted": true }
```
- 服务端动作：落库 user 消息 → 唤醒 Agent Loop → **后续 agent 消息走 WS**。
- 若会话正 `executing` 且不接受打断的阶段，Agent 可回一条 `system` 消息说明（仍 200，不阻塞）。

### 2.7 用户信号（卡片回应）
用于「确认方案 / 选择追问项 / 审核确认」。**两种归宿、对客户端一致**（见 [04 §8](04-agent-runtime.md#8-用户信号的两种归宿)）：

```http
POST /api/v1/ai/agent/conversations/100200/signals
{
  "client_msg_id": "uuid",
  "ref_message_id": "5567",          // 对应哪张卡（clarify/plan_card/review_card）
  "signal": "confirm_plan",          // 卡片 content_json 里声明的 signal 名
  "payload": { "...": "..." }         // 选项值 / 编辑结果 / 审核动作
}
```
**响应 `data`**：`{ "accepted": true, "routed_to": "agent" }`，`routed_to ∈ {agent, engine}`：

| `routed_to` | 含义 | 服务端做了什么 |
|-------------|------|---------------|
| `engine` | 该 signal 对应**引擎 await 闸门**（如分镜确认） | 取该会话 `current_task_id` 作为 `callback_token`，转调现有 `await.HandleSignal({signal_name, callback_token, payload})`（见 `ai-engine/handler/await_handler.go`），引擎继续执行 |
| `agent` | 会话级决策（如 `confirm_plan`） | Agent Loop 自行推进到 Act / Fork，不碰引擎 await |

> 客户端无需知道 `routed_to` 的差异，它只管「点了卡上的按钮 → POST signal → 等 WS 后续」。`routed_to` 仅用于可观测/调试。

`plan` 对象（出现在 `plan_card.content_json` 与会话详情 `current_plan`）：
```json
{
  "id":"100234","version":1,"intent":"short_drama","skill_key":"short_drama",
  "slots":{"duration_sec":60,"style":"urban_romance","characters":2,"aspect_ratio":"9:16"},
  "stages":["规划剧情","生成分镜","逐镜生成画面","配音","合成成片"],
  "estimated_cost":320,"status":"draft"
}
```

## 3. WebSocket 流式协议

**完全复用现有 `WSHub` 与其订阅/ack/恢复协议**（见 [WS 订阅恢复规范](../../../docs/workflow_ws_subscription_recovery_spec.md)）。对客户端而言，只是在已支持的 `type: "task"` 之外**新增一种 `type: "conversation"`**，行为同构 → 客户端改动极小。

> ⚠️ **实现校正（以代码为准，2026-06；详见 [17](17-implementation-status-and-roadmap.md) §3）**：本 §3 的部分恢复能力是**设计目标，当前未实现**。实际 `WSHub`（`ai-engine/websocket/hub.go`）：
> - **已实现**：`{action,type:"conversation",id}` 订阅 + ownership 校验 + `subscription_ack(ok/error_code)`；下行 `type:"message"` 帧带整条消息（§3.4 的 message 帧形态准确）。
> - **未实现**：subscribe 的 `after_sequence` 补推（§3.2）、ack 里的 `last_sequence`（§3.3）、`type:"state"` / agent_state 帧（§3.4）、帧级 event_seq / replay（§3.5）。
> - **进度不再是 transient `progress` 帧**：已改为持久可变的 `activity` 消息（[16](16-activity-stream.md)），同样走 `type:"message"`，客户端**按 message.id 原地 upsert**。
> - **实际恢复策略**：断线/重连/进详情时走 REST 兜底——`GET /messages?after_sequence` 补增量 + 整窗重拉刷新 activity 累积态 + `GET /conversations/:id` 补 agent_state。

### 3.1 连接
复用现有 `GET /api/v1/ai/ws`（单用户单连接、连接级订阅上下文）。一条连接可同时订阅多个会话/任务房间。

### 3.2 订阅 / 退订（入站，沿用现有格式）
```json
{ "action": "subscribe",   "type": "conversation", "id": "100200" }
{ "action": "subscribe",   "type": "conversation", "id": "100200", "after_sequence": 57 }
{ "action": "unsubscribe", "type": "conversation", "id": "100200" }
```
- `after_sequence` 可选：订阅成功后，服务端**先补推** `sequence > after_sequence` 的历史消息，再转入实时（断线增量恢复一步到位）。

### 3.3 订阅确认（出站 ack，沿用现有 `subscription_ack`）
```json
{ "type":"subscription_ack", "topic":"conversation", "id":"100200", "action":"subscribe", "ok":true, "last_sequence": 88 }
```
失败：
```json
{ "type":"subscription_ack", "topic":"conversation", "id":"100200", "action":"subscribe",
  "ok":false, "error_code":"CONVERSATION_NOT_FOUND", "message":"..." }
```
错误码（对齐现有 + 新增）：`CONVERSATION_NOT_FOUND` / `CONVERSATION_FORBIDDEN` / `CONVERSATION_ARCHIVED` / `INVALID_SUBSCRIPTION_TYPE` / `INVALID_REQUEST` / `INTERNAL_ERROR`。
> 与任务订阅不同：会话**不会**因「已完成」而拒绝订阅（completed 会话仍可继续对话）。因此**没有** `ALREADY_FINISHED` 语义。

### 3.4 服务端下行帧
每条新消息 = 一帧 `message`：
```json
{ "type":"message", "topic":"conversation", "id":"100200", "data": { /* 一条 <message> 完整 JSON，带 sequence */ } }
```
- **进度消息**（`kind:"progress"`）高频，按 `grade:transient` 处理：只推 WS、可不入库；客户端按 `task_id` **原地更新**同一张进度卡，不刷屏。
- **里程碑**（clarify / plan_card / review_card / result_card / error_card / stage 切换）按 `grade:persistent` 入库并带 `sequence`，断线后可经 §2.5 补回。
- **状态变更帧**（可选）：会话/agent_state 变化时推 `{ "type":"state", "id":"100200", "data": { "status":"running", "agent_state":{...} } }`，便于客户端切换 UI（也可由客户端从消息推断，state 帧是优化项）。

### 3.5 断线恢复闭环（与任务页一致）
```text
重连 → subscribe(conversation, after_sequence=last_sequence)
      → 收 ack(ok=true) + 补推缺口消息 → 实时继续
ack 失败/弱网 → 退化为 HTTP：GET /messages?after_sequence=last_sequence
```

## 4. 端到端示例（含追问 + 审核 + 迭代）

```text
① POST /conversations {first_message:"做个一分钟都市爱情短剧"}
   ← data.conversation.id=100200          // 客户端立即 WS subscribe conversation:100200
② WS ← message(kind=clarify, "想要几个角色？", options=[1,2,3])      // stage: collecting→awaiting_user
③ POST /signals {ref:那条clarify, signal:"answer_slot", payload:{slot:"characters",value:2}}
   ← {accepted:true, routed_to:"agent"}                              // →collecting
④ WS ← message(kind=plan_card, plan{...,estimated_cost:320})         // planning→confirming
⑤ POST /signals {ref:plan_card, signal:"confirm_plan"}
   ← {accepted:true, routed_to:"agent"}                              // Act：建 Task + TaskLink
⑥ WS ← message(kind=progress,"正在规划剧情…")  (transient, 原地更新) // executing
⑦ WS ← message(kind=review_card, card_type="storyboard_review_card", payload{frames..})  // executing→reviewing
⑧ POST /signals {ref:review_card, signal:"confirm_storyboard_image", payload:{action:"accept"}}
   ← {accepted:true, routed_to:"engine"}                             // 转 await.HandleSignal(callback_token=task_id)
⑨ WS ← progress… ← message(kind=result_card, primary_file_url, actions:[再来一版/修改])  // completed
⑩ POST /messages {text:"第二幕改成夜晚"}                              // 新一轮 → collecting
   ← WS message(kind=plan_card「修改预览」: 将重做1个分镜,约Y积分)     // 来自 PatchPreview
⑪ POST /signals {signal:"confirm_modify"} → Fork → progress… → 新 result_card（版本2）
```

## 5. 错误码表（REST `code`）

| HTTP | `code`（建议常量） | 含义 |
|------|-------------------|------|
| 400 | `INVALID_REQUEST` | 参数校验失败 |
| 401 | `401` | 未登录/过期 |
| 403 | `403` / `CONVERSATION_FORBIDDEN` | 越权访问他人会话 |
| 404 | `CONVERSATION_NOT_FOUND` / `MESSAGE_NOT_FOUND` | 资源不存在 |
| 409 | `CONVERSATION_ARCHIVED` | 已归档会话不可写 |
| 409 | `SIGNAL_STALE` | 卡片已失效（如 await 已被处理/超时） |
| 402 | `INSUFFICIENT_POINTS` | 确认方案时积分不足（复用现有计费错误） |
| 500 | `INTERNAL_ERROR` | 内部错误 |

## 6. 落地清单（服务端）

1. 4 张表 + 仓储（[03 §2](03-conversation-layer.md#2-数据模型)）+ `agent_states`（[11](11-agent-state-machine.md)）。
2. `/agent/*` 路由 + handler（薄：CRUD + 唤醒 Agent Loop，不含智能）。
3. `WSHub` 增加 `type:"conversation"` 订阅分支（鉴权按 `user_id`，ack/幂等/连接级上下文全部复用任务订阅已有实现）。
4. `signals` 的 `routed_to=engine` 分支：从 `current_task_id` 取 `callback_token`，复用 `await.HandleSignal`。
5. Agent Runtime 订阅 EventBus → 翻译 → 落 message + WS 推送（[04 §6](04-agent-runtime.md#6-进度翻译translate-dont-forward)）。

## 7. 待定决策（Open Decisions）
- **state 帧是否必需**：客户端能否仅凭消息流推断 UI 状态？若能则 §3.4 的 `state` 帧降为可选优化。
- **答槽 signal 名规范**：统一用 `answer_slot{slot,value}`，还是每个 slot 一个具名 signal？建议统一 `answer_slot`，降低客户端复杂度。
- **进度入库粒度**：transient 进度是否完全不入库？建议「阶段切换入库、百分比刷新不入库」，与现有 TaskEvent 分层一致。

---

下一篇：[10 · Skill Manifest 规范](10-skill-manifest-spec.md)。
