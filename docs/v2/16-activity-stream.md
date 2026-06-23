# 16 · 执行期活动流（Activity / 过程消息）契约

> 把执行期的「一行 transient progress」升级成**一条可折叠、可增长、可恢复的过程消息**——像 Claude Code / 小云雀那样把 agent 正在做的事实时摊开。
>
> 不推翻架构：底座仍是 TaskEvent（EventBus）→ AgentObserver 翻译（[14 §5](14-repository-contracts.md)）→ WS 推送（[09 §3.6](09-conversation-api.md)）。变的只是**翻译目标**：从「覆盖式一行」改成「累积式 steps」。本文是客户端+服务端的双边契约。

## 1. 消息类型：`kind = "activity"`

每个任务运行**一条** activity 消息，与 review_card / result_card **并存**（activity=过程，卡片=交互/成品）：

| 维度 | activity | 旧 transient progress |
|---|---|---|
| 数量 | 每个 run 一条 | 每个 task 一条 |
| 持久 | **持久**（占一个 sequence，可回放） | 否（WS-only） |
| 可变 | **原地更新**（steps 增长） | 覆盖 text |
| 渲染 | 可折叠时间线 | 一行 |

> activity **取代**旧的 transient progress（`UpsertTransient`）。result/error/review 仍是各自独立的持久卡。

## 2. `content_json` 结构

```jsonc
{
  "kind": "activity",
  "title": "正在创作短剧",            // 折叠态标题
  "status": "running",               // §4
  "current_step": "storyboard",      // 当前活跃 step 的 id（可空）
  "overall_progress": 0.35,          // 0..1，可空（取自引擎 overall_progress）
  "task_id": "70010",                // 关联任务（= run 的 root task）
  "steps": [
    { "id": "analyze",    "label": "分析你的需求",     "status": "completed", "at": 1733200100 },
    { "id": "storyboard", "label": "生成九宫格故事板",  "status": "running",   "at": 1733200103,
      "detail": "正在规划 8 个分镜…" },              // detail 可空，给展开态显示
    { "id": "review_prompt", "label": "确认分镜脚本",   "status": "pending" },
    { "id": "shots",      "label": "逐镜生成画面",      "status": "pending" },
    { "id": "voiceover",  "label": "配音",            "status": "pending" },
    { "id": "composite",  "label": "合成成片",         "status": "pending" }
  ]
}
```
- 消息顶层字段（id/sequence/role/kind/created_at）同普通 message（[09 §4.2](09-conversation-api.md)）；`role="agent"`，`kind="activity"`，`grade="persistent"`。
- **step 骨架**初始即按 Skill 铺好（全 `pending`），随事件激活——给用户一个「路线图」（类似 Claude Code 的待办）。骨架来源：manifest 的 `plan_stages` / `stages`（[10 §6](10-skill-manifest-spec.md)）。

## 3. step.status

| status | 含义 | 客户端图标建议 |
|---|---|---|
| `pending` | 未开始（已规划） | ○ 灰 |
| `running` | 进行中 | ◐ 转圈 |
| `completed` | 已完成 | ✓ 绿 |
| `waiting_user` | 卡在审核闸门，等用户确认 | 👤 高亮 |
| `failed` | 出错 | ⚠ 红 |

## 4. activity.status

| status | 触发 | 含义 |
|---|---|---|
| `running` | 任务执行中 | 正常推进 |
| `waiting_user` | 命中审核闸门（有 review_card 待确认） | 暂停等用户 |
| `completed` | task 成功 | 全部完成（result_card 随后） |
| `failed` | task 失败 | 失败（error_card 随后） |

activity.status 与会话 `agent_state.stage` 投影一致：running↔executing、waiting_user↔reviewing、completed↔completed、failed↔failed。

## 5. Observer：TaskEvent → steps

AgentObserver（红线 3 的唯一桥）按事件更新同一条 activity 消息（白名单翻译，不 dump 内部噪声）。

### 5.1 🔴 事件分层红线：子任务事件 ≠ 会话消息

一次 workflow run 会 fan-out 成大量子任务（map / loop / subworkflow / 单镜头 i2v / 配音 / 合成），引擎对**每个**子任务都发 `task_succeeded` / `task_failed` / `task_final_failed`，且 `RootTaskID=主任务、TaskID=子任务`。

> **红线**：只有**主任务自身**的终态才能升格为 `result_card` / `error_card`。子任务终态是「执行过程内部事件」，**只能更新 activity，绝不产生会话卡片**。
>
> 判据：`ev.TaskID == ev.RootTaskID` ⟺ 主任务事件（`RootTaskID==0` 视同主任务）。否则是子任务事件。
>
> 违反它就会出现「创作完成 ×20」「同一任务又 success 又 failed」的刷屏（实测 bug 根因）。

### 5.2 路由表

| 引擎事件 | 主/子 | activity 动作 | 会话卡片 |
|---|---|---|---|
| `task_started` | 主 | **创建** activity（骨架全 pending）+ 首 step→running，title=「正在创作短剧」 | — |
| `generation_pipeline_stage`（无 card_type） | 主/子 | 对应 step→running、其之前 step→completed、`current_step`/`overall_progress` 更新 | — |
| `generation_pipeline_stage`（命中 gate） | 主/子 | 对应 step→`waiting_user`，status=`waiting_user` | **append review_card**（[14 §5]） |
| 用户确认后引擎恢复（再来的 progress） | 主/子 | 该 step→completed，status 回 running | — |
| **子任务** `task_succeeded` | 子 | （未来 P0-4：对应 child→completed）当前忽略 | **❌ 不产卡** |
| **子任务** `task_failed`/`final_failed` | 子 | （未来 P0-4：child→failed）当前忽略 | **❌ 不产卡** |
| **主任务** `task_succeeded` | 主 | 所有 step→completed，status=`completed` | **append result_card（一次）** |
| **主任务** `task_failed`/`final_failed` | 主 | `current_step`→failed，status=`failed` | **append error_card（一次）** |

> 审核 gate 事件**不**按主/子收口——故事板/await 节点可能跑在子工作流里，gate 必须照常翻译成 review_card。

### 5.3 终态幂等 + 互斥

一个 run 至多一张最终卡（doc 16 §4）。实现：终态处理在 AgentState CAS 重试循环内，先判 `state.stage ∈ {completed, failed}` → 已终结则直接跳过。

- **幂等**：主任务的 `task_failed` 后通常还会再来 `task_final_failed`、recovery 还可能重发——第二次进来 stage 已 failed → 不再追加。
- **互斥**：success 与 failed 都守同一个终态闸；AgentState 的 version CAS 串行化并发 finalize，于是 result_card **XOR** error_card，绝不并存。
- activity 更新与卡片 append、stage CAS 在**同一个 UoW 事务**内完成，提交后再推 WS（activity 一帧 + 卡片一帧）。

> 已知边界（后续硬化）：跨多次 run（fork / 再来一版）时，「迟到的旧 run 终态」仅靠 stage 闸不够精确，需在 `conversation_task_links` 记 `final_status` 做**按 task** 收口（用户建议，列为后续项）。

## 6. WS 推送

复用 Gap-A 的会话房间推送（[09 §3.6](09-conversation-api.md) 同款 message 帧）：每次 activity 更新推一帧**完整**的 activity 消息——
```json
{ "type":"message", "topic":"conversation", "id":"100200", "data": { /* 整条 activity 消息（含最新 content_json） */ } }
```
客户端**按 `data.id` 原地替换**（同一条消息，更新内容）。这就是「会生长的消息」的实时来源。

## 7. GET /messages：恢复 activity 最终态

- activity 是**持久**消息、占一个 sequence、**原地更新**。`GET /messages?after_sequence=0` 返回它时带的是**当前累积态**（不是历史快照），所以**首次进入/刷新**即可还原完整时间线。
- 断线重连：activity 的 sequence 固定在创建时；增量 `after_sequence` 若已越过它，不会重拉。**恢复规则**：重连时从一个**足够低的游标**（或整段）重拉一次以刷新 activity 最新态，再转 WS 增量。
- progress 丢一两帧无所谓——下一帧即最新累积态（活动消息是幂等全量）。
- （后续可加「订阅成功即推一次当前 activity 快照」，免重拉，本期不做。）

## 8. 客户端折叠渲染规则

- **折叠态（默认）**：一行 = 状态图标 + `title` + 当前 step（`current_step` 对应的 label）+（可选）`overall_progress`。像 Claude Code 收起的工具块。
- **展开**：完整 `steps` 列表，每行按 §3 图标 + label（+ `detail`）。
- `waiting_user`：高亮该 step，提示「去确认」——实际操作在对应的 **review_card**（独立消息，按 pending_message_id 规则渲染按钮，见集成文档 §4.3）。
- `completed`：自动折叠，显示「已完成」，**result_card** 紧随其后（成品 + 「再来一版/改一下」）。
- `failed`：展开到失败 step，**error_card** 紧随（原因 + 退款告知）。
- 一条会话可有多条 activity（每次 fork/再来一版一条），按 sequence 顺序排列。

## 9. 落地改造点（实现时）
1. `domain.MessageKind` += `activity`；message 仓储加 `UpsertActivity`（按 (conv, task_id, kind=activity) 找；首次分配 sequence 持久插入，后续原地更新 content_json）。
2. Observer：用 activity 累积取代 transient 一行；初始化骨架 + 事件映射到 step；翻译 `task_started`。
3. manifest：steps 骨架来源（复用 `plan_stages` + `stages` 的 match→step 映射；必要时加 `activity_steps` 字段）。
4. WS：activity 更新经现有 notifier 推送（已就绪）。
5. 客户端：activity 消息渲染器（折叠/展开/图标/与 review·result 卡衔接）。

---

返回：[v2/README.md](README.md) · 相关：[09 §3.6 WS](09-conversation-api.md) · [10 §6 进度翻译](10-skill-manifest-spec.md) · [14 §5 Observer](14-repository-contracts.md)
