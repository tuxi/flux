# 08 · 路线图与里程碑

> 原则：**主干先通，再谈增强。** 每个阶段都能独立上线、独立灰度、独立回滚，且**全程不改 Workflow Engine**。

## 1. 阶段总览

```text
阶段一  Conversation + Agent Runtime + Skill Selector   ← 唯一的「必须先做」
   │     （单 Agent · 3–4 个高频 Skill · 进度翻译 · 迭代 fork）
   ▼
阶段二  RAG 创作记忆增强                                  ← 让 Agent 更懂你
   │     （failure_case + template 起步）
   ▼
阶段三  按域拆分专家 Agent（Multi-Agent 雏形）            ← 短剧/带货/Logo Agent
   │
   ▼
阶段四  Blueprint First Runtime                          ← Agent 临时编排多 Skill
```

> 一句话指引（来自立项判断）：**先把「不改引擎下引入 Conversation + Agent Runtime + Skill Selector」做成，后面的短剧 Agent、带货 Agent、Logo Agent、乃至 Blueprint First Runtime，都能自然挂上，不必推翻重来。**

## 2. 阶段一：主干（本期重点）

### 2.1 范围
**做**：Conversation / Message / Plan / TaskLink；意图识别；槽位补全（追问）；Skill 选择；启动任务；Task Event 翻译；迭代修改（fork/patch）；3–4 个高频 Skill 接入。

**不做**：Multi-Agent、长期记忆、RAG、MCP、复杂推理链、自动改作品。

### 2.2 服务端工作分解（WBS）

| 模块 | 工作项 | 复用/新增 | 关键依赖 |
|------|--------|----------|---------|
| 数据层 | 4 张表 + 仓储（conversation/message/plan/task_link） | 新增 | 现有 GORM/repo 约定 |
| Conversation API | `/ai/agent/*`（[03 §6](03-conversation-layer.md#6-http-api-apiv1aiagent)） | 新增 | 复用 `ValidateAuth` |
| WS | conversation room + 增量推送（[03 §5](03-conversation-layer.md#5-websocket-协议)） | 扩展现有 `WSHub` | 复用 sequence/after_sequence 经验 |
| Skill 层 | SkillRegistry + Manifest 加载 + 校验（[05](05-skill-layer.md)） | 新增 | 现有 `WorkflowRegistry` |
| Manifest | 短剧/带货(pro/simple)/图生视频/文生图 Manifest | 新增（聚合现有元数据） | 现有 ToolDefinition/ToolMode/DSL |
| Agent Runtime | 主循环 + 意图 + 槽位 + Plan + Selector + 翻译（[04](04-agent-runtime.md)） | 新增 | 现有 `llmClient` |
| 引擎对接 | 建任务 / 订阅 EventBus / 发 signal / fork-patch | **复用** | `workflow_handler` / `eventbus` / `await_handler` / `redo_service` |
| 计费对接 | Quote 预估 + 既有冻结/退款 | 复用 | 现有 billing |
| Wiring | 在 `server.go` 注册新包与路由 | 扩展 | `ai-engine/server/server.go` |

> 建议新增包：`ai-engine/conversation/`、`ai-engine/agent/`、`ai-engine/skill/`，与现有包平级，在 `server.go` 统一 wire（呼应 [02 §5](02-architecture-overview.md#5-进程与部署形态第一版)）。

### 2.3 客户端工作分解
见 [06 §10](06-client-architecture.md#10-客户端落地顺序建议)（P0 会话骨架+Feed+WS → P1 附件+审核卡+方案卡 → P2 作品↔会话+版本树+修改预览 → P3 Marketplace 入口+旧页降级）。

### 2.4 里程碑与验收（阶段一）

| 里程碑 | 验收标准 |
|--------|---------|
| M1 主干打通（内测） | 用户在会话里说「做个一分钟都市短剧」→ 追问→方案→执行→分镜审核→成片，全程不进任何工具页 |
| M2 多 Skill | 短剧/带货/图生视频/文生图 4 个 Skill 均可由会话发起 |
| M3 迭代 | 「第二幕改成夜晚」走 PatchPreview→Fork，只重跑目标节点，成本可见 |
| M4 健壮性 | 断线后 `after_sequence` 增量恢复无丢失；失败有人话解释 + 退款告知 |
| M5 灰度 | Agent 首页与旧首页并行灰度，可一键切换；旧入口零回归 |

### 2.5 成功指标（北极星）
- **Conversation 创建量 > 直接 Workflow 创建量**（入口翻转的硬指标）
- 首页创建任务率 ↑ / 平均参数填写步骤 ↓ / 首次成功创作耗时 ↓
- Workflow 对用户直接可见度逐步 ↓

## 3. 阶段二：RAG 创作记忆
详见 [07](07-rag-roadmap.md)。落地顺序：`failure_case + template` 先行 → `user_work + prompt_case` 跟进。唯一代码改动点是 Agent Runtime 主循环里插入 `retrieve()`（其余层不动）。验收：相似失败任务的复发率下降；带货脚本/提示词质量在盲评中提升；「按上次风格再来一个」可用。

## 4. 阶段三：专家 Agent（Multi-Agent 雏形）

当单 Agent 的「一个大 system prompt 管所有域」开始变臃肿、各域 Skill 增多时，自然演进为：

```text
                 ┌────────────────┐
用户消息 ───────▶│ Router Agent    │  只做：判域 + 路由
                 └───┬───┬───┬─────┘
                     ▼   ▼   ▼
            ┌─────────┐ ┌──────┐ ┌──────┐
            │短剧 Agent│ │带货  │ │Logo  │   各自：更专的 system prompt
            └────┬────┘ │Agent │ │Agent │   + 更专的 Skill 子集 + 域内 RAG
                 │      └──┬───┘ └──┬───┘
                 └─────────┴────────┴──────▶ 复用同一 Agent Runtime 骨架 + Skill Layer + Engine
```

要点：
- **专家 Agent 复用阶段一的 Runtime 骨架**（[04](04-agent-runtime.md)），区别只是 system prompt + Skill 子集 + 域内 RAG 偏置。
- Router 本质是阶段一「意图识别」的升格——所以阶段一把意图识别做成闭世界分类，是为这里铺路。
- 引擎依然不动。

### 4.1 Logo Skill（贯穿阶段一→三）
Logo 是规划中的新 Skill：阶段一可先以 `text_to_image/image_to_image` 组合或新建 `logo_generation` workflow 接入（补 Manifest 即可被 Agent 调度，[05](05-skill-layer.md)）；阶段三再做专门的「Logo Agent」深化（品牌调研→风格→多稿→矢量化建议）。

## 5. 阶段四：Blueprint 演进

### 5.1 从 Plan 到 Blueprint
阶段一的 **Plan** 是「单 Skill + 槽位」的浅结构。随着用户诉求复杂化（「先把这堆图抠干净，再各做一段图生视频，最后拼成一条带字幕的合集」），Plan 自然演进为 **Blueprint**：一个由多个 Skill 组成、可执行的轻量 DAG。

```text
Plan（一期）          Blueprint（四期）
intent + skill   ──▶  intent + [skill₁ → skill₂ → skill₃] (带数据流连接)
单 Task               多 Task 编排（可并行/串行）
```

### 5.2 关键：仍然不改引擎
Blueprint 的「执行」依旧是**用现有引擎能力组合**——
- 多 Skill 串联 = 多个 Task + TaskLink，由 Agent 按依赖顺序发起（前一个的产物 asset_id 作为后一个的输入）；
- 或者，把高频的组合**固化为一个新的 Workflow**（我们一直在做的事），再以单 Skill 暴露。

即：Blueprint 是 **Agent 侧的编排表示**，落地仍是「建任务 + 串数据」，引擎角色不变。`source.type: blueprint`（[05 §8](05-skill-layer.md#8-与未来来源的兼容预留)）是它的接入形态。

> 这呼应了一直想做的 **Blueprint First Runtime**：它不是要替换 DAG 引擎，而是**在 Agent 层引入「可被 Agent 生成与改写的编排蓝图」**，由引擎执行。阶段一把 Plan 设计成可向后兼容扩展的结构，就是为这一步留的种子。

## 6. 风险登记册

| 风险 | 影响 | 缓解 |
|------|------|------|
| LLM 选错 Skill / 发明能力 | 体验差、用户不信 | 闭世界选择（[04 §3](04-agent-runtime.md#3-意图识别)/[05 §4](05-skill-layer.md#4-skill-selector-选择策略)）；选不中坦诚降级 |
| 追问过多 → 用户烦 | 流失 | 能默认就默认、选项化、批量问关键缺口（[04 §4.2](04-agent-runtime.md#42-追问的少打扰原则)） |
| 进度沉默 / 转圈焦虑 | 长任务流失 | 阶段级进度翻译 + 复用现有 progress 计算（[04 §6](04-agent-runtime.md#6-进度翻译translate-dont-forward)） |
| 对话里随口「再来一个」烧积分 | 投诉/亏损 | 昂贵操作先报价确认 + PatchPreview 透明（[04 §5.3/§7](04-agent-runtime.md)） |
| Agent 层故障波及主业务 | 可用性 | 同进程但逻辑解耦；Agent 不可用时旧入口零回归仍可用 |
| Manifest 与 workflow 漂移 | 选错/映射错 | 启动期一致性校验 fail-fast（[05 §6](05-skill-layer.md#6-注册与同步)） |
| 翻译表覆盖不全 → 暴露原始事件 | 体验破功 | 白名单展示：无映射事件默认不展示（[04 §6.3](04-agent-runtime.md#63-没有映射的事件)） |
| 范围蔓延（一期就想做 RAG/多 Agent） | 交付延期 | 严守阶段一边界（[00 §7](00-vision-and-positioning.md#7-明确不做第一阶段)） |

## 7. 架构演进不变量（每阶段都要守）

1. **引擎零改动**：所有阶段，Workflow Engine 都只是被调度的产线。
2. **Agent 是引擎的客户**：建任务/发 signal/fork 全走现有接口，不开后门、不绕计费。
3. **Plan/Blueprint 向后兼容**：新阶段扩展字段，不破坏旧结构。
4. **增强可降级**：RAG/Multi-Agent 故障时退回更简形态，主干仍可用。
5. **闭世界**：Agent 永远只能调度已注册的 Skill。

## 8. 一句话总结

> 阶段一把「**对话入口 + 单 Agent + Skill 化的现有产线**」做扎实，就等于为短剧 Agent、带货 Agent、Logo Agent、RAG 记忆、乃至 Blueprint First Runtime，铺好了同一条不必返工的轨道。
> **不改引擎，只加大脑与入口——这是 V2 全程的纪律。**

---

返回：[v2/README.md](README.md) · 上一篇：[07 · RAG 增强路线](07-rag-roadmap.md)
