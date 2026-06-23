# 00 · 愿景与定位

## 1. 问题：我们把「目标」翻译成了「操作」

当前 DreamAI 是 **Workflow First**。用户要做一件事，必须先把自己的目标，翻译成一连串系统语言：

```text
找到对应工具 → 选模式(mode) → 选模型(provider) → 填提示词 → 上传素材 → 设时长/比例 → 创建任务
```

对专业用户，这套「控制台」是优势。但对绝大多数用户，这是一道**翻译墙**：

- 用户想的是：「帮我把这几张图做成一条带货视频」
- 系统要求的是：「请在 `goods_video_pro` 工具里，选择 `image_to_video` 模式，上传商品图，填写卖点……」

用户关心 **目标（What / Why）**，系统却逼他先决定 **路径（Which workflow / Which model）**。

> 本质上，我们不是一个「聊天产品」，也不是一个「AI 助手」。
> 我们是一个 **Conversation Driven Creation**（对话驱动创作）产品。

## 2. 升级：Goal First，而不是 Tool First

V2 把入口从「工具货架」翻转为「创作伙伴」：

| 维度 | V1（Workflow First） | V2（Agent First） |
|------|---------------------|-------------------|
| 用户起点 | 选一个工具 | 说一个目标 |
| 谁决定路径 | 用户 | Agent |
| 缺信息时 | 表单标红、用户自查 | Agent 主动追问 |
| 看到的进度 | `running / success / failed` | 「正在规划剧情 / 正在生成第二幕」 |
| 修改作品 | 重新填表、重跑 | 「把第二幕改成夜晚」一句话 |
| Workflow | 直接暴露给用户 | 退化为 Agent 的 **Skill** |
| Task | 用户直接面对的对象 | Agent 的执行单元 |

用户表达的应该是「**我想做什么**」，而不是「**我想用哪个工具**」。

## 3. 产品定位

```text
DreamAI：
  从  AI Tool Platform（AI 工具平台）
  升级为  AI Creative Agent（AI 创作智能体）
```

- **不叫**「聊天」「AI 助手」「Chatbot」。它的对话只是手段。
- **核心是创作**：对话的产物是**作品**（视频/图片/Logo/封面…），不是文字答案。
- **Agent 是创作总监**：理解你 → 补齐信息 → 制定方案 → 调度产线 → 交付成品 → 听你反馈再改。

## 4. 设计原则（贯穿全套文档）

### 4.1 Goal First（目标优先）
用户表达目标，Agent 决定路径。任何需要用户「预先决定技术路径」的设计，都是退化。

### 4.2 Workflow As Skill（工作流即技能）
现有 Workflow **一个都不删、一行都不重写**。每个 Workflow 通过补一份 **Skill Manifest** 被 Agent 调度。

| 现有 Workflow | 对应 Skill |
|---------------|-----------|
| `short_drama` | 短剧 Skill |
| `goods_video_pro` / `goods_video_simple` | 带货视频 Skill |
| `image_to_video` / `motion_control` | 照片动起来 Skill |
| `text_to_image` / `image_to_image` / `style_transfer` | 图片创作 Skill |
| `logo_generation`（规划中） | Logo Skill |
| `text_to_video` | 文生视频 Skill |

详见 [05-skill-layer.md](05-skill-layer.md#现有能力映射表)。

### 4.3 Conversation Is First-Class Citizen（会话是一等公民）
Conversation 成为第一入口与主线索。Task 不再直接面向用户，而是 Conversation 下的执行单元（一个 Conversation 可挂多个 Task）。

### 4.4 不改引擎（Non-Invasive）
Agent Runtime 是 Workflow Engine 的**客户**，不是它的**改造者**。Agent 只能通过现有的「建任务 / 监听事件 / 发 signal」与引擎交互。这条边界是 V2 能否平稳落地的生命线。

### 4.5 进度是翻译，不是透传（Translate, don't Forward）
底层 `TaskEvent`（节点状态、provider 轮询…）必须被翻译成用户语言，再进入会话。用户永远不该看到 `node_complete_async`。

### 4.6 迭代优先（Iteration as a First-Class Action）
「再做一个」「换个风格」「第二幕改成夜晚」是创作的常态。V2 把它作为一等操作，直接落到现有 fork/redo/patch 机制上（见 [04](04-agent-runtime.md#7-迭代式修改--复用-forkpatch) / [06](06-client-architecture.md)）。

## 5. 首页改版

### 5.1 现状
首页是入口网格：`图片生成 | 视频生成 | 带货视频 | 图生视频 | Logo …`。用户被迫先做分类决策。

### 5.2 升级后：首页 = Agent

```text
┌─────────────────────────────────────────┐
│   你好，我是 DreamAI                        │
│   今天想创作什么？                          │
│                                           │
│   ┌─────────────────────────────────┐     │
│   │  说出你的想法…                    │ 🎤📎 │  ← 文本 / 图片 / 视频
│   └─────────────────────────────────┘     │
│                                           │
│   试试：                                   │
│   [制作短剧] [带货视频] [照片动起来]          │  ← 快捷建议（点了也是进会话）
│   [设计 Logo] [生成商品图] [制作宣传片]       │
│                                           │
│   最近作品  ▸                              │  ← 延续创作的入口
│   [▦][▦][▦][▦]                            │
└─────────────────────────────────────────┘
```

输入框支持：**文本 / 图片 / 视频**。快捷建议本质是「预填的第一句话」，点击后同样进入会话。

### 5.3 底部导航

| Tab | V2 含义 |
|-----|---------|
| 创作 | Conversation Agent（新主入口） |
| 灵感 | 原首页内容升级为 **Skills Marketplace**（短剧/带货/图片/Logo/模板…，可一键「让 Agent 帮我做」） |
| 作品 | 我的创作（Task 产物，支持「继续聊/再改改」回到会话） |
| 我的 | 账户/会员/设置 |

> 「灵感」不再是冷货架，而是「**带着一个范例进入会话**」的跳板：用户看到喜欢的模板 → 「按这个做一个」→ 直接进 Agent。

详见 [06-client-architecture.md](06-client-architecture.md)。

## 6. 第一阶段目标（用户视角）

用户能够**只用自然语言**，无需进入任何具体工具页面，完成：

- 一分钟短剧
- 带货视频
- 图生视频 / 照片动起来
- Logo / 商品图 / 封面
- 图片生成

并能在同一个会话里：补充信息、确认方案、看懂进度、拿到成品、说一句话再改一版。

## 7. 明确不做（第一阶段）

| 不做 | 原因 / 何时做 |
|------|--------------|
| Multi-Agent（多智能体协作） | 先把单 Agent + Skill 跑通；未来按域拆分（短剧/带货/Logo Agent），见 [08](08-roadmap-and-milestones.md) |
| 长期记忆 / RAG | 第二阶段以「创作记忆增强」形式接入，见 [07](07-rag-roadmap.md) |
| MCP / 外部工具接入 | Skill 抽象已为其预留位置，非首版重点 |
| 复杂推理 / 自主规划链 | 第一版 Plan 是「意图+槽位+技能」的浅结构，不做自由 ReAct 长链 |
| 自动修改作品 | 修改必须用户在环（一句话触发，但由用户确认），不做无人值守的自动重做 |

## 8. 成功指标

- 首页创建任务率提升
- 用户填写参数步骤数下降
- 用户首次成功创作的平均耗时下降
- **Conversation 创建量 > 直接 Workflow 创建量**（北极星：入口完成翻转）
- Workflow 对用户的直接可见度逐步降低
- 从「工具驱动」到「目标驱动」：用户留存与复购随创作成功率提升

---

下一篇：[01 · 竞品分析与潜能激发](01-competitive-analysis.md) —— 看看别人怎么做，反推我们该把哪些底座能力变成杀手锏。
