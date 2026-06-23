# 05 · Skill 层设计

> Skill Layer 是 Agent 与现有引擎之间的**薄封装**。它做两件事：(1) 用一份 **Skill Manifest** 把每个现有 Workflow「描述」给 LLM 看；(2) 提供 **Skill Selector** 在已注册 Skill 内选择。**它不重写任何工作流。**

## 1. 核心理念：Workflow As Skill

```text
一个 Workflow  +  一份 Manifest（说明书）  =  一个 Agent 可调度的 Skill
```

我们已经有几十个打磨好的 Workflow（短剧、带货、图生视频、图片创作…）。它们对 Agent 来说就是「能力」。Agent 不需要知道 DAG 内部，只需要知道：

- 这个 Skill **能干什么**（description）
- 需要**哪些信息**（slots）
- 大概**多少钱 / 多久**（cost / duration hint）
- **什么样的请求该选它**（intent 标签 + 示例）
- 执行时**对外可见的阶段**（stages，用于进度翻译与方案卡）

这正是 Coze/Dify 把 workflow 暴露成 function-calling tool 的思路（[01 §2.2](01-competitive-analysis.md#22-workflow-as-tool-编排coze--扣子--dify)），但我们调度的是重度创意产线。

## 2. Manifest 的原材料已经在库里

好消息：**Manifest 的大部分字段，现有系统已经有了**，不需要凭空写：

| Manifest 字段 | 现有来源 | 代码 |
|--------------|---------|------|
| 名称/标题/副标题 | `ToolDefinition.Title/Subtitle`、`ToolMode.Title` | `internal/model/entity/tool_definition.go`、`tool_mode.go` |
| 描述 | `ToolDefinition.Description`、`ToolMode.Description` | 同上 |
| 路由标识 | `route_key` / `mode_key` | `tool_definition.go` / `home_tool.go` |
| 产物类型 | `WorkflowDefinition.Output.ResultType` | `definition/workflow_definition.go` |
| 可见阶段 | Workflow 的 `stage_changed` 事件 / 节点 label | 各 `*_dsl.go`（如 `short_drama_main_dsl.go`） |
| 入参 schema | Workflow 节点的 `InputSchema` / start 节点 input | `tool/schema.go`、各 DSL |
| 成本预估 | 现有 **Quote** 接口 / `estimated_cost_total` | billing |
| 图标/封面 | `IconURL` / `CoverURL` | entity |

> 结论：Skill Manifest **不是新建一套元数据**，而是「**聚合现有元数据 + 补几个 Agent 专用字段**」。补的主要是：`intent` 标签、`slots` 的追问语义、`stages` 的用户文案、`few-shot` 示例。

## 3. Skill Manifest 规格

建议 Manifest 以**声明式文件**维护（每个 Skill 一份，放 `ai-engine/skill/manifests/` 或随各 workflow 目录），启动时注册进 `SkillRegistry`（与现有 `WorkflowRegistry.Sync` 同构）。示例（短剧）：

```yaml
# skill: short_drama
key: short_drama                      # 唯一标识（= workflow name）
intent: [short_drama]                 # 命中此意图域；可多标签
title: 短剧
summary: 把一个故事点子做成一分钟竖屏短剧（分镜→画面→配音→成片）
description: >                         # 给 LLM 选择/判别用，要点足、边界清
  适合：用户想做有剧情的短视频、连续画面叙事、都市/悬疑/搞笑等。
  不适合：单张图、纯口播带货、Logo。需要一个故事方向即可开始。
result_type: video
source:                               # Skill 的执行来源
  type: workflow                      # workflow | tool_mode | (未来) mcp/blueprint
  workflow_name: short_drama
needs_plan_confirmation: true         # 昂贵 → 出方案卡确认
estimated_cost_hint: "约 300 积分"     # 兜底文案；精确值走 Quote
duration_hint: "约 3–6 分钟"

slots:
  - key: idea                         # 故事点子（核心）
    required: true
    type: string
    maps_to: input.idea
    ask: "想讲一个什么样的故事？一句话就行"
  - key: style
    required: false
    type: enum
    options: [urban_romance, suspense, comedy, healing]
    default: urban_romance
    maps_to: input.style_preset
    ask: "想要什么风格？"
  - key: characters
    required: false
    type: int
    default: 2
    maps_to: input.character_count
  - key: duration_sec
    required: false
    type: int
    default: 60
    maps_to: input.duration
  - key: aspect_ratio
    required: false
    type: enum
    options: ["9:16", "16:9", "1:1"]
    default: "9:16"
    maps_to: input.aspect_ratio

stages:                               # 引擎阶段 → 用户文案（进度翻译表）
  - match: "stage_changed:storyboard"      ; text: 正在规划剧情与分镜
  - match: "await_user_action:storyboard_review_card" ; as: review_card ; text: 分镜画好了，看看要不要调整
  - match: "stage_changed:generating_shots"; text: 正在逐镜生成画面
  - match: "voiceover_compose"             ; text: 正在配音
  - match: "stage_changed:composite_video" ; text: 正在合成成片

gates:                                # 会话内审核闸门（来自引擎 await signal）
  - signal: confirm_storyboard_prompt ; card: prompt_review_card
  - signal: confirm_storyboard_image  ; card: storyboard_review_card

examples:                             # few-shot：帮意图识别 & 选择
  - "帮我做个一分钟的都市爱情短剧"
  - "把这个故事拍成短视频：程序员转行开咖啡馆"

iteration:                            # 支持的迭代动作 → 落到 patch
  - phrase_hint: ["第N幕/某一镜 改成…"] ; scope: node ; target: shots
  - phrase_hint: ["换个音色/配音"]       ; scope: node ; target: voiceover
  - phrase_hint: ["换个风格/再来一版"]    ; scope: whole
```

### 3.1 Manifest 字段总览

| 段 | 作用 | 被谁消费 |
|----|------|---------|
| `key / intent / description / examples` | 意图识别 & Skill 选择 | [04 §3/§5](04-agent-runtime.md) |
| `slots` | 槽位补全 + input 映射 + 追问话术 | [04 §4](04-agent-runtime.md#4-槽位补全追问策略) |
| `source` | 如何执行（建哪个 workflow） | [04 §5.4](04-agent-runtime.md#54-启动任务复用现有路径) |
| `needs_plan_confirmation / estimated_cost_hint` | 报价闸门 | [04 §5.3](04-agent-runtime.md#53-报价闸门) |
| `stages` | 进度翻译 | [04 §6](04-agent-runtime.md#6-进度翻译translate-dont-forward) |
| `gates` | await signal ↔ 会话审核卡 | [04 §8](04-agent-runtime.md#8-用户信号的两种归宿) |
| `iteration` | 迭代修改落点 | [04 §7](04-agent-runtime.md#7-迭代式修改--复用-forkpatch) |

### 3.2 两类 source
- `type: workflow` —— 直接对应一个注册的 `WorkflowDefinition`（短剧/带货/图生视频…），`act` 时按 workflow_name 建任务。
- `type: tool_mode` —— 对应一个 `ToolDefinition + ToolMode`（如「文生图」），`act` 时走现有 `tools/tasks` 等价逻辑（携带 `route_key/mode_key`）。

> 这样，**现有「工具/模式」体系**与**现有「工作流」体系**都能被统一描述为 Skill，Agent 无需关心两者底层差异。

## 4. Skill Selector 选择策略

```text
selectSkill(intent, slots, attachments):
    候选 = registry.byIntent(intent)            # 意图标签初筛
    if 候选.isEmpty(): return nil               # → Agent 坦诚降级
    候选 = 候选.filter(可用性)                   # 已发布/未下线/用户有权限（复用现有发布状态）
    if len(候选)==1: return 候选[0]
    # 多候选：用规则 + LLM 在候选内裁决
    候选 = applyHeuristics(候选, slots, attachments)   # 如带货：有参考视频→pro，无→simple
    if len(候选)==1: return 候选[0]
    return llmChooseWithinCandidates(候选)       # 携带各 Manifest description，闭集选择
```

原则：
- **闭集选择**：只在已注册、已发布的 Skill 内选；LLM 永远只在候选里挑，不发明。
- **复用发布状态**：Skill 可用性直接读现有 `status=published / is_active` 等字段——运营在 admin 后台上下线工具，Agent 立刻同步可见/不可见。
- **可解释**：选择理由写入 Plan（调试 & 未来可对用户解释「我用『短剧』来做这个」）。

## 5. 现有能力映射表（Skill 候选清单）

> 基于仓库现有 `ai-engine/workflows/*` 与工具体系整理。第一阶段优先做 ★ 标记的高频 Skill。

| Skill | source.type | 对应现有实现 | 产物 | 第一阶段 |
|-------|-------------|-------------|------|:---:|
| 短剧 | workflow | `short_drama`（`workflows/short_drama`，含 storyboard/i2v/tts/composite） | video | ★ |
| 带货视频(高级) | workflow | `goods_video_pro`（`workflows/goods`） | video | ★ |
| 带货视频(快速) | workflow | `goods_video_simple`（`workflows/goods_simple`） | video | ★ |
| 商品故事视频 | workflow | `commerce_product_story_video`（`workflows/commerce_video`） | video | ○ |
| 视觉带货 | workflow | `visual_goods_video`（`workflows/visual_goods`） | video | ○ |
| 照片动起来 / 图生视频 | workflow | `image_to_video`、`image_to_video_with_motion`（`workflows/videos`、`motion_control`） | video | ★ |
| 文生视频 | workflow | `text_to_video`（`workflows/videos`） | video | ○ |
| 文生图 | tool_mode/workflow | `text_to_image`（`workflows/images`） | image | ★ |
| 图生图 | tool_mode/workflow | `image_to_image`（`workflows/images`） | image | ★ |
| 风格迁移 | tool_mode/workflow | `style_transfer`（`workflows/images`） | image | ○ |
| UI 素材生成 | workflow | `ui_asset`（`workflows/images`） | image | ○ |
| Logo 设计 | workflow | 规划中（Logo Skill，[08](08-roadmap-and-milestones.md)） | image | ○(规划) |
| 数字人 | workflow | `workflows/digital_human` | video | ○ |

★=首批接入 Agent；○=Manifest 后续补齐再接入。**workflow 一个不删**，未接入 Agent 的仍可通过旧入口直达。

## 6. 注册与同步

- **SkillRegistry**：进程启动时加载所有 Manifest（声明式文件 + 可选 DB 覆盖），构建 `intent → []Skill` 索引，校验每个 `source` 指向的 workflow/tool 确实存在（与现有 `WorkflowRegistry.Sync` 同期执行）。
- **一致性校验**（启动期 fail-fast，呼应短剧 `dsl_graph_validity_test.go` 的工程习惯）：
  - 每个 Manifest 的 `source.workflow_name` 必须在 `WorkflowRegistry` 里存在。
  - 每个 `slots[].maps_to` 路径应能对上 workflow input（可做软校验 + 告警）。
  - 每个 `gates[].signal` 应是该 workflow 真实会发的 await signal。
- **运营可改**：description / examples / stages 文案这类「话术」可放 DB，让运营在 admin 调优，无需发版（复用现有 tool CMS 后台范式）。

## 7. 为什么这层要「薄」

- **薄 = 可控**：Skill Layer 不持有业务逻辑，只持有「描述 + 选择 + 映射」。出问题易定位。
- **薄 = 解耦**：换 LLM、加 RAG、上 Multi-Agent，都在 Agent Runtime 改；Skill Layer 几乎不动。
- **薄 = 渐进**：先给 3–4 个高频 Skill 写 Manifest 就能上线；其余 Skill 逐个补 Manifest 接入，不阻塞。

## 8. 与未来来源的兼容（预留）

`source.type` 是开放枚举，未来可加：
- `mcp`：外部 MCP server 暴露的工具，作为 Skill 接入（Agent 无感知差异）。
- `blueprint`：Agent 临时编排多个 Skill 组成的可执行蓝图（[08](08-roadmap-and-milestones.md#blueprint-演进)）。
- `composite`：把若干 workflow 串成的「组合 Skill」（如「抠图→图生视频」），仍以单一 Manifest 对外呈现。

Agent Runtime 只面向 `Skill` 抽象，不关心 `source` 具体是什么——这是这层抽象的价值所在。

---

下一篇：[06 · 客户端架构](06-client-architecture.md) —— Agent 首页、会话式创作、Feed 渲染、与旧入口共存。
