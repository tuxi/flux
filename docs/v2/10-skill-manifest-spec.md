# 10 · Skill Manifest 规范（Workflow → Skill 映射标准）

> 这是 V2 的**关键契约**。它决定「一个 Workflow 如何变成一个 Agent 可调度的 Skill」。Manifest 写对了，Agent 才能真正做到：**理解意图 → 选择 Skill → 收集缺失参数 → 创建 Task**；写不对，Agent 永远只能硬编码。
>
> 本文用 **规范性措辞**：MUST（必须）/ SHOULD（应该）/ MAY（可选）。[05](05-skill-layer.md) 讲设计动机，本文讲**字段标准与校验规则**。

> ⚠️ **实现校正（2026-06，以代码为准；详见 [17](17-implementation-status-and-roadmap.md) §2/§Phase C）**：
> - **as-built 新增字段**：`activity_steps`（执行期过程块骨架，[16](16-activity-stream.md)）；`source.route_key` / `source.mode_key`（**载荷字段**——决定任务能否进入 `/works` 作品列表，缺失则作品不可见）。
> - **当前校验深度（`skill/manifest.go` Validate）仅做**：key 非空、intent 非空、slot.maps_to 存在、required slot 有 ask 或 default。**尚未校验**：workflow 是否存在、gate signal 是否有效、route/mode 是否合法、activity step 唯一性与 matcher、result contract。**接第二个 Skill 前必须补齐**（Phase C）。
> - 注册纪律目标：**无 Manifest 的 Workflow 不允许被 Agent 自动调用**；`LoadEmbedded → Validate → Register`，inactive/invalid 不进 Registry。

## 1. 定位

```text
一个 Workflow  +  一份 Manifest  =  一个 Skill
```

Manifest 是 **Agent 看 Workflow 的唯一视角**。Agent 不读 DAG，只读 Manifest。Manifest 回答 5 个问题：

| 问题 | 字段 | 消费者 |
|------|------|--------|
| 这个 Skill 能干什么？ | `intent` / `summary` / `description` / `examples` | 意图识别 & Skill 选择（[04 §3/§5](04-agent-runtime.md)） |
| 需要哪些信息？ | `slots` | 槽位补全（[04 §4](04-agent-runtime.md#4-槽位补全追问策略)） |
| 怎么执行？多少钱？ | `source` / `needs_plan_confirmation` / `cost` | 启动 & 报价（[04 §5](04-agent-runtime.md#5-技能选择与任务启动)） |
| 执行时给用户看什么？ | `stages` / `gates` | 进度翻译 & 审核（[04 §6/§8](04-agent-runtime.md)） |
| 能怎么改？ | `iteration` | 迭代修改（[04 §7](04-agent-runtime.md#7-迭代式修改--复用-forkpatch)） |

## 2. 顶层结构

Manifest MUST 是一份可校验的声明式文档（YAML 或 JSON）。建议每个 Skill 一份文件，置于 `ai-engine/skill/manifests/<key>.yaml`，启动期注册进 `SkillRegistry`。

```yaml
schema_version: 1          # 规范版本（本规范 = 1）
key: short_drama           # Skill 唯一标识
intent: [short_drama]
title: 短剧
summary: 一句话能力概述
description: 给 LLM 选择/判别用的详细说明（含适合/不适合）
result_type: video         # image | video | audio | timeline
status: active             # active | beta | disabled（运营可控）
phase: p1                  # 接入阶段标记（p1 首批 / p2…）
source: { ... }            # §3
cost: { ... }              # §4
slots: [ ... ]             # §5
stages: [ ... ]            # §6
gates: [ ... ]             # §7
iteration: [ ... ]         # §8
examples: [ ... ]          # few-shot
```

### 2.1 顶层字段规范

| 字段 | 类型 | 必填 | 规则 |
|------|------|:---:|------|
| `schema_version` | int | MUST | 当前固定 `1` |
| `key` | string | MUST | 全局唯一；`^[a-z][a-z0-9_]{1,63}$`；建议 = workflow name |
| `intent` | string[] | MUST | 至少一个；命中这些意图域才进入候选 |
| `title` | string | MUST | 用户可见短名 |
| `summary` | string | MUST | ≤ 40 字，列表/卡片用 |
| `description` | string | MUST | 给 LLM；MUST 含「适合」与「不适合」边界（防误选） |
| `result_type` | enum | MUST | `image\|video\|audio\|timeline` |
| `status` | enum | MUST | `active\|beta\|disabled`；非 active SHOULD 不进默认候选 |
| `phase` | string | SHOULD | 接入阶段标记，便于灰度 |
| `examples` | string[] | SHOULD | 3–8 条真实用户说法，喂意图识别 few-shot |

## 3. `source`（如何执行）

```yaml
source:
  type: workflow              # workflow | tool_mode | composite | mcp（后两者预留）
  workflow_name: short_drama  # type=workflow 时 MUST
  # type=tool_mode 时：
  # route_key: video_generation
  # mode_key: image_to_video
```

| `type` | 含义 | 落地（act） | 现有依据 |
|--------|------|------------|---------|
| `workflow` | 对应一个注册的 `WorkflowDefinition` | 按 `workflow_name` 走 `CreateTaskFromWorkflow` 等价逻辑 | `registry/workflow_registry.go` |
| `tool_mode` | 对应 `ToolDefinition + ToolMode` | 携 `route_key/mode_key` 走 `tools/tasks` 等价逻辑 | `internal/handler/tool/`、`entity/tool_*.go` |
| `composite` | 多 workflow 串成的组合（预留） | Agent 按依赖建多 Task | [08](08-roadmap-and-milestones.md#blueprint-演进) |
| `mcp` | 外部 MCP 工具（预留） | 适配器 | — |

- `source.type` MUST 提供其所需的定位字段。
- 启动期 MUST 校验 `workflow_name`（或 `route_key/mode_key`）在对应注册表中**确实存在**（§9 fail-fast）。

## 4. `cost`（报价与确认）

```yaml
cost:
  needs_plan_confirmation: true   # MUST：是否在执行前出「方案卡」等用户确认
  estimate: quote                 # quote | fixed | none
  fixed_points: 0                 # estimate=fixed 时使用
  hint: "约 300 积分"              # 兜底文案（Quote 不可用时显示）
```

- `needs_plan_confirmation` MUST。昂贵/不可逆（视频生成类）MUST 为 `true`；轻量幂等（文生图）MAY 为 `false`（免确认直跑，降摩擦）。
- `estimate=quote`：执行前调现有 **Quote** 接口拿精确预估，写入 Plan.estimated_cost。
- 客户端在方案卡/修改预览卡 MUST 展示预估，确认后才执行（护栏，见 [04 §10](04-agent-runtime.md#10-llm-用量与护栏)）。

## 5. `slots`（参数 = 槽位）

`slots` 是 Manifest 的核心。每个 slot 定义「一项执行所需信息」：怎么抽、怎么问、怎么映射到 workflow input、怎么校验。

```yaml
slots:
  - key: idea
    title: 故事点子
    type: string                 # string|text|int|number|bool|enum|asset|asset[]
    required: true
    maps_to: input.idea          # MUST：映射到 workflow 的 input 路径（点分）
    ask: "想讲一个什么样的故事？一句话就行"
    extract_hint: "用户对剧情/主题的描述"
  - key: style
    title: 风格
    type: enum
    required: false
    default: urban_romance
    options:
      - { value: urban_romance, label: 都市爱情 }
      - { value: suspense,      label: 悬疑 }
      - { value: comedy,        label: 搞笑 }
    maps_to: input.style_preset
    ask: "想要什么风格？"
  - key: characters
    type: int
    required: false
    default: 2
    min: 1
    max: 4
    maps_to: input.character_count
  - key: product_images
    title: 商品图
    type: asset[]
    required: true
    accept: [image]
    maps_to: input.product_images   # asset_id 引用，复用现有 RegisterTaskInputAssetRefs
    ask: "把商品图发给我（可多张）"
```

### 5.1 slot 字段规范

| 字段 | 类型 | 必填 | 规则 |
|------|------|:---:|------|
| `key` | string | MUST | Skill 内唯一 |
| `type` | enum | MUST | `string\|text\|int\|number\|bool\|enum\|asset\|asset[]` |
| `required` | bool | MUST | 是否必填 |
| `maps_to` | string | MUST | workflow input 路径（点分），如 `input.spec.duration` |
| `default` | any | SHOULD（选填项强烈建议） | 有 default 的缺失项**不追问**，记入 `defaults_applied` |
| `options` | {value,label}[] | enum MUST | 枚举可选项；供「点选式追问」 |
| `min`/`max`/`pattern` | — | MAY | 校验约束 |
| `accept` | string[] | asset MUST | 允许的素材类型 `image\|video\|audio` |
| `ask` | string | required 且无 default 时 MUST | 追问话术 |
| `extract_hint` | string | SHOULD | 给 LLM 的抽取提示 |
| `group` | string | MAY | 同组槽位可在一张卡里一并询问（减少往返） |

### 5.2 填充与追问语义（规范）
对每个 slot，Agent MUST 按此优先级解析（见 [04 §4.1](04-agent-runtime.md#41-三段式填充)）：
1. **抽取**：从用户消息/附件/历史抽。
2. **默认**：抽不到但有 `default` → 用默认，记 `defaults_applied`，方案卡告知「可调整」。
3. **追问**：`required && 无 default && 抽不到` → 入待问队列。

追问 MUST 遵守「少打扰」：能默认就默认、能枚举就给 `options`、同 `group` 批量问、风格类倾向「先出一版再改」（[04 §4.2](04-agent-runtime.md#42-追问的少打扰原则)）。

### 5.3 `maps_to` 映射规范
- `act()` 时，按各 slot 的 `maps_to` 把已填值写进 `RunWorkflowReq.Input`（点分路径，复用 `engine.SetByPath` 同义逻辑）。
- `asset`/`asset[]` slot 的值 MUST 是 `asset_id`（或 `{asset_id,url}`），由 Agent 走现有 `RegisterTaskInputAssetRefs` 注册引用，**不直传 URL 作为权威**。
- 启动期 SHOULD 软校验每个 `maps_to` 能对上目标 workflow 的 input 字段，对不上则告警（§9）。

## 6. `stages`（进度翻译表）

把引擎事件映射为用户文案。这是「进度是翻译不是透传」的落地（[04 §6](04-agent-runtime.md#6-进度翻译translate-dont-forward)）。

```yaml
stages:
  - match: "stage_changed:storyboard"
    text: 正在规划剧情与分镜
  - match: "stage_changed:generating_shots"
    text: "正在逐镜生成画面（第 {i}/{n} 幕）"   # {i}{n} 来自事件 meta / node_index
  - match: "node:voiceover_compose"
    text: 正在配音
  - match: "stage_changed:composite_video"
    text: 正在合成成片
```

- `match` 规则（MUST 命中其一）：`stage_changed:<stage>` | `node:<node_name>` | `event:<task_event_type>`。
- `text` MAY 含 `{i}/{n}/{percent}` 占位，取自事件 `meta` / `node_index` / `progress`。
- **未匹配的引擎事件 MUST 默认不展示**（白名单展示，挡住 `node_complete_async` 等内部细节，见 [04 §6.3](04-agent-runtime.md#63-没有映射的事件)）。
- 百分比直接复用现有 `overall_progress=(node_index+node_progress)/node_total`，不重算。

数据依据：短剧 `stage_changed` 事件见 `workflows/short_drama/short_drama_main_dsl.go` 头注释（TaskEvent types / stages）。

## 7. `gates`（审核闸门：await signal ↔ 会话卡）

声明该 workflow 会发的 `await_user_action` 闸门，及其对应的会话审核卡与回填 signal。

```yaml
gates:
  - card_type: prompt_review_card        # 引擎 await_user_action 的 card_type
    signal: confirm_storyboard_prompt     # 用户确认时回填的 signal_name
    title: 确认分镜脚本
  - card_type: storyboard_review_card
    signal: confirm_storyboard_image
    title: 确认分镜画面
```

- 当引擎推 `await_user_action(card_type=X)` 时，Agent MUST 据此渲染 `review_card`（[09 §2.7](09-conversation-api.md#27-用户信号卡片回应)）。
- 用户回应经 `POST /signals` → `routed_to=engine` → `await.HandleSignal({signal_name=signal, callback_token=task_id, payload})`。
- 启动期 SHOULD 校验 `gates[].signal` 是该 workflow 真实声明的 await signal（短剧的两个 signal 见其 DSL 头注释）。

## 8. `iteration`（迭代修改 → patch/fork）

声明「用户能怎么改」，把口语映射到 fork/patch 的作用域（[04 §7](04-agent-runtime.md#7-迭代式修改--复用-forkpatch)）。

```yaml
iteration:
  - intent_hint: ["第N幕改成…", "某一镜换成…"]
    scope: node                 # node | whole
    target_node: shots          # scope=node 时 MUST：可被 patch 的目标节点
    patch_path: input.shots[{i}].prompt   # 可选：更精确的 patch 落点
  - intent_hint: ["换个音色", "重新配音"]
    scope: node
    target_node: voiceover
  - intent_hint: ["换个风格", "再来一版"]
    scope: whole                # 整体 fork（复用上游、重跑全链或从头）
```

- `scope=node`：Agent 先 `PatchPreview`（预估重跑哪些节点 + 代价）→ 出修改预览卡 → 确认 → `Fork(task, patch)`。
- `scope=whole`：整体再生成一版（新 TaskLink，`relation=fork`）。
- 客户端 MAY 把 `intent_hint` 渲染成成品卡上的快捷 chips（`[换风格][换音色][再来一版]`）。

## 9. 注册与校验（启动期 fail-fast）

`SkillRegistry` 在进程启动时（与 `WorkflowRegistry.Sync` 同期）加载并校验所有 Manifest。校验失败 MUST 阻断启动（fail-fast，呼应短剧 `dsl_graph_validity_test.go` 的工程习惯）：

| 校验 | 级别 | 规则 |
|------|------|------|
| `key` 唯一 | ERROR | 不得重复 |
| `source` 存在性 | ERROR | `workflow_name`/`route_key+mode_key` MUST 在对应注册表存在 |
| `intent` 非空 | ERROR | 至少一个 |
| required slot 有 `ask` 或 `default` | ERROR | 否则无法补全 |
| `slots[].maps_to` 可解析 | WARN | 对不上 workflow input 字段则告警 |
| `gates[].signal` 真实存在 | WARN | 对不上 workflow await signal 则告警 |
| `description` 含适合/不适合 | WARN | 缺边界易误选 |

> 话术类字段（`description`/`ask`/`stages.text`/`examples`）MAY 存 DB，让运营在 admin 调优、免发版（复用现有 tool CMS 后台范式）。结构性字段（`key`/`source`/`slots.maps_to`/`gates.signal`）SHOULD 随代码版本管理。

## 10. 版本与演进

- `schema_version` 标识规范版本；本规范为 `1`。规范升级 MUST 向后兼容或提供迁移。
- 单个 Skill 的 Manifest 变更 SHOULD 走代码评审；运营可调字段走 DB 审计。
- **与 workflow 版本的关系**：workflow 有自己的 `WorkflowVersion`（SHA-256）。Manifest 引用 `workflow_name`（取其最新已发布版本），**不绑定具体 hash**——workflow 升级对 Agent 透明，除非 input 契约变化（那时 `maps_to` 校验会告警）。

## 11. 完整示例

### 11.1 短剧（`source.type=workflow`，含 gates）
见 §2–§8 各片段；对应 `workflows/short_drama`。要点：`needs_plan_confirmation:true`、两个 await gate、node 级 iteration（逐镜/配音）。

### 11.2 带货视频·高级（`goods_video_pro`）
```yaml
schema_version: 1
key: goods_video_pro
intent: [goods_video, ecommerce_video]
title: 带货视频（高级）
summary: 用商品图生成有卖点脚本的口播带货视频
description: >
  适合：有商品图、想要成片带卖点/口播/字幕的电商视频。
  不适合：无商品图的纯创意短剧、单图静态海报。
result_type: video
status: active
phase: p1
source: { type: workflow, workflow_name: goods_video_pro }
cost: { needs_plan_confirmation: true, estimate: quote, hint: "约 400 积分" }
slots:
  - { key: product_images, title: 商品图, type: asset[], required: true, accept: [image], maps_to: input.product_images, ask: "把商品图发给我（可多张）" }
  - { key: selling_points, title: 卖点, type: text, required: false, maps_to: input.selling_points, ask: "有想突出的卖点吗？没有我来提炼", extract_hint: "用户对卖点/受众/场景的描述" }
  - { key: duration_sec, type: int, required: false, default: 15, maps_to: input.duration }
  - { key: aspect_ratio, type: enum, required: false, default: "9:16", options: [ {value:"9:16",label:竖屏}, {value:"16:9",label:横屏} ], maps_to: input.aspect_ratio }
stages:
  - { match: "node:analyze_product_image", text: 正在分析商品 }
  - { match: "node:generate_creative_brief_v2", text: 正在撰写脚本与卖点 }
  - { match: "stage_changed:generating_shots", text: 正在生成画面 }
  - { match: "node:voiceover_compose", text: 正在配音 }
  - { match: "stage_changed:composite_video", text: 正在合成成片 }
iteration:
  - { intent_hint: ["口播太长", "脚本改短", "换个卖点"], scope: node, target_node: generate_creative_brief_v2 }
  - { intent_hint: ["换个音色"], scope: node, target_node: voiceover }
  - { intent_hint: ["再来一版", "换个风格"], scope: whole }
examples:
  - "用这几张耳机图做个带货视频"
  - "帮我做条口红的种草视频，突出显白"
```

### 11.3 文生图（`text_to_image`，轻量免确认）
```yaml
schema_version: 1
key: text_to_image
intent: [text_to_image, image_generation]
title: 文生图
summary: 用一句话生成图片
description: "适合：从文字描述生成图片/封面/素材。不适合：基于已有图改（用图生图）、做视频。"
result_type: image
status: active
phase: p1
source: { type: tool_mode, route_key: image_generation, mode_key: text_to_image }
cost: { needs_plan_confirmation: false, estimate: quote, hint: "约 10 积分" }   # 轻量→免确认直跑
slots:
  - { key: prompt, title: 描述, type: text, required: true, maps_to: input.prompt, ask: "想画什么？描述越具体越好" }
  - { key: aspect_ratio, type: enum, required: false, default: "1:1", options: [ {value:"1:1",label:方形}, {value:"9:16",label:竖屏}, {value:"16:9",label:横屏} ], maps_to: input.aspect_ratio }
  - { key: count, type: int, required: false, default: 1, min: 1, max: 4, maps_to: input.count }
stages:
  - { match: "event:task_started", text: 正在生成图片 }
iteration:
  - { intent_hint: ["再来几张", "换一批"], scope: whole }
  - { intent_hint: ["改成竖版", "换比例"], scope: whole }
examples:
  - "画一只赛博朋克风格的猫"
  - "生成一张小红书封面，主题是露营"
```

## 12. 与现有元数据的对账（落地省力）

Manifest 多数字段**聚合现有数据**而非新造（[05 §2](05-skill-layer.md#2-manifest-的原材料已经在库里)）：

| Manifest 字段 | 现有来源 |
|--------------|---------|
| title/summary/description | `ToolDefinition/ToolMode` 的 Title/Subtitle/Description |
| source.route_key/mode_key | `ToolDefinition.RouteKey` / `ToolMode.Key` |
| result_type | `WorkflowDefinition.Output.ResultType` |
| stages.match | workflow `stage_changed` 事件 / 节点名 |
| gates | workflow await `card_type`/`signal`（DSL 头注释） |
| cost.estimate=quote | 现有 Quote 接口 |
| status | `ToolDefinition.Status/IsActive`（运营上下线即时生效） |

> 因此首批 3–4 个 Skill 的 Manifest 工作量主要在：补 `intent`/`examples`、给 `slots` 写 `ask` 与 `maps_to`、给 `stages` 写用户文案。**不是从零造一套元数据。**

---

下一篇：[11 · Agent 状态机](11-agent-state-machine.md)。
