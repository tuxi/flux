# 创意反馈交互流程（await 节点）

## 流程概览

```
提交任务 → 工作流执行 → creative_feedback_await 命中 → 服务端 WS 推送事件
                                                              ↓
                                              客户端收到事件 → 查询节点详情 → 展示反馈 UI
                                                              ↓
                                                  ┌─ 用户提交反馈 → 继续执行
                                                  └─ 5分钟超时 → 自动继续
```

## 1. 客户端如何感知「需要反馈」

**不要**通过轮询 `task.status == "suspended"` 判断，因为 `suspended` 的原因有很多（超时、其他 await 节点、外部暂停等），无法区分是否是"等待用户反馈"。

正确方式：**监听 WebSocket 事件**。

当工作流执行到 await 节点（如 `creative_feedback_await`）时，服务端推送 WS 事件，携带 `node_name`（即 step 名称）。事件只告知"哪个节点在等待"，**不携带节点 output**（output 可能很大，不应走 WS）。

客户端收到事件后：
1. 根据 `step` 判断是哪个交互节点（当前为 `creative_feedback_await`）
2. 调用 `GET /api/v1/user/works/:id/nodes/:node` 拉取节点详情（含上游 `generate_creative_brief` 的产出）
3. 展示反馈 UI

**WS 事件格式：**

```json
{
  "type": "user_input_required",
  "step": "creative_feedback_await",
  "message": "创意简报已生成，请提供反馈意见",
  "meta": {
    "signal_name": "goods_timeline_creative_feedback"
  }
}
```

- `type` 固定为 `"user_input_required"`，客户端据此过滤
- `step` 携带等待中的节点名
- 事件每 10 秒推送一次（首次 2 秒后），直到用户反馈或超时

## 2. 用户提交反馈

**请求：** `POST /api/v1/ai/await/signals`

```json
{
  "signal_name": "goods_timeline_creative_feedback",
  "callback_token": "<task_id>",
  "payload": {
    "feedback": "把开场改成更有冲击力的，去掉品牌名的重复堆叠"
  }
}
```

| 字段 | 说明 |
|------|------|
| `signal_name` | 固定值 `"goods_timeline_creative_feedback"` |
| `callback_token` | 提交任务时返回的 `task_id`（服务端已自动注入到任务 input） |
| `payload.feedback` | 用户输入的调整意见，**不可为空**（客户端提交前校验） |

### 超时兜底

- 超时时间：**300 秒（5 分钟）**
- 超时后工作流自动继续，`feedback` 为空字符串，脚本按原创意简报生成
- 客户端应展示倒计时，归零后自动关闭反馈 UI

## 3. UX 要求

### 正常路径
- 用户提交任务后进入结果页等待
- 顶部提示："创意简报生成中，请勿离开页面"
- 收到 await 事件后展示创意简报内容 + 反馈输入框 + 倒计时

### 用户离开页面
- 若用户切到 app 内其他页面，服务端仍然在等待
- 客户端需通过推送/弹窗提醒："有任务需要你的反馈"[任务ID: xxx]
- 点击通知跳转到该任务的结果页，展示反馈 UI
- 倒计时不受离开影响（服务端计时）

## 4. 反馈如何生效

用户反馈文本 → `generate_timeline_script_v1` 节点的 `user_feedback` 输入 → 拼接到 LLM prompt：

```
【UserFeedback】
用户对创意简报的调整反馈，你必须据此调整分镜脚本：
<用户输入的 feedback 文本>
```

若 feedback 为空（超时场景），`【UserFeedback】` 段落不出现在 prompt 中，脚本按原创意简报生成。

## 5. 接口速查

| 接口 | 方法 | 用途 |
|------|------|------|
| `tools/tasks` | POST | 提交任务（客户端已有） |
| `/ai/tasks/{task_id}` | GET | 查询任务状态及节点详情 |
| `/ai/await/signals` | POST | 发送反馈信号唤醒工作流 |

## 6. 扩展性

后续可复用同一套 `await` + `signal` 机制到其他节点（分镜脚本审阅、预览确认等），只需更换 `signal_name`。客户端根据 WS 事件中的 `node_name` 区分展示内容和交互形式。
