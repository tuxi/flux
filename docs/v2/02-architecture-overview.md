# 02 · 总体架构

## 1. 四层模型

V2 在现有引擎之上叠加三层，对外暴露一套 Conversation API：

```text
┌────────────────────────────────────────────────────────────────────┐
│ ① 客户端 App                                                          │
│    Agent 首页 · 会话流 · Agent Feed · 审核卡 · 作品延续              │
└──────────────────────────────┬─────────────────────────────────────┘
                              Conversation API + WS（新增 /ai/agent/*）
┌──────────────────────────────▼─────────────────────────────────────┐
│ ② Conversation Layer（新增·状态）                                     │
│    Conversation / Message / Plan / TaskLink  + 仓储                   │
│    职责：持久化对话、消息、计划、会话↔任务关联；不含智能             │
└──────────────────────────────┬─────────────────────────────────────┘
                              │ 调用
┌──────────────────────────────▼─────────────────────────────────────┐
│ ③ Agent Runtime（新增·智能）                                          │
│    意图识别 → 槽位补全 → Plan 生成 → Skill 选择 → 启动 → 进度翻译     │
│    职责：把「用户目标」变成「引擎可执行的 Task」，并把执行翻译回用户语言 │
└───────────┬───────────────────────────────────┬─────────────────────┘
          │ 选/调 Skill                        │ 监听/翻译
┌───────────▼───────────────┐         ┌─────────▼─────────────────────┐
│ ④ Skill Layer（新增·薄）   │         │  EventBus（现有）              │
│   Skill Manifest 注册表    │         │  TaskEvent 订阅                │
│   Skill Selector           │         └─────────▲─────────────────────┘
└───────────┬───────────────┘                   │ 发布
          │ 创建 Task / 发 signal（= 现有接口）  │
══════════════════ ★ 零改动边界 ★ ════════════════════════════════════
┌──────────────────────────────────────────────────────────────────┐
│ ⑤ Workflow Engine（现有·不动）                                      │
│   Builder → Workflow(DAG) · Engine · Worker · await/signal          │
│   Task / NodeRuntime / TaskEvent / fork·patch·resume·replay         │
│   PostgreSQL(+pgvector) · Redis Queue · WSHub                       │
└──────────────────────────────────────────────────────────────────┘
```

各层一句话职责：

- **Conversation Layer**：只管「记住对话」——状态层，无智能。
- **Agent Runtime**：唯一的「大脑」——所有 LLM 调用、决策、翻译都在这。
- **Skill Layer**：一本「能力说明书 + 选择器」——把现有 Workflow 描述给大脑，并代为下单。
- **Workflow Engine**：「产线」——一行不改。

## 2. Agent Loop（主循环）

Agent Runtime 的核心是一个**事件驱动的循环**，而非一次性请求。它在「用户消息」和「引擎事件」两种输入下被唤醒：

```text
            ┌────────────── 用户消息 / 用户信号 ──────────────┐
            │                                                │
            ▼                                                │
   ┌─────────────────┐   缺信息   ┌──────────────┐           │
   │ Perceive 感知    │──────────▶│ Clarify 追问  │───────────┘
   │ (解析消息+上下文) │           └──────────────┘  (等用户回答→回到 Perceive)
   └────────┬────────┘
            │ 信息足够
            ▼
   ┌─────────────────┐         ┌──────────────────┐
   │ Reason 推理      │────────▶│ Plan 生成/更新     │
   │ 意图+槽位+选技能  │         │（写入 Conversation）│
   └────────┬────────┘         └──────────────────┘
            │ 用户确认方案（可选闸门）
            ▼
   ┌─────────────────┐  创建 Task   ┌──────────────────┐
   │ Act 行动         │────────────▶│  Workflow Engine   │
   │ (调用 Skill)     │             └─────────┬────────┘
   └─────────────────┘                       │ TaskEvent
            ▲                                 ▼
   ┌────────┴────────┐  翻译事件   ┌──────────────────┐
   │ Respond 回应     │◀───────────│ Observe 观察       │
   │ (写 Agent 消息)  │             │ (订阅 EventBus)    │
   └─────────────────┘             └──────────────────┘
            │ 任务完成
            ▼
   交付成品 → 等待用户反馈（「再改改/再来一个」→ 回到 Perceive，走 fork/patch）
```

> 与通用 Agent 的 ReAct 区别：第一阶段我们**不做自由长链推理**。循环是「浅而可控」的——意图→槽位→技能→执行→翻译，每一步都有结构、可观测、可计费。复杂推理留给第二阶段（[08](08-roadmap-and-milestones.md)）。

## 3. 与现有引擎的关系（非侵入式叠加）

**这是 V2 最重要的工程纪律。** Agent Runtime 与引擎之间只有 4 个**既有**触点，全部是引擎已对外暴露的能力，无需为 Agent 新增引擎内部改动：

| # | Agent 动作 | 复用的现有接口/机制 | 代码位置 |
|---|-----------|--------------------|---------|
| 1 | 启动一个 Skill | 创建 `Task` + `Enqueue`（同 `RunWorkflow` / `CreateTaskFromWorkflow`） | `ai-engine/handler/workflow_handler.go` |
| 2 | 观察执行进度 | 订阅 `EventBus` 的 `TaskEvent` | `ai-engine/eventbus/` |
| 3 | 把用户的确认/选择回灌 | 发 signal（同 `await.HandleSignal`） | `ai-engine/handler/await_handler.go` |
| 4 | 迭代修改作品 | `Fork` / `Resume` / `PatchPreview` | `workflow_handler.go` + `service/redo_service.go` |

```text
┌────────────────────┐
│  Agent Runtime      │
└──┬───────┬─────┬────┘
 (1)建任务 (3)发signal (4)fork/patch
   │       │     │
   ▼       ▼     ▼     (2)订阅事件
┌──────────────────────┐◀──────────  EventBus
│  现有 HTTP/Service 层  │
└──────────────────────┘
   （引擎内部：一行不动）
```

**为什么是订阅 EventBus 而不是新加回调**：引擎已经把每个 `TaskEvent` 发布到进程内 EventBus（再由它持久化 + WS 广播）。Agent Runtime 只需多挂一个订阅者，把事件「翻译」后写进 Conversation。引擎对「有没有 Agent 在听」完全无感知。

> 落地建议：第一版 Agent Runtime 与 AI Engine **同进程**（直接函数调用 + 进程内 EventBus 订阅），避免分布式复杂度。未来需要独立伸缩时，再把触点换成 HTTP/消息总线即可，契约不变。

## 4. 关键时序

### 4.1 一次完整创作（含追问 + 审核闸门）

```text
用户            Conversation API      Agent Runtime        Skill/Engine
 │  "做个一分钟都市短剧"   │                  │                    │
 ├──────────────────────▶│ 存 user message  │                    │
 │                       ├─────────────────▶│ 意图=short_drama    │
 │                       │                  │ 槽位缺: 角色数/风格? │
 │   ◀── Agent: "想要几个角色？都市爱情还是悬疑？" (clarify message)│
 │  "两个角色，都市爱情"   │                  │                    │
 ├──────────────────────▶│─────────────────▶│ 槽位齐 → 生成 Plan   │
 │   ◀── Agent: [创作方案卡: 60s/2角色/都市爱情/预计 X 积分]（确认?）│
 │  "开始"(确认 signal)    │                  │                    │
 ├──────────────────────▶│─────────────────▶│ Act: 建 Task ───────▶ 入队执行
 │                       │                  │   TaskLink(conv,task)│
 │   ◀═══ Agent Feed: "正在规划剧情…" ◀── 翻译 TaskEvent(stage) ◀──┤
 │   ◀═══ 短剧到达 storyboard await 闸门 ◀── await_user_action ◀───┤
 │   ◀── Agent: [分镜预览卡 9 格]（确认/调整?）                    │
 │  "第3格换成夜景"        │                  │                    │
 ├──────────────────────▶│─────────────────▶│ 发 signal(确认分镜)  ──▶ 引擎继续
 │   ◀═══ "正在生成第二幕…正在合成…" （持续翻译）                  │
 │   ◀── Agent: [成品视频卡] + "要不要换个 BGM / 再来一版?"        │
```

### 4.2 迭代修改（走 fork/patch，不全量重算）

```text
用户  "把第二幕改成夜晚"
 └─▶ Agent Runtime
      ├─ 解析为「修改意图」+ 定位目标节点（shot[1]）
      ├─ PatchPreview(task, patch=shot[1].prompt+=night) → 预估「只重跑 shot[1]+合成，约 Y 积分」
      ├─ Agent: [修改预览卡: 将重做 1 个分镜，约 Y 积分]（确认?）
      └─ 用户确认 → Fork(task, patch) → 新 Task 复用其余节点 → Feed 翻译 → 交付新版
```

> 这一段是相对纯 LLM 创作产品的**结构性优势**：我们改的是「DAG 里的一个节点」，不是「整段重生成」。

## 5. 进程与部署形态（第一版）

- **同进程叠加**：Agent Runtime / Conversation / Skill 作为 `ai-engine` 内的新包（建议 `ai-engine/agent/`、`ai-engine/conversation/`、`ai-engine/skill/`），在 `server.go` 一并 wire，复用同一套 repo / EventBus / WSHub / LLM client。
- **路由**：新增 `/api/v1/ai/agent/*`（会话）与 WS 复用现有 `WSHub`（新增 conversation room）。详见 [03](03-conversation-layer.md#5-websocket-协议)。
- **LLM 调用**：复用现有 `llmClient`（OpenAI/DeepSeek/Qwen/火山…），意图识别与 Plan 生成走 function-calling/结构化输出。
- **存储**：新增 4 张表（conversation/message/plan/task_link），与现有 Task 表通过 `task_link` 关联，不改 Task 表结构（仅可选地复用其 `entry_type` 等冗余字段）。

## 6. 扩展点（为未来预留，但第一版不实现）

架构刻意在这些位置留好「插槽」，使后续能力**挂上去而非推翻**：

| 未来能力 | 预留挂载点 | 说明 |
|---------|-----------|------|
| **RAG 创作记忆** | Agent Loop 的 `Reason` 之前插入「检索」步骤；Plan 生成时注入检索结果 | 5 类知识 + pgvector，见 [07](07-rag-roadmap.md) |
| **Multi-Agent** | Skill Selector 升级为「Router Agent → 专家 Agent」；专家 Agent 复用同一 Runtime | 短剧/带货/Logo Agent，见 [08](08-roadmap-and-milestones.md) |
| **MCP / 外部工具** | Skill Manifest 增加一种 `source: mcp` 的 Skill 来源 | Skill 抽象与来源解耦 |
| **Blueprint First Runtime** | Plan 升级为可执行 Blueprint；Skill 之间可被 Agent 临时编排成新 DAG | Plan→Blueprint 是同一对象的演进，见 [08](08-roadmap-and-milestones.md#blueprint-演进) |
| **长期记忆 / 用户画像** | Conversation 之上加 user-profile 检索，注入系统提示 | 复用 RAG 基础设施 |

> 关键：这些**全部挂在 Agent Runtime 与 Skill Layer 上，不下沉到引擎**。引擎始终只是「被调度的产线」。

## 7. 架构不变量（Review 时对照）

1. 引擎代码零改动（除非是引擎自身的独立优化）。
2. Agent 调度 Skill = 走现有「建 Task / 发 signal / fork」路径，不绕过计费与权限。
3. 用户永远看不到原始 `TaskEvent` 类型名，只看到翻译后的自然语言/卡片。
4. 任何昂贵操作（生成/重生成）在执行前都经过「报价 + 用户确认」。
5. Skill Selector 只能选**已注册**的 Skill；不存在则坦诚降级。
6. Conversation 是事实来源；客户端无状态，断线后凭 `after_sequence` 增量恢复。

---

下一篇：[03 · Conversation 层设计](03-conversation-layer.md) —— 数据模型、状态机、API 与 WS 协议。
