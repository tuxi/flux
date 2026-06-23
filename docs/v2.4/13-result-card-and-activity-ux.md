# 13 · ResultCard 语义修复 & Activity UX

> P0 + P1 + P2：真机测试修复。完成态的再生/修改、Activity 活动确认、浮动显示。

## P0 · ResultCard 上下文语义

### 问题
完成态 `result_card` 的 actions（再来一版/改一下）没有 `signal`，用户点击或输入控制语后，后台将其当作普通聊天文本，甚至写入 `user_prompt`。

### 修复
- **result_card 结构化信号**：`actions` 增加 `signal: "regenerate_result"` / `"modify_result"`。
- **ObjectResult 加入对象语义管线**：`ActiveObjectResolver` 产出 result ActiveObject，`TargetResolver` 解析顺序 pending → review → **result** → plan。
- **服务层源 Plan 注入**：完成/失败态通过 `result_card.task_id → task_link.plan_id → Plan` 找到源方案，注入 `ConversationContext.Plan`，使再生复用源 prompt。
- **act 守卫**：`regenerate → OperationRegenerate`，`空修改语 → clarify + PendingInteraction(target=result_card)`，`带反馈修改 → 提示词增量重跑新方案版本`。
- **污染根治**：控制 act 不跑兜底 slot 抽取；确认态 typed confirm 直接 launch，不进 planner。

### 客户端变更
- result_card 点击「再来一版」「改一下」→ `POST /api/v1/ai/conversations/:id/signal`
  ```json
  { "signal": "regenerate_result", "ref_message_id": "<result_card.id>" }
  ```
  ```json
  { "signal": "modify_result", "ref_message_id": "<result_card.id>" }
  ```
- 直接输入文本仍兼容（兜底走文本 turn）。

## P1 · 确认 review_card 后不产生独立聊天气泡

### 修复
- `routeEngineSignal` 不再追加 `KindText "已确认，继续生成中…"`。
- 改为即时更新 Activity 消息：`ConfirmGate()` 将 waiting 步骤标记 completed、下一步置 running。
- 确认门未确认时仍单独显示 review_card；确认后成为 Activity 步骤状态。

### 客户端变更
- 不再渲染独立的「已确认，继续生成中…」聊天气泡。
- 确认状态改由 Activity 对应 step（status: completed→running）呈现。

## P2 · Activity 实时浮动到最新位置

### 修复
- `activity.ToContent()` 增加 `updated_at`（每次序列化时刷新 Unix 秒）。
- running/waiting_user 时设置 `display_mode: "floating"`。
- completed/failed 后 `display_mode` 缺省，固定在原时间线位置。

### 客户端变更
- `display_mode == "floating"` → 将该 Activity 浮到消息列表最新位置。
- `display_mode` 缺省或非 floating → 保持原位置。

## 回归

短剧主链路、review 修改 by fork、PR-I LLM 解释器零回退。`go test ./ai-engine/agent/... ./config/...` 全绿。
