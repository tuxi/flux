# 06 · 客户端架构

> V2 对客户端是一次**入口与交互范式**的升级，不是从零重写。客户端的核心新增是「会话式创作」这条主线，以及一个**数据驱动的 Agent Feed 渲染器**。底层与服务端的契约见 [03](03-conversation-layer.md)。

> 本仓库是服务端代码，App 为独立客户端（iOS 优先，含 Apple 登录 / IAP / 匿名账号）。本文以**平台中立**方式描述架构与契约，便于 iOS（及后续 Android/Web）一致实现。

## 1. 信息架构与导航

底部四 Tab（呼应 [00 §5.3](00-vision-and-positioning.md#53-底部导航)）：

```text
┌───────┬───────┬───────┬───────┐
│ 创作   │ 灵感   │ 作品   │ 我的   │
│Agent  │Skills │Works  │Account│
└───────┴───────┴───────┴───────┘
```

| Tab | 屏幕 | V2 角色 |
|-----|------|---------|
| **创作** | Agent 首页 → 会话 | 新主入口（Conversation Agent） |
| **灵感** | Skills Marketplace | 原首页内容升级：模板/技能橱窗，「让 Agent 照这个做」一键进会话 |
| **作品** | 作品列表/详情 | Task 产物；「继续聊 / 再改改」回到会话 |
| **我的** | 账户/会员/设置 | 复用现有 |

## 2. 三个核心屏

### 2.1 Agent 首页（创作 Tab）

```text
你好，我是 DreamAI / 今天想创作什么？
[ 说出你的想法…              🎤 📎 ]     ← 文本 + 语音 + 附件(图/视频)
试试： [制作短剧][带货视频][照片动起来]
      [设计 Logo][生成商品图][制作宣传片]
最近作品 ▸ [▦][▦][▦][▦]
```

- 输入框是唯一焦点。提交 → `POST /agent/conversations`（带首条消息/附件/`entry=home`）→ 进入**会话屏**。
- 快捷建议 = 预填的第一句话（`entry=suggestion`），点击即进会话并自动发送。
- 「最近作品」点击 → 进入对应会话（有 TaskLink）或「基于此作品新建会话」。

### 2.2 会话屏（主战场）

一条垂直的消息流 + 底部输入条。消息流由**异构卡片**组成（见 §3）。关键交互：

- **用户消息**：文本 / 附件缩略图。
- **Agent 追问（clarify）**：问题 + 可点选项 chips（点击即回填，免打字）。
- **方案卡（plan_card）**：意图、关键参数（可点开微调）、预计积分、`[开始创作]` 主按钮。
- **进度（progress）**：阶段名 + 进度条 + 当前步骤备注，**原地更新**（同一 task 的进度合并为一张活动卡，不刷屏）。
- **审核卡（review_card）**：如短剧分镜九宫格，支持「确认 / 调整」→ 发 signal。
- **成品卡（result_card）**：可播放/可预览 + `[再来一版][修改][发布][下载]`。
- **失败卡（error_card）**：人话原因 + `[重试][换方案]`。

### 2.3 作品屏（作品 Tab）

- 列表复用现有 `/user/works`。
- 详情新增：若该 Task 有 TaskLink → `[继续在会话中创作]`；否则 `[基于此作品开启会话]`（V1 老任务也能进 Agent）。
- 「版本」区：同一会话下 `primary + fork` 版本树，可切换对比。

## 3. Agent Feed 渲染协议（客户端最关键的抽象）

客户端**不硬编码业务流程**，而是实现一个 **kind → 渲染器** 的注册表。服务端推什么 `kind`，客户端就渲染对应卡片。新增一种卡片 = 服务端加一个 `kind` + 客户端注册一个渲染器，**互不阻塞、可灰度**。

```text
renderers = {
  "text":            TextBubble,
  "user_attachment": AttachmentBubble,
  "clarify":         ClarifyCard,      // question + options chips
  "plan_card":       PlanCard,         // 参数可编辑 + 确认按钮
  "review_card":     ReviewCardRouter, // 按 card_type 再分发(分镜/帧/音色…)
  "progress":        ProgressCard,     // 按 task_id 合并/原地更新
  "result_card":     ResultCard,       // 播放器 + 动作
  "error_card":      ErrorCard,
  "system":          SystemNotice,
}
render(message) = renderers[message.kind](message.content_json)
```

- **渲染纯数据驱动**：客户端只认 `kind + content_json`（[03 §4](03-conversation-layer.md#4-消息类型kind)），不认识具体是「短剧」还是「带货」。业务差异都在 `content_json` 里。
- **review_card 二级分发**：`review_card` 内按 `card_type` 路由到具体审核 UI。**复用现有短剧的 `await_user_action` 卡片实现**（prompt_review_card / storyboard_review_card）——这些 UI 客户端已经有了，V2 只是把它们放进会话流里渲染。
- **动作统一**：所有卡片的按钮统一描述为 `actions[]`（`{type: signal|navigate|fork|download, ...}`），客户端用一个 action dispatcher 处理，不为每张卡写一套点击逻辑。

## 4. 客户端状态模型

客户端尽量**无业务状态**，以服务端为事实来源：

```text
ConversationStore:
  conversation: {id, status, title}
  messages: [ ...按 sequence 有序... ]
  lastSequence: int            # 增量恢复游标
  pendingUserActions: [...]    # 等待用户响应的卡片(clarify/plan/review)
  activeTasks: {task_id: progressState}   # 进度卡的原地更新状态
```

- **乐观回显**：用户发消息后立即本地插入（带 `client_msg_id`），服务端回执后对齐去重。
- **进度合并**：同 `task_id` 的 `progress` 消息更新同一张活动卡，不堆叠。
- **里程碑落地**：阶段切换/审核/结果是 persistent 消息，进历史；高频进度是 transient，可不入历史（与服务端分层一致）。

## 5. 实时通道与断线恢复

完全复用现有 WS 基础设施（[03 §5](03-conversation-layer.md#5-websocket-协议)）：

```text
进入会话 → WS subscribe room=conv:{id}
收到 message 帧 → 按 sequence 合并进 store
断线/切后台/弱网 →
  重连 → subscribe(after_sequence = lastSequence)   # WS 增量
  或    → GET /messages?after_sequence=lastSequence  # HTTP 兜底拉缺口
```

- 与现有任务页的「`after_sequence` 增量恢复」体验一致——用户切到后台再回来，进度无缝接上。
- 长任务（短剧 3–6 分钟）：即使全程断网，回来后凭 `after_sequence` 也能补齐所有里程碑与最终结果（因为 persistent 消息已入库）。

## 6. 迭代修改的客户端体验

呼应 [04 §7](04-agent-runtime.md#7-迭代式修改--复用-forkpatch)：

```text
成品卡 [修改] 或 用户直接说「第二幕改成夜晚」
  → Agent 回 [修改预览卡]: "将重做 1 个分镜，约 Y 积分"  [确认修改][取消]
  → 确认 → 进度卡(只跑被改节点) → 新 [成品卡]（作为新版本，挂到版本树）
```

- 修改预览卡的数据来自服务端 `PatchPreview`（哪些节点重跑 + 代价），客户端只渲染。
- 成品卡上提供「快捷修改」chips（来自 Manifest `iteration` 提示）：`[换风格][换音色][换 BGM][再来一版]`，点击即发起对应修改意图，降低用户表达成本。

## 7. 与旧入口共存（平滑迁移）

**不删任何旧页面**，分阶段降权：

| 阶段 | 旧工具页（route_key/mode_key 表单页） | 灵感/Marketplace |
|------|-------------------------------------|------------------|
| 灰度初期 | 首页仍可达；Agent 首页并行灰度 | 模板橱窗 |
| 中期 | 旧工具页降级为「Skill 详情 / 专家模式」（从 Marketplace 进入） | 主推「让 Agent 帮我做」 |
| 后期 | 仅高级用户/特定场景保留专家模式入口 | Skill 的两种入口：会话 or 专家表单 |

- 旧表单页 = Skill 的「手动驾驶」模式；Agent 会话 = Skill 的「自动驾驶」模式。两者后端是**同一个 workflow**，只是发起方式不同（[05 §3.2](05-skill-layer.md#32-两类-source)）。
- 用户可在专家模式里「转交给 Agent」，也可在会话里「展开高级参数」——双向通道，照顾不同用户。

## 8. 输入与多模态

- **文本**：主输入。
- **附件**：图/视频，复用现有 `/uploads/init` + `/uploads/complete`（asset_id），消息里带 `assets[]`。附件类型作为意图先验传给服务端。
- **语音**：可选（语音转文本后当文本发送），非首版必需。
- **从相册/作品引用**：把已有 asset/作品作为附件带入会话（「用这张图做个视频」）。

## 9. 性能与体感要点

1. **首响应要快**：发消息后，Agent 的第一条反馈（哪怕是「让我想想…」占位/打字态）应在感知阈值内出现——服务端 `messages` 接口立即回执 + WS 尽快推首帧。
2. **进度要细**：长任务必须有阶段级进度（短剧 5 个阶段都要露出），避免「转圈焦虑」（[01 §4](01-competitive-analysis.md#4-我们要警惕的坑来自竞品的教训)）。
3. **钱要透明**：方案卡/修改预览卡必须显示预计积分，确认后才执行。
4. **可中断不丢**：任何时候切后台/杀进程，回来能恢复（靠 sequence + persistent 消息）。
5. **卡片可重渲染**：所有卡片都能从 `content_json` 纯函数重建，便于历史回看与断线恢复。

## 10. 客户端落地顺序（建议）

```text
P0  会话屏骨架 + Feed 渲染器(text/clarify/plan/progress/result/error) + WS 接入
P1  附件输入 + 审核卡(复用短剧 await 卡) + 方案卡参数微调
P2  作品↔会话双向跳转 + 版本树 + 修改预览卡
P3  Marketplace「让 Agent 做」入口 + 旧工具页降级为专家模式
```

每个 P 都能独立灰度，且不依赖服务端 RAG/Multi-Agent（那是后端第二阶段，对客户端透明）。

---

下一篇：[07 · RAG 增强路线](07-rag-roadmap.md) —— 第二阶段的创作记忆增强。
