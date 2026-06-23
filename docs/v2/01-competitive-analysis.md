# 01 · 竞品分析与潜能激发

> 目的不是抄，而是：**用竞品的成熟范式，反推我们底座里被低估的能力，把它们变成杀手锏。**

## 1. 三类竞品坐标系

把市场放在两个轴上：**交互形态**（表单 ↔ 对话）× **产物类型**（文本/通用 ↔ 创意成品）。

```text
                  产物 = 创意成品（图/视频/Logo）
                              ▲
        即梦 / 可灵 / Vidu     │     Lovart / Krea / Whisk
        Midjourney            │     Higgsfield / Pika
        （半对话·创意工具）     │     （创意 Agent）  ← DreamAI V2 要去的象限
   表单 ◀──────────────────────┼──────────────────────▶ 对话
        传统 SaaS 工作流        │     ChatGPT / Claude
        Dify / n8n（编排）      │     Manus / Devin / Coze
        （可视化 workflow）     │     （通用 Agent）
                              ▼
                  产物 = 文本 / 通用任务
```

DreamAI V1 在左上「半对话创意工具」与左下「可视化 workflow」之间。**V2 的目标象限是右上：创意 Agent**——既是 Agent（对话驱动、会追问、会规划），又交付**严肃创意成品**（而非聊天文本）。这个象限目前**没有绝对赢家**，是我们的机会窗口。

## 2. 逐个拆解：每家「可借鉴的一招」

### 2.1 通用 Agent：Manus / Devin / ChatGPT(Agent) / Claude

| 范式 | 它怎么做 | 我们怎么借 |
|------|---------|-----------|
| **可见的计划（Visible Plan）** | Manus 把任务拆成 todo 列表，实时勾选；用户随时看到「它打算干什么、干到哪了」 | 我们的 **Plan 对象**（意图+槽位+技能+分阶段）直接渲染成会话里的「创作方案卡」，对应短剧的 stage_changed 事件天然分阶段 |
| **Agent Loop（感知→推理→行动→观察→反思）** | ReAct 式循环，调用工具、看结果、再决策 | 我们的 Agent Runtime 主循环（[04](04-agent-runtime.md)）；「行动」= 启动 Task，「观察」= 监听 TaskEvent |
| **人在环路（Interrupt & Resume）** | 关键决策点暂停问用户，确认后继续 | ★ **我们已有 `await` + signal**！短剧的 `await_user_action` 卡片就是这个机制——别人要新造，我们只需复用 |
| **会话即持久会话（Durable Session）** | 任务跨多轮、可中断可恢复 | Conversation 持久化 + 现有 Task 的 suspend/resume/recovery scanner |

> **洞察**：通用 Agent 最难的是「让 Agent 安全地在真实世界行动 + 可中断可恢复」。我们的引擎**天生**就是为长耗时、异步、可恢复的任务设计的。我们不缺执行底座，缺的是把它包装成对话。

### 2.2 Workflow-as-Tool 编排：Coze / 扣子 / Dify

**最值得对标的一类**——因为它们的核心架构就是我们要走的路：

```text
Coze/Dify 的 Agent = LLM + 一堆「插件/工作流」作为 Tool
用户说目标 → Agent 用 function-calling 选 workflow/plugin → 填参 → 执行 → 回答
```

| 它们的做法 | 对我们的启发 |
|-----------|-------------|
| Workflow 被描述成一个「Tool / Plugin」，带 name + description + 参数 schema，喂给 LLM 做 function calling | 这正是我们的 **Skill Manifest**（[05](05-skill-layer.md)）。我们的 `ToolDefinition/ToolMode` 已经有 title/subtitle/description/key——**Manifest 的原材料已就绪** |
| Agent 自己决定调哪个 workflow | 我们的 **Skill Selector**（[04](04-agent-runtime.md#5-技能选择)） |
| 槽位/参数缺失时由 LLM 追问或用默认值 | 我们的 **槽位补全**，并且可升级为会话内审核卡（比纯文本追问体验更好） |

> **差异化**：Coze/Dify 的 workflow 大多是「轻量 LLM 编排 + API 调用」。我们的 workflow 是**重度、长耗时、多模态、可计费、可分叉的创意产线**（短剧要做分镜→i2v→TTS→合成）。同样是「Agent 调 Workflow」，我们调度的是**真正难的活**。这既是壁垒也是责任：进度翻译、成本透明、可中断必须做扎实。

### 2.3 创意 Agent：Lovart / Krea / Whisk(Google) / Higgsfield

| 它们的亮点 | 我们怎么接住 |
|-----------|-------------|
| **对话式创意**：「给我一张赛博朋克猫」→ 出图 → 「换成白天」→ 迭代 | 我们的迭代直接走 **fork/patch**，且能精确到「只重做某一个节点/某一幕」，比整图重生成更省、更可控 |
| **创意画布 / 多产物并排**：一次产出多个变体供挑选 | 对应我们的 **map 节点 fan-out**（一次生成多分镜/多变体）+ 子任务选择器（`GetChildrenByNode`） |
| **风格/参考图记忆**：「按我上次那个风格」 | 第二阶段 RAG 的 **user_work memory**（[07](07-rag-roadmap.md)）；pgvector 已就绪 |

### 2.4 视频生成：即梦(Dreamina) / 可灵(Keling) / Vidu / Sora / Pika

| 它们的产品决策 | 启发 |
|---------------|------|
| 把「分镜/首尾帧/运镜」做成**渐进式可控**，而不是一句话黑盒出片 | 我们短剧的 **storyboard 审核闸门**就是这个思路——Agent 应在关键节点（分镜确认、配音音色）让用户参与，而不是全自动 |
| 失败/不满意时**局部重生成**某一段，而非整条重来 | 我们的 **逐镜 redo/fork**（短剧文档已明确：逐镜重生成将来通过 redo/fork 作为受控、可计费功能开放） |
| 把昂贵操作的**成本/积分前置告知** | 我们已有 `estimated_cost_total` + Quote 接口，Agent 应在「确认方案卡」里就把预计消耗说清楚 |

## 3. 潜能激发：我们被低估的「结构性优势」

把上面所有「可借鉴」收拢，会发现：**别人要新造的能力，我们大多已经有了底座**。V2 的工作量更多在「包装与翻译」，而非「从零造能力」。

| 别人做 Agent 时的硬骨头 | DreamAI 现状 | V2 复用点 |
|------------------------|--------------|-----------|
| 长耗时任务的异步/恢复/重试 | ✅ Worker + RecoveryScanner + suspend/resume | Agent 只管发起，恢复由引擎兜底 |
| 人在环路的中断/确认 | ✅ `await` 节点 + signal + `await_user_action` 卡片 | 追问/审核**直接复用** |
| 迭代修改不全量重算 | ✅ fork / redo / **patch / resume**（节点级复用，PatchPreview 能预览代价） | 「改一下」= 一次廉价 fork |
| 实时进度流 + 断线恢复 | ✅ TaskEvent(grade/sequence) + WSHub + `after_sequence` 增量 | Agent Feed 的事件源现成 |
| 技能目录的运营/上下线/版本 | ✅ `ToolDefinition/ToolMode/Version` CMS + 发布/下线/灰度 | Skill 目录**直接长在它上面** |
| 成本透明与计费 | ✅ 任务级冻结/退款/结算 + Quote | 「这个方案大约消耗 X 积分」可信地说出来 |
| 向量检索底座 | ✅ PostgreSQL + pgvector | RAG 不用换数据库 |

> **一句话**：竞品在补「执行可靠性、可恢复、可迭代、可计费」这些 Agent 的脏活累活；我们已经把这些做完了，只差一个「会说话的入口」。V2 = **把强大的产线，藏到一句自然语言后面。**

## 4. 我们要警惕的坑（来自竞品的教训）

1. **黑盒焦虑**：全自动出片让用户失控、不敢信。→ 用 **Plan 卡 + 关键闸门审核**给回控制感。
2. **进度沉默**：长任务无反馈用户就流失。→ **进度翻译**必须细腻（「正在生成第二幕」而非转圈）。
3. **成本失控**：对话里随口「再来一个」可能烧掉大量积分。→ 昂贵操作（i2v/重生成）**先报价、用户确认**，并复用 PatchPreview 展示「会重跑哪些节点」。
4. **过度承诺**：LLM 容易答应做不到的能力。→ Skill Selector 只能选**已注册的 Skill**；选不中就坦诚说「这个我还不会，但我可以…」。
5. **追问疲劳**：问太多用户烦。→ 槽位补全要会「猜默认值 + 一次性问关键缺口」，能默认就默认（[04](04-agent-runtime.md#4-槽位补全追问策略)）。

## 5. 结论：差异化定位

```text
我们不做「又一个对话机器人」，
也不做「又一个出图/出片工具」，
我们做：把一条工业级创意产线，封装成一个懂你目标、会追问、会规划、会迭代、成本透明的创作伙伴。
```

护城河 = **重度可控创意产线（已有） + Agent 对话入口（V2 新增）** 的组合。单独任何一边都有人做，组合起来且做扎实的，目前是空位。

---

下一篇：[02 · 总体架构](02-architecture-overview.md) —— 把这套定位落成可落地的四层架构与非侵入边界。
