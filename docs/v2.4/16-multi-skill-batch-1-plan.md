# 16 · Multi-Skill Batch 1 接入计划与验收门

> 状态：设计文档（评审中），**不写 Go 业务实现**。评审通过后先实现 1A（公共地基），再接 t2i / logo。
> 上位：[00 Planner Overview](00-agent-planner-overview.md) · [03 Skill Selection & Catalog](03-skill-selection-and-catalog-observation.md) · [06 Plan Card](06-plan-card-generation.md) · [13 Result Card & Activity UX](13-result-card-and-activity-ux.md)。
> V2.1 字段对账：[v2.1/01 text_to_image](../v2.1/01-text-to-image-skill.md) · [v2.1/02 image_to_image](../v2.1/02-image-to-image-skill.md) · [v2.1/04 Contract Compiler](../v2.1/04-skill-contract-compiler.md)（缺口 G1–G21）。
> 审计基准：`feat/agent-first-v2` 真实代码，时间 2026-06。所有 file:line 为审计当时引用，实现前需复核。

---

## 0. 背景与本轮定位

Agent Runtime 已通过 `short_drama` 验证（V2.1~V2.4 全链路）。下一步**不在短剧上继续堆特判**，而是把已有工具按 V2.4 Skill 接入 Planner Catalog，让多 Skill 走**统一**的 Manifest → Catalog → Planner → ActionPlan → PlanCard/ResultCard 链路。

本轮（Batch 1）的目标不是"多接几个功能"，而是：**用最便宜、最确定的两个 Skill（text_to_image + logo_design）把 Catalog/Planner 的"通用性"逼出来，并把当前残留的 demo / short_drama 特判清理为公共能力。** 与 [v2.1/00 §1](../v2.1/00-skill-integration-and-stability.md) 的"由简到繁"梯度一致，但落在 V2.4 Planner 之上。

### 0.1 接入一个 Skill 的"四件套"（硬约束）

`CompiledSkill` 由 `compiler.Compile()`（`ai-engine/agent/compiler/compiler.go`）从**三个来源**合成，缺一即在启动 `BuildRegistry`（Isolated 策略）时被 **disable**：

```text
① Manifest YAML            agent/skill/manifests/<key>.yaml          —— intent / slots / bindings / planning / 展示元数据
② DB ToolModeVersion       tool_def / tool_mode / tool_mode_version  —— contract.Resolver 解析 route_key→mode_key→active version → workflow / 默认值 / options
③ Workflow Audit 文件      workflows/<name>/input_contract.audit.yaml —— 执行必填项（EffectiveRequired），由 server 装配处 LoadAuditFile + Register
④ 对应公共能力             见 §3（1A）/ §6（asset 地基）
```

> 当前只有 `short_drama` 的 audit 文件被注册（`server.go:746`）。**只写 Manifest 不补 audit ⇒ Skill 启动即 disable。**

### 0.2 已经就绪、可直接复用（避免重复造轮子）

| 能力 | 证据 | 说明 |
|---|---|---|
| 固定常量入参（旧 G1） | `skill.Binding{from: skill_constant}` + compiler required-coverage | `input.mode="text_to_image"` 用 binding 注入即可，**G1 不再是阻塞** |
| InputPlanningPolicy 五策略 | `domain/planner_types.go:184-188` | `ask_user / creative_default / system_default / cost_affecting_default / requires_asset` |
| PlanCard = ActionPlan 投射 | `plancard/adapter.go`、[06](06-plan-card-generation.md) | 展示元数据来自 `manifest.planning.inputs.*.display` + `plan_stages` |
| ResultCard 通用动作 | `observer.go:310-314` `regenerate_result`/`modify_result`；`conversation_service.go:263` 处理 | **开箱即用，不绑短剧** |
| 契约版本固定 | `CompiledRegistry.PinFor` + `PlanVerifier`（confirm/launch 二次校验） | 新 Skill 自动获得 pin_on_plan |
| /works 归类 | task 带 entry_type/route_key/mode_key 进 `ListByUserV2` | route/mode 来自 manifest.source.contract |

---

## 1. Batch 1 范围

```text
1A  公共地基（common foundation）   —— 必做，先做。清理特判，让 Catalog 真正驱动
1B  text_to_image                   —— 纯文本出图，最快闭环，作为 L1 高频回归基线
1C  logo_design                     —— 映射 text_to_image mode，验证"一个 workflow 多个 Skill"
```

**为什么是这三项**：text_to_image 无 await 闸门、无 asset 依赖、成本最低（约 8 积分），是验证 Planner 通用性的最小闭环；logo_design 复用同一 workflow，专门验证"差异化只在 Manifest 层、不在 runtime 写特判"。两者都**不依赖**尚未落地的会话图片入口（G5），因此能在 1A 之后立即落地。

### 1.1 验收总纲（Batch 1 Definition of Done）

1. 启动后 `CompiledRegistry` 至少含 `short_drama / text_to_image / logo_design` 三个 `status=enabled`，diagnostics 无 disabled。
2. 下列对话不再"泛化拒绝"、不串短剧文案：
   - "画一只赛博朋克的猫，竖版" → text_to_image，出 PlanCard；
   - "生成一个极简 Logo" → logo_design，出 PlanCard（**不得拒绝、不得误判为 text_to_image 后丢失 logo 语义**）；
   - "你会做什么" → 基于**已启用 Catalog** 枚举真实能力（含短剧 + 图片 + Logo），**不得只回短剧**。
3. 接入后**无新增** short_drama / demo 特判；§3 列出的现存特判被清除或通用化。
4. 不绕过 ActionPlan / Contract Validator；不回退传统表单工具逻辑。
5. `short_drama` 创建 / 确认 / review 修改 / 再来一版闭环**零回退**（回归矩阵 §7）。

---

## 2. Deferred（本轮明确不接，避免范围蔓延）

| Skill | 推迟原因 | 解阻条件 |
|---|---|---|
| **image_to_image** | 依赖会话图片入口 G5 + 指代解析 G6 + 资产引用登记 G7（§6）。当前 `handler/dto.go` 入站只有 `Text`，用户无法把图带进会话 | 先落地 §6 的 asset 地基（Batch 2） |
| **image_to_video** | 同上，asset 类；且 `videos.ImageToVideoWorkflowDSL` 的 active mode 需核对实库 | asset 地基 + 视频结果卡核对（Batch 2） |
| **product_image** | goods/ 下**无"一句话出商品主图"工作流**（现有均为商品视频 `goods_video_pro` / 扣图 `clean_product_image` / 分析 `analyze_product_image`）。需先选定出图链路 | 单独立项：新建 product_image workflow 或降级映射 i2i+模板（地基稳定后） |

> 注意"把这张图改成动漫风"的需求拆成两半：**无图→追问上传**（`requires_asset` policy → `ask_user`）这一半**仅靠 Planner 即可**，不依赖 G5；但**带图→真正出图**这一半必须等 §6。Batch 1 不接 i2i，意味着"改成动漫风"在 Batch 1 暂只到"请先上传图片"为止，完整闭环在 Batch 2。

---

## 3. 1A · 公共地基（先做，决定后续不再堆特判）

三项改造，全部属于"把已固化的 short_drama / demo 特判改成 Catalog 驱动"，无新 Skill 也应做。

### 3.1 Catalog-driven Help / Identity（硬指标："你会做什么"）

**现状（特判）**：`persona.go:18-20` 的 `DefaultPersona()` 把 Help / Identity / HelpBrief 写死成短剧文案（"我现在能帮你创作 AI 短剧…"）。`runtime.go:446-448` `metaReply(IntentHelp/Identity)` 直接返回静态串；`SetPersona` 从未用 Catalog 数据调用。⇒ **违反"你会做什么要基于已启用 SkillCatalog 返回真实能力"。**

**目标**：Help / Identity 回复由 `CompiledRegistry` 中 `status=enabled` 的 Skill 动态拼装。

```text
metaReply(IntentHelp) =
  枚举 catalog.EnabledSkills()
    -> 取每个 CompiledSkill.Manifest.{title, summary}
    -> 生成 "我现在能帮你：① AI 短剧… ② AI 图片… ③ Logo 设计…，想做哪个？"
  blocking stage 下用精简版（HelpBrief 同理 catalog 驱动）+ helpAnchor(c) 锚回当前任务
```

约束：
- 文案模板可留在 persona（语气可调），但**能力清单必须来自 Catalog 实时枚举**，新增/下线 Skill 自动反映。
- 短剧 review/confirm 等 blocking stage 仍优先锚回当前任务（保持 [v2.2] 行为）。
- **展示顺序必须稳定、确定**：Catalog 枚举不得依赖 map 迭代顺序（Go map 随机）。按显式 `display_order`（Manifest 可选字段，缺省回退 `key` 字典序）排序后再拼装，保证同一 Catalog 每次回复顺序一致、可回归断言。建议短剧/图片/Logo 的相对次序由 `display_order` 固定。
- 不读 DB、不调 LLM；纯内存 Catalog 投射。

**验收**：仅启用短剧时"你会做什么"只列短剧；启用 t2i/logo 后自动出现三项；disable 某 Skill 后该项消失。

### 3.2 IntentHints / examples / asset 驱动的 scoreSkill

**现状（特判）**：`planner/planner.go` 的 `scoreSkill`（约 :317-360）写死关键词加分——`视频→含"video"的 key +0.5`、`图/图片→含"image"的 key +0.5`、`短剧 +0.5`、`赛博朋克 +0.2`；`inferCreativeDefault`（约 :380）硬编码 `bgm_style + 赛博朋克`。问题：
- `text_to_image` 与 `image_to_image` **都含 "image"** ⇒ "改成动漫风"会同时 +0.5，真正的区分信号（"这张/已有图"、是否带 asset）没进打分；
- `logo_design` 的 key 不含 image/video ⇒ 关键词路径恒为 0 分，能否命中**全靠 IntentHints**，漏配即"泛化拒绝"。

**目标**：`scoreSkill` 改为**结构化信号驱动**，对齐 [03 §6/§7/§8](03-skill-selection-and-catalog-observation.md)：

| 信号 | 来源 | 建议权重 |
|---|---|---|
| explicit skill mention（"logo/标志"、"短剧"、"做张图"） | `CompiledSkill.IntentHints` / TurnInterpretation | +0.35 |
| goal type exact match | `IntentHints.GoalTypes` | +0.30 |
| manifest example 命中 | `CompiledSkill.Examples` | +0.10 |
| asset compatibility（带图 → 利于 i2i/i2v；纯文 → 利于 t2i） | current turn assets / ActiveObjects | +0.15 |
| negative hint 命中（"天气"等非创作） | `IntentHints` 负例 | −0.40 |
| current plan skill 延续 | AgentState / CurrentPlan | +0.35 |

仲裁沿用 [03 §8]：上下文延续 > 对象能力 > **显式 Skill 优先** > 高置信单候选（top≥0.75 且领先 second≥0.15）> 分数接近则追问消歧 > 无候选则拒绝/锚回。

关键纠正（直接服务 logo 验收）：
- **显式 logo 提及必须压过泛化"画/生成图"**：避免"画一个 logo"被 t2i 与 logo_design 打平触发误消歧（[03 §8.3] 显式 Skill 优先）。
- 删除 `image/video/short_drama` 字符串特判与 `赛博朋克/bgm_style` 硬编码；保留为可被任意 Skill 复用的通用信号 + 各 Manifest 自带 IntentHints。
- creative_default 的推断逻辑通用化（按 field + 主题，不写死单一 field/主题）；短剧 bgm_style 的推断改由短剧 Manifest 的 planning 规则声明。

**验收**：见 §7 回归矩阵的 SkillSelection 段（含 logo 显式命中、t2i 纯文命中、负例不误命中）。

### 3.3 去 demo / short_drama 特判清单（与新 Skill 解耦）

| 位置 | 特判 | 改法 |
|---|---|---|
| `persona.go` / `runtime.go:446` | Help/Identity 写死短剧 | §3.1 Catalog 驱动 |
| `planner.go scoreSkill` | image/video/drama/赛博朋克 关键词 | §3.2 IntentHints 驱动 |
| `planner.go inferCreativeDefault` | `bgm_style+赛博朋克` 硬编码 | 通用 creative_default + 短剧 Manifest 声明 |
| `runtime.go` 残留文案/anchor（[v2.1/00 G10]） | pendingAnchor/contextualFallback/headline 串短剧 | 文案改 Manifest 驱动（title/activity_headline/slot.ask） |

> 红线：**1A 的任何改动都不得引入"if skillKey == X"的新特判**；通用化是唯一允许方向。

---

## 4. 1B · text_to_image 接入规格

字段与真实 workflow `images.TextToImageWorkflowDSL`（注册 `server.go:534`）对账；执行必填来自 `text_to_image_param_validate.go` 的 `requiredFields=["user_prompt", mode]` 且 `mode` 必须 `=="text_to_image"`。

### 4.1 Manifest（`agent/skill/manifests/text_to_image.yaml`）

```yaml
schema_version: 1
key: text_to_image
intent: [text_to_image, image_generation]
title: AI 图片生成
summary: 根据文字描述生成图片 / 插画 / 海报 / 封面
description: >
  适合：从一句文字描述生成图片/插画/海报/封面/素材。
  不适合：基于已有图片修改（用 image_to_image）、让图片动起来或做视频。
result_type: image
status: active

source:
  type: workflow
  workflow_name: text_to_image
  route_key: image_generation        # 实库确认：tool_def id=2
  mode_key: text_to_image            # 实库确认：tool_mode 2001 / active v15
  contract:
    route_key: image_generation
    mode_key: text_to_image
    version_policy: pin_on_plan

cost:
  needs_plan_confirmation: true      # Batch 1 上线前【保持 true】：先统一 PlanCard 确认体验；降为 false 减摩擦留待 Batch 1 之后单独评估
  estimate: quote
  hint: "约 8 积分"

slots:
  - key: prompt
    title: 图片描述
    type: text
    required: true
    maps_to: input.user_prompt
    ask: "想画什么？描述越具体越好（主体、风格、场景、光线都可以说）"
    extract_hint: "用户对画面内容/主体/风格的描述"
  - key: aspect_ratio
    title: 画幅
    type: enum
    required: false
    default: "1:1"
    maps_to: input.aspect_ratio
    options:
      - { value: "1:1",  label: 方形 }
      - { value: "9:16", label: 竖版 }
      - { value: "16:9", label: 横版 }
  - key: style
    title: 风格
    type: string
    required: false
    maps_to: input.style
    ask: "想要什么风格？（写实 / 动漫 / 油画 / 赛博朋克…，不指定我来把握）"

# 执行输入来源声明（V2.1-0B.2）。固定常量 mode 用 skill_constant 注入（替代旧 G1）。
bindings:
  - { target: input.user_prompt,   from: slot, key: prompt }
  - { target: input.aspect_ratio,  from: slot, key: aspect_ratio }
  - { target: input.style,         from: slot, key: style }
  - { target: input.mode,          from: skill_constant, value: text_to_image }
  - { target: input.callback_token, from: system_injected, resolver: task_id }

# V2.4 输入规划策略 + PlanCard 展示元数据
planning:
  inputs:
    user_prompt:
      policy: ask_user
      ask_prompt: "想画什么？描述越具体越好"
      display: { label: 图片描述, display_type: text, editable: true }
    aspect_ratio:
      policy: system_default
      default: "1:1"
      display: { label: 画幅, display_type: enum, editable: true }
    style:
      policy: creative_default
      default_strategy: infer_from_prompt   # 抽不到则交模型，置信不足不强加
      confidence_floor: 0.7
      display: { label: 风格, display_type: text, editable: true }

plan_stages: ["理解图片需求", "生成图片", "保存作品"]
gates: []                              # 无 await 用户闸门
activity_steps:
  - { id: analyze,  label: 理解图片需求, match: "event:task_started" }
  - { id: generate, label: 生成图片,    match: "node:provider_router" }
  - { id: save,     label: 保存作品,    match: "node:upload_result" }
stages:
  - { match: "event:task_started",   text: "正在理解你的图片需求" }
  - { match: "node:provider_router", text: "正在生成图片" }
  - { match: "node:upload_result",   text: "正在保存作品" }

examples:
  - "画一只赛博朋克风格的猫"
  - "生成一张小红书封面，主题是露营，竖版"
  - "帮我画一个动漫风格的女孩"
  - "来张极简风的咖啡店海报"
```

> `activity_steps/stages` 的 `match` 节点名需真机校准（[v2.1/01 §2 注]）。

### 4.2 Audit 文件（`workflows/images/input_contract.text_to_image.audit.yaml`）

```yaml
schema_version: 1
workflow_name: text_to_image
audit_revision: 1
required_fields:
  - { target: input.user_prompt, reason: validator_data_schema }   # requiredFields[0]
  - { target: input.mode,        reason: validator_data_schema }   # requiredFields[1]，且严格 == text_to_image
audit_warnings:
  # 以下字段有 mode_default 兜底（covered_by_default），但 Execute() 有白名单/枚举命令式约束 → 标 covered_but_unverified
  - { code: execute_whitelist, message: "model 受 supportsConfiguredTextToImageModel 白名单约束", target: input.model }
  - { code: execute_enum,      message: "size/aspect_ratio/quality/background 受枚举校验", target: input.aspect_ratio }
```

要点：
- `input.mode` 由 `skill_constant` 覆盖（`covered_by_producer`），`input.user_prompt` 由 slot 覆盖；coverage 审计通过。
- whitelist/enum 字段不是 required，由 mode_default 兜底；compiler 将其标 `covered_but_unverified` + warning（[v2.1/04] Validator Execute() 命令式约束 compiler 只证覆盖不证有效）。
- 装配：`server.go` 装配处 `LoadAuditFile(images.TextToImageInputContractAudit, "text_to_image")` 并 `Register`（与 short_drama 同样式，`server.go:746`）。

### 4.3 PlanCard（ActionPlan 投射）

```text
图片描述：<user_prompt>
画幅：<aspect_ratio>（默认方形 · 可改）
风格：<style 或「自动」>
预计消耗：约 8 积分            # 接 Quote 后为精确值（G8，沿用短剧既有缺口，不在 Batch 1 阻塞）
生成阶段：理解需求 → 生成图片 → 保存作品
[开始生成] [调整一下]
```
- `editable` 来自 `planning.inputs.*.display.editable`（aspect_ratio / style 可改）。
- **不得**出现短剧 derived（keyframe/segment/shot）——这是 1A §3.2/[v2.1/00 G2] 要清的脏 derived。

### 4.4 ResultCard actions

复用通用 `observer.go:310-314`：

```json
{ "kind": "result_card", "result_type": "image", "primary_file_url": "<image_url>",
  "actions": [ {"signal": "regenerate_result", "label": "再来一张"},
               {"signal": "modify_result",     "label": "改一下"} ] }
```
- `regenerate_result` → 沿用源 plan slots 整体重生成（whole-fork）；
- `modify_result` → 先追问"想怎么改"，反馈合并回源 plan 重生成（`conversation_service.go:263` 既有逻辑）。
- 文案"再来一张/换风格"可按 result_type 渲染（客户端或 Manifest 文案），属优化非阻塞。

---

## 5. 1C · logo_design（复用 text_to_image mode）

**决策（已拍板）**：logo_design **复用 `image_generation/text_to_image` 的 mode** —— 零 DB 改动，最快落地；接受 /works 归"AI 图片生成"类、计费走图片档。差异化**只在 Manifest 层**。

```yaml
schema_version: 1
key: logo_design
intent: [logo_design, logo]
title: AI Logo 设计
summary: 用一句话生成 Logo / 标志 / 标识
description: >
  适合：生成 Logo、标志、品牌标识、应用图标等。
  不适合：通用插画/海报（用 text_to_image）、基于已有图修改（用 image_to_image）。
result_type: image
status: active

source:                                # 与 text_to_image 解析到同一 ToolModeVersion
  type: workflow
  workflow_name: text_to_image
  route_key: image_generation
  mode_key: text_to_image
  contract: { route_key: image_generation, mode_key: text_to_image, version_policy: pin_on_plan }

cost: { needs_plan_confirmation: true, estimate: quote, hint: "约 8 积分" }

slots:
  - key: prompt
    title: Logo 描述
    type: text
    required: true
    maps_to: input.user_prompt
    ask: "想要什么样的 Logo？说说品牌名、行业、想表达的感觉（极简/科技/可爱…）"
    extract_hint: "品牌名 / 行业 / 风格意图"

bindings:
  - { target: input.user_prompt, from: slot, key: prompt }
  - { target: input.mode,        from: skill_constant, value: text_to_image }
  # 注入 logo 模板词到 style（sealed），不污染 user_prompt；用户的"赛博朋克"等仍进 user_prompt
  - { target: input.style,       from: skill_constant, value: "极简扁平矢量 logo，干净留白，居中构图，纯色背景，可商用品牌标识" }
  - { target: input.callback_token, from: system_injected, resolver: task_id }

planning:
  inputs:
    user_prompt:
      policy: ask_user
      ask_prompt: "想要什么样的 Logo？品牌名 / 行业 / 风格都可以说"
      display: { label: Logo 描述, display_type: text, editable: true }

plan_stages: ["理解 Logo 需求", "生成 Logo", "保存作品"]
gates: []
activity_steps:
  - { id: analyze,  label: 理解 Logo 需求, match: "event:task_started" }
  - { id: generate, label: 生成 Logo,     match: "node:provider_router" }
  - { id: save,     label: 保存作品,      match: "node:upload_result" }
examples:
  - "生成一个极简 Logo"
  - "帮我设计一个咖啡店的标志"
  - "做一个科技公司 logo，蓝色调"
  - "给我的 App 设计一个图标"
```

设计要点与风险：
1. **同一 workflow 多 Skill 合法**：两个 Manifest 的 `source.contract` 解析到同一 `ToolModeVersion`，compiler 身份校验（route/mode/workflow 三者一致）通过，各自得到独立 `CompiledSkill` 与 ContractPin。
2. **差异化全在 Manifest**：intent/examples/title/slot.ask + `input.style` 的 logo 模板常量。**runtime/planner 不得为 logo 写任何特判**——这正是验证 1A §3.2 通用 scoreSkill 的目的。
3. **`input.style` 设为 sealed（skill_constant 必为 sealed）**：用户无法改 style 槽（符合"这就是个 logo"语义）；风格词（如"赛博朋克 logo"）走 `user_prompt` 仍生效。
   - **Batch 1 落地用 `skill_constant`（最简、零 launcher 改动）**；但**预留升级路径**：当需要把品牌名/行业/风格结构化合成进 logo prompt（而非简单常量）时，改用 `derived` binding（`{target: input.user_prompt 或 input.style, from: derived, resolver: logo_prompt_compose, sealed: true}`），由 launcher 侧注册的 resolver 把 slot 值套进 logo prompt 模板。Manifest 字段（`derived`/`resolver`/`sealed`）已就绪（`skill/binding.go`），**升级只需新增 resolver 实现，不改 Manifest schema、不改 Planner**。本轮不实现，仅锁定接口形态，避免 skill_constant 成为死路。
4. **选择风险（1A 必须先解决）**：`logo_design` key 不含 "image"，依赖 IntentHints 显式命中。1A §3.2 须保证"显式 logo 提及 > 泛化画/生成图"，否则"画一个 logo"会与 t2i 打平触发误消歧。
5. **/works 与计费**：entry_title=manifest.title（"AI Logo 设计"），但 route/mode=image_generation/text_to_image → 列表归图片类、计费图片档。已接受。若后续要独立归类/计费，再 DB 播种独立 mode（非 Batch 1）。
6. **audit 复用**：logo 不需新 audit，复用 text_to_image 的（同 workflow_name）。

---

## 6. asset 类前置依赖（G5/G6/G7）—— Batch 2 解阻，本轮仅锁定

i2i / i2v / product_image 的真正闭环依赖以下公共能力（[v2.1/02](../v2.1/02-image-to-image-skill.md) 已详述），**Batch 1 不实现，但在此锁定为 Batch 2 入口门槛**：

| 缺口 | 现状 | 需要的公共改动 |
|---|---|---|
| **G5 会话图片入口** | `handler/dto.go` 入站 DTO 只有 `Text`；`ConversationContext` 不暴露 assets | 消息 DTO 增 `assets:[{asset_id,asset_type}]`；service 写入 `Message.ContentJSON`；Context 暴露；校验 ownership==会话 userID |
| **G6 指代解析** | 无人把"这张/上一张/刚生成的图"解析为 asset_id | AgentState 增资产上下文（`last_result_asset_id` / `recent_uploaded_assets`）；不确定时**追问，不静默选图** |
| **G7 资产引用登记** | Agent launcher 不调 `RegisterTaskInputAssetRefs`（工具路径 `tool.go:522` 有） | launcher 创建 task 后对 asset 类 input 登记，所有权/生命周期/签名一致 |

**Batch 1 行为（i2i 未注册）**："改成动漫风"无图 → Catalog 无 i2i 候选 → Planner 判定为"可识别的创作目标但无匹配 Skill" → 回 `plannerCreationUnsupportedText`（"图片编辑能力即将上线，可先上传图片…"）。**Batch 1 回归只断言两点：① 不误选 `text_to_image`；② 给出能力即将上线 / 请上传图片的说明**——不要求选中 disabled/未注册的 i2i。

**Batch 2 行为（i2i 注册后、G5 之前/之后）**：`requires_asset` policy 已就绪（`planner.go` planInputs 分支）——"改成动漫风"无图 → 选中 i2i → `source_image=requires_asset` → `ask_user` 追问上传。这才是 [03 §11]"不得因为缺图就错误选择 text_to_image"的完整形态，移到 Batch 2 回归。

---

## 7. 回归矩阵（验收）

### 7.1 SkillSelection（1A + 1B + 1C 核心）
- [ ] "画一只赛博朋克的猫，竖版" → `text_to_image`，PlanCard，aspect_ratio=9:16；不误命中 short_drama/logo。
- [ ] "生成一个极简 Logo" → `logo_design`，PlanCard；**不拒绝、不丢 logo 语义**。
- [ ] "画一个 logo" → `logo_design`（显式 logo > 泛化画），**不触发"图片/视频/短剧"误消歧**。
- [ ] "做个都市爱情短剧" → `short_drama`，不被图片 Skill 抢。
- [ ] "帮我做一个赛博朋克"（无偏向）→ 追问消歧（图片/视频/短剧），**不默认短剧**。
- [ ] "把这张改成动漫风"（无图，**i2i Batch 1 未注册**）→ **不误选 `text_to_image`** + 给出"图片编辑能力即将上线 / 请先上传图片"说明（`plannerCreationUnsupportedText`）。**不要求**选中 disabled/未注册的 i2i——选中 i2i + `requires_asset` 追问属 Batch 2 回归（§6）。
- [ ] 负例"今天天气怎么样" → out_of_scope，不创建 Skill plan；有 active 任务则锚回。

### 7.2 "你会做什么"（硬指标）
- [ ] 仅启用 short_drama 时 → 只列短剧。
- [ ] 启用 t2i/logo 后 → 列出短剧 + 图片 + Logo（来自 Catalog 实时枚举）。
- [ ] disable 某 Skill → 该项从回复消失。
- [ ] blocking stage（短剧 review 中）问"你会做什么" → 精简能力句 + 锚回当前任务。
- [ ] **展示顺序确定**：同一 Catalog 多次提问，能力清单顺序完全一致（按 `display_order`→`key` 排序，不受 map 迭代随机性影响）——可作快照断言。

### 7.3 闭环（t2i / logo）
- [ ] PlanCard 字段正确，**无短剧脏 derived**。
- [ ] 确认后 Activity 三步原地步进；只产**一个**最终 result_card（content_audit_map 子任务不升格刷屏）。
- [ ] 图片进 /works，可打开 creative-detail。
- [ ] 失败（审核拦截/param_validate.is_valid=false）→ **一个** error_card，不产成功作品。
- [ ] `regenerate_result`（再来一张）保留源 prompt 重生成；`modify_result`（改一下）先追问再重生成。
- [ ] logo：style 模板常量已注入（sealed），用户描述进 user_prompt 生效。

### 7.4 契约与稳定性
- [ ] 启动 diagnostics：三 Skill 全 `registered`，无 disabled；audit 文件均加载。
- [ ] ContractPin 在 confirm/launch 二次校验通过；active 版本变更时按 pin_on_plan 不静默用新契约。
- [ ] **无新增** short_drama/demo 特判（grep `== "short_drama"` / 硬编码关键词 应只剩白名单内）。
- [ ] `short_drama` 创建 / 确认 / review 修改 / 再来一版 **零回退**（[03 §17] / [10 回归矩阵]）。

---

## 8. PR 切分与门控

```text
PR-16A  本设计文档（评审门）                              —— 当前
PR-16B  1A 公共地基：persona catalog 驱动 + scoreSkill 通用化 + 去 demo 特判 + audit 加载机制
PR-16C  1B text_to_image：manifest + audit + 注册 + 决策快照/端到端测试 + L1 回归
PR-16D  1C logo_design：manifest（复用 t2i mode）+ 端到端 + 选择消歧回归
PR-16E  Batch 1 回归：short_drama 零回退 + §7 全矩阵
```

门控（沿用 [11 final-review-and-implementation-gate]）：
- **本文档评审通过前，不进入任何 Go 实现。**
- 实现顺序严格 1A → 1B → 1C；1A 不达成（你会做什么仍写死 / scoreSkill 仍特判）则不接任何新 Skill。
- 每个 PR 必须带回归，且不得破坏 short_drama 闭环。

---

## 9. 未决/需在实现前复核
1. `text_to_image` 真机 `node:provider_router` 是否为合适的"生成中"锚点（activity match 校准）。
2. logo 显式命中阈值：1A scoreSkill 落地后用回归矩阵锁定权重，避免与 t2i 打平。
3. Quote/计费一致性（G8）：t2i/logo PlanCard 估价目前为 hint 兜底；与短剧同属既有缺口，本轮不阻塞但需标注"估价为占位"。
4. logo `input.style` 模板词需与设计/运营确认（影响出图风格一致性）。
5. README 索引补 `16-multi-skill-batch-1-plan.md` 链接。
