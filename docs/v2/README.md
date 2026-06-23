# DreamAI V2 · Agent First 创作架构

> 从 **Workflow First** 升级为 **Agent First**。
> 从「用户找工具、填参数」升级为「用户说目标，Agent 造结果」。
> 一句话：**Conversation Driven Creation**。

本目录是 DreamAI V2 的设计文档根目录。V2 不是一次推翻重来，而是在**完全不改动现有 DAG Workflow Engine** 的前提下，在其之上叠加一层 **Conversation Layer + Agent Runtime + Skill Selector**，把现有的每一个 Workflow / Tool 沉淀为 Agent 可调度的 **Skill**。

---

## 📍 当前实施状态（先读这个）

> **[17 · 实现现状审计与后续路线图](17-implementation-status-and-roadmap.md)** 是 Agent First 的**唯一阶段性状态总览**。文档 00–16 是**设计**；17 记录**真实实现、文档↔实现差异、Phase A–D 路线图与验收标准**。**当 00–16 与 17 冲突时，以 17 + 真实代码为准。**
>
> 一句话现状：**short_drama Agent First MVP 主体已成立并经真实联调**，现从「架构搭建期」转入「稳定 / 体验 / 通用性验证期」。改动 Conversation / Skill / WS 契约或新增 Skill 前，请先读 17。

---

## 0. 为什么是现在

我们已经有了一套被反复打磨、能力很强的底座：

- **可回放、可分叉、可局部重做的 DAG 运行时**（fork / redo / patch / resume / replay）
- **await 节点 + signal 机制**（人在环路中的确认/审核，短剧工作流已在用）
- **统一的 TaskEvent 事件流**（grade / sequence / progress / node_index，WS 实时 + DB 持久化 + 增量恢复）
- **CMS 化的工具目录**（`ToolDefinition → ToolMode → ToolModeVersion`，带 `route_key` / `mode_key`）
- **任务级计费**（冻结/退款/结算、`estimated_cost_total` / `actual_cost_total`）
- **PostgreSQL + pgvector**（RAG 的天然底座）

这些恰好是「做一个严肃 Agent 产品」最难、最贵的部分。我们缺的不是执行力，而是**入口形态**——用户仍然站在工具货架前，而不是站在一个懂他目标的创作伙伴面前。

V2 要补的，就是这最后、也是最关键的一层。

详见 [00-vision-and-positioning.md](00-vision-and-positioning.md) 与 [02-architecture-overview.md](02-architecture-overview.md#与现有引擎的关系非侵入式叠加)。

---

## 1. 一页纸架构

```text
┌──────────────────────────────────────────────────────────────┐
│  App（客户端）                                                  │
│  Agent 首页 · 会话式创作 · Agent Feed · In-Conversation 审核卡   │
└───────────────────────────┬──────────────────────────────────┘
                            │  Conversation API + WS（新增）
┌───────────────────────────▼──────────────────────────────────┐
│  Conversation Layer（新增）                                     │
│  Conversation / Message / Plan / TaskLink                      │
└───────────────────────────┬──────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│  Agent Runtime（新增·大脑）                                      │
│  意图识别 → 槽位补全(追问) → 生成计划 → 选择技能 → 启动任务 → 进度翻译 │
└───────────────────────────┬──────────────────────────────────┘
                            │  Skill 调用（= 复用现有 Run/Task 接口）
┌───────────────────────────▼──────────────────────────────────┐
│  Skill Layer（新增·薄封装）                                      │
│  Skill Manifest + Skill Selector（把 Workflow/Tool 描述给 LLM）  │
└───────────────────────────┬──────────────────────────────────┘
                            │  ★ 零改动边界 ★
┌───────────────────────────▼──────────────────────────────────┐
│  Workflow Engine（现有·不动）                                    │
│  DAG · Task · await/signal · TaskEvent · fork/patch/resume      │
└──────────────────────────────────────────────────────────────┘
```

**核心契约（必须守住）**：Agent Runtime 调用 Skill 的唯一方式，就是走现有的「创建 Task → 入队 → 监听 TaskEvent → 发 signal」路径。Agent **不直接操作 DAG**。这条边界让 V2 可以独立演进、独立灰度、独立回滚。

---

## 2. 文档索引

| # | 文档 | 内容 | 读者 |
|---|------|------|------|
| 00 | [愿景与定位](00-vision-and-positioning.md) | Agent First 理念、产品定位、设计原则、首页改版、不做什么 | 全员 / 产品 |
| 01 | [竞品分析与潜能激发](01-competitive-analysis.md) | Coze/Dify/Manus/即梦/可灵/Lovart/ChatGPT 等，可借鉴的模式与我们的差异化 | 产品 / 架构 |
| 02 | [总体架构](02-architecture-overview.md) | 四层模型、Agent Loop、与现有引擎的非侵入式关系、关键时序 | 架构 / 服务端 |
| 03 | [Conversation 层设计](03-conversation-layer.md) | Conversation/Message/Plan/TaskLink 数据模型、状态机、API、WS 协议 | 服务端 |
| 04 | [Agent Runtime 设计](04-agent-runtime.md) | Agent 主循环、意图识别、槽位补全(追问)、Plan 生成、进度翻译 | 服务端 / 算法 |
| 05 | [Skill 层设计](05-skill-layer.md) | Workflow as Skill、Skill Manifest、Skill Selector、现有能力映射表 | 服务端 / 架构 |
| 06 | [客户端架构](06-client-architecture.md) | Agent 首页、会话 UI、Agent Feed 渲染、审核卡、与旧入口共存、迭代式修改 | 客户端 |
| 07 | [RAG 增强路线](07-rag-roadmap.md) | 5 类创作记忆、pgvector、检索增强 Plan、为什么第一版不做 | 服务端 / 算法 |
| 08 | [路线图与里程碑](08-roadmap-and-milestones.md) | 分阶段范围、成功指标、风险、未来 Agent 如何挂载、Blueprint 演进 | 全员 |

**🔒 架构定型三件套 + 数据模型锁定**（写完这几份，V2 架构与数据模型即定型，后续是工程实现而非方向问题）：

| # | 文档 | 内容 | 读者 |
|---|------|------|------|
| 09 | [Conversation API 契约](09-conversation-api.md) | 实现级 REST + WS 规格：信封/分页/幂等/信号双归宿/断线恢复 | 服务端 / 客户端 |
| 10 | [Skill Manifest 规范](10-skill-manifest-spec.md) | **Workflow → Skill 的映射标准**：字段规范、校验规则、3 个完整示例 | 服务端 / 架构 |
| 11 | [Agent 状态机](11-agent-state-machine.md) | **AgentState** 工作记忆 + 9 状态机 + 「过一天回来」的恢复语义 | 服务端 |
| 12 | [数据模型 DDL + GORM Entity](12-data-model-ddl.md) | **5 张新表锁定**：PostgreSQL DDL + GORM Entity + 序号/幂等/CAS 落地要点 | 服务端 |
| 13 | [short_drama 最小闭环验证](13-short-drama-skill-walkthrough.md) | 用一个 Skill 端到端跑通全栈：完整 Manifest + slots + examples + input 映射 + 卡片结构 + 进度翻译 + **17 步状态流** | 服务端 / 全员 |
| 14 | [Repository 接口契约](14-repository-contracts.md) | **Service/Repository 边界锁定**：6 Repo + UnitOfWork + AgentObserver + Outbox + 四条红线 | 服务端 |
| 15 | [AgentRuntime Decision Engine](15-agent-decision-engine.md) | **Agent 的大脑**：`Decide(state,input)→Decision`、回合路由、冷启动流水线、能力端口、护栏 | 服务端 / 架构 |
| 16 | [执行期活动流（Activity）](16-activity-stream.md) | 执行期一行 progress → **可折叠/可增长/可恢复的过程消息**：kind=activity、steps、Observer 翻译、WS、客户端折叠渲染 | 服务端 / 客户端 |
| **17** | [**实现现状审计与后续路线图**](17-implementation-status-and-roadmap.md) | **⭐ 当前唯一状态总览**：真实架构快照 + 已实现/未实现/延后矩阵 + 文档↔实现差异 + 技术债 + Phase A–D 路线图。**与 00–16 冲突时以本文为准** | 全员 |

建议阅读顺序：**00 → 01 → 02 →（按角色）服务端 03/04/05/07 · 客户端 06 → 08 → 定型套 09/10/11 → 数据模型 12 → 工程边界 14 → 大脑 15 → 端到端验证 13**。

> **四条工程红线**（[14](14-repository-contracts.md) 锁定）：①Message.sequence 必须事务内递增 ②AgentState 更新必须走 version CAS ③TaskEvent 不得直接写 ConversationMessage，必须经 AgentObserver/Translator（`Conversation` 与 `TaskEvent` 独立）④跨服务副作用（建任务/通知/webhook）一律 Post-Commit 走 Outbox，事务内只入箱（防孤儿任务）。

> **优先级排序**（落地顺序，RAG 垫底甚至可不入 V2）：①Conversation Layer ②Agent Runtime ③**Skill Manifest** ④Patch/Fork ⑤RAG。其中 Skill Manifest 决定 `Workflow → Skill` 怎么映射，是「Agent 能否不靠硬编码工作」的分水岭——见 [10](10-skill-manifest-spec.md)。

---

## 3. 范围边界（第一阶段，务必收敛）

第一阶段只回答一个问题：

> **如何在不改现有 Workflow Engine 的前提下，引入 Conversation + Agent Runtime + Skill Selector。**

**做**：Conversation / Message / 意图识别 / 槽位补全（追问）/ Skill 选择 / 启动任务 / Task Event 翻译为用户语言。

**不做**（明确推迟）：Multi-Agent、长期记忆、RAG、MCP、复杂推理、自动修改作品。

> RAG、Multi-Agent 不是被否定，而是被排序。架构上为它们预留好挂载点（见 [02](02-architecture-overview.md) 的扩展点小节与 [08](08-roadmap-and-milestones.md)），第一版先把 **Conversation → Skill Selector → Workflow** 这条主干跑通。

---

## 4. 术语表

| 术语 | 含义 | 对应现有实体 |
|------|------|------------|
| Conversation | 一次创作过程（一个目标的完整对话） | 新增 |
| Message | 会话中的一条消息（用户/Agent/系统/进度/结果） | 新增 |
| Plan | Agent 对当前创作的结构化计划（意图+槽位+所选技能）——**已承诺的快照** | 新增 |
| AgentState | Agent「当前进行到哪一步」的**可变工作记忆**（草稿态槽位/等待原因/恢复锚点） | 新增（见 [11](11-agent-state-machine.md)） |
| TaskLink | Conversation 与 Workflow Task 的关联（一对多） | 新增（关联现有 `Task`） |
| Skill | Agent 可调度的一个能力单元 | = 现有 Workflow / ToolMode 的封装 |
| Skill Manifest | 给 LLM 看的技能说明书（描述+槽位+成本+示例） | 新增（生成自现有元数据） |
| Agent Runtime | 会话与引擎之间的「大脑」 | 新增 |
| Slot / 槽位 | 执行技能所需的一项信息（时长/风格/素材…） | 映射到 Workflow `input` 字段 |
| Clarification | Agent 为补全槽位发起的追问 | 复用 `await` + signal 思想（会话内卡片） |
| Agent Feed | 把底层 TaskEvent 翻译成的用户可读进度流 | 翻译自现有 `TaskEvent` |

---

## 5. 关键设计取舍速览

1. **不改引擎**：Agent 是引擎的「客户」，不是引擎的「改造者」。零改动边界是第一原则。
2. **Skill = 既有 Workflow**：不重写任何工作流；为它们补一份「说明书（Manifest）」即可被 Agent 调度。
3. **追问 = 会话内的 await**：复用短剧已验证的 `await_user_action` 卡片范式，把「缺参数」变成「对话里的一次选择」。
4. **迭代 = fork/patch**：「再做一个 / 把第二幕改成夜晚 / 换个音色」直接落到现有 fork/redo/patch——这是我们相对纯 LLM 产品的**结构性优势**。
5. **进度 = 翻译而非透传**：用户看到的是「正在规划剧情…正在生成第二幕…」，而不是 `node_running`。
6. **旧入口不删**：工具页降级为「Skill 详情 / 专家模式」，与 Agent 首页长期共存，平滑迁移。

---

*文档维护：本目录随 V2 演进持续更新。改动 Conversation/Skill 契约时，请同步更新 [03](03-conversation-layer.md) 与 [05](05-skill-layer.md) 并在 [08](08-roadmap-and-milestones.md) 记录里程碑。*
