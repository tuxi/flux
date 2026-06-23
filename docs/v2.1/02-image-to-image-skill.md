# V2.1 · 02 · image_to_image Skill 接入规格（Level 2）

> 目标：验证 **Asset 类型 Slot、会话内图片引用、AgentState 资产上下文、连续修改**。这是 V2.1 真正考验公共架构的一层。
> 字段与真实 workflow `ai-engine/workflows/images/image_to_image_workflow_dsl.go` 对账。
> 上位：[00 总体方案](00-skill-integration-and-stability.md)（缺口编号 G1–G13）、[01 text_to_image](01-text-to-image-skill.md)。

> ⚠️ **本 Skill 依赖一组当前不存在的公共能力**（G5/G6/G7/G4/G12）。在主架构落地这些能力前，image_to_image **无法在不特判的前提下闭环**。本文既是接入规格，也是这组公共能力的需求说明。

---

## 1. 真实 Workflow 输入输出（对账）

**Workflow**：`Name: "image_to_image"`，注册于 `server.go:531`。
特征：`merge_i2i_params` 归一化（支持顶层 input 与 `input.spec.*` 两种注入，后者用于被当作 Map 子工作流调用）→ 内容审核 → 参数校验 → 路由 → submit/wait → 下载/后处理/上传。**无 await 用户闸门**。provider 限 **aliyun / volcengine**，模型白名单（`image_to_image_param_validate.go:147/154`）。

### 1.1 输入（`input.*` 或 `input.spec.*`）
| key | 必填 | 类型 | 说明 / 证据 |
|---|---|---|---|
| `source_image_url` **或** `source_image_asset_id` | ✅(二选一) | string / number | 源图；`param_validate.go:123` 两者全空报「缺少必选参数」。**asset_id 优先**（资产体系） |
| `user_prompt` | ✅ | string | 修改指令/附加提示；`param_validate.go:128` 必填 |
| `mode` | ✅(常量) | string | **必须 = `"image_to_image"`**；`param_validate.go:135` 严格相等校验。需 G1 固定入参 |
| `negative_prompt` | — | string | 反向提示词 |
| `style` | — | string | 风格倾向 |
| `model` | — | string | 白名单：wan2.7-image-pro/-image、wan2.5-i2i-preview、doubao-seedream-4.0/4.5/5.0-lite |
| `api_provider` | — | string | 仅 aliyun / volcengine |
| `size` / `aspect_ratio` / `quality` / `seed` | — | — | 同 text_to_image，归一化+校验 |
| `enable_prompt_optimize` | — | bool | 是否优化提示词 |
| `callback_token` | 系统注入 | string | = task_id |

### 1.2 输出（`OutputDefinition`）
```text
result_type      = "image"
primary_file_url = 结果图 URL
extras.image_url / image_asset_id        = 结果图
extras.source_image_url / source_image_asset_id = 源图（用于展示「输入→输出」关系）
extras.reference_image, optimized_prompt, model, provider, ...
```
> **关键**：源图信息在 `extras`，而 `TaskResultReader` 当前不读 extras（G12）→ result_card 无法直接展示输入图。

### 1.3 计费
`billing_seed.go:294` `image_to_image: 10`（base_points）。同 §[01](01-text-to-image-skill.md) 的乘子与 Quote/冻结现状（G8）。

## 2. Asset Slot 契约（核心 · 公共能力需求）

这是本 Skill 的中心。需求文档第八节要求「**首先定义通用 Asset Slot，不能只为 image_to_image 临时塞字段**」。

### 2.1 Slot 定义（Manifest 视角）
```yaml
- key: source_image
  title: 要修改的图片
  type: asset            # 已在 manifest.go Slot.Type 注释枚举内：asset | asset[]
  required: true
  accept: [image]        # 已有字段 manifest.go:98
  maps_to: input.source_image_asset_id   # asset_id 引用（[v2/10 §5.3] 不直传 URL）
  ask: "把要修改的图片发给我"
```
> `asset` 类型、`accept`、`maps_to` 这些 **Manifest 字段已存在**（`skill/manifest.go:88-101`）。缺的是**填充与落地链路**（下文 2.2–2.5）。
> ✅ **实库佐证**（[04 §11.2](04-skill-contract-compiler.md)）：i2i active 版本(v10) 的 UI schema **确有** `source_image_asset_id`（type=image, required, visible），且**只提供 asset_id、无 url 字段**——与本 slot `maps_to: input.source_image_asset_id` 完全一致；Validator 同时接受 `source_image_url|source_image_asset_id` 二选一。model 默认 `doubao-seedream-4.0`（白名单内）。

### 2.2 Asset 值的最小契约
一个 asset 槽位的值在 AgentState/Plan 中至少携带：
```text
asset_id      （权威标识，落 input.source_image_asset_id）
asset_type    （= image；校验 accept）
url/preview_url（展示用，非权威）
source        （来源：upload | previous_result | work | message）
ownership     （归属用户，必须 == 会话用户，越权拒绝）
availability  （是否仍可用：未删除/未过期/有权限）
```

### 2.3 用户上传图片如何进入 ConversationContext（🔴 G5）
**现状**：入站 DTO 只有 `text`（`agent/handler/dto.go:24-37`）；`ConversationContext.Input` 是 `*domain.Message`（`conversation_service.go:38`），`ContentJSON` 不被入站填充。**用户无法把图片带进会话。**
**需要的公共改动**（主架构）：
1. 消息 DTO 增 `assets: [{asset_id, asset_type}]`（沿用客户端既有 OSS 上传 → asset_id 流程，见 `docs/client-oss-asset-integration.md` / `oss-storage-asset-management-prd.md`）。
2. `ConversationService` 把 assets 写入 `Message.ContentJSON`（持久化、可回放）。
3. `ConversationContext` 暴露当前消息附带的 assets。
4. **校验**：asset ownership == 会话 userID（拒绝越权引用他人资产）。

### 2.4 source asset 如何保存到 AgentState（🔴 G6）
`AgentState.CollectedSlots`（`domain/types.go:172`）能存任意值，但当前无人写 asset。
**需要**：
- Slot 抽取（G4）把当前消息 assets 绑定到 `source_image` 槽位（按 `accept` 过滤 image）。
- AgentState 增「资产上下文」用于指代解析：`last_result_asset_id`（上一轮 result_card 的 `image_asset_id`）、`recent_uploaded_assets`（最近上传）。

### 2.5 launcher 注册输入资产引用（🔴 G7）
普通工具路径 `internal/service/tool.go:522` 调 `RegisterTaskInputAssetRefs(userID, taskID, input)`；Agent 的 `engineTaskLauncher.CreateTask`（`agent_outbox_launcher.go`）**没有**。
**需要**：launcher 在 `tasks.Create` 后，对 asset 类 input（`source_image_asset_id`）调用 `RegisterTaskInputAssetRefs`，保证所有权/生命周期/签名与工具路径一致。

## 3. 指代解析：「这张 / 上一张 / 刚生成的图」（🔴 G6）

需求文档第八节明确要求处理这些表达，且「**第一版若无法可靠解析，应明确限制并追问，不允许静默选错图片**」。

| 用户表达 | 解析目标 | 数据来源 |
|---|---|---|
| （随消息上传图片） | 当前消息 assets[0] | ConversationContext.Input.assets（G5） |
| 「把**这张**改成动漫风」 | 当前消息 asset；无则最近上传 | Input.assets ?? AgentState.recent_uploaded_assets |
| 「改**刚生成的**那张」/「在上一张基础上」 | 上一轮 result 的 `image_asset_id` | AgentState.last_result_asset_id（来自上一个 result_card） |
| 「用我作品里的某张」 | /works 某 task 的结果 asset | 需带 asset_id 引用（work source） |

**第一版可接受的最小策略（明确降级）**：
- 只可靠支持两种来源：**(a) 当前消息上传**、**(b) previous_result（刚生成的图）**。
- 指代不明 / 多候选 / 无候选 → **追问**「你想修改哪张？把图发我，或说『刚生成的那张』」。**绝不静默选图。**
- 图片失效/无权限/已删除（availability=false 或 ownership 不符）→ 明确告知并请重新提供，不进入执行。

## 4. Manifest 草案（`agent/skill/manifests/image_to_image.yaml`）

```yaml
schema_version: 1
key: image_to_image
intent: [image_to_image, image_edit]
title: AI 图片编辑
summary: 在已有图片基础上修改/重绘
description: >
  适合：用户已有一张图片，想在其基础上修改——换风格、改背景、调整局部、改色调等。
  不适合：从零文字生成图片（用 text_to_image）、让图片动起来或做视频。
result_type: image
status: active

source:
  type: workflow
  workflow_name: image_to_image
  route_key: image_generation      # ✅ 实库确认（dream_log: tool_def id=2）
  mode_key: image_to_image         # ✅ 实库确认 tool_mode_id=2046825768654225408；contract 见 [04 §11.2]

cost:
  needs_plan_confirmation: false   # 轻量；首版联调可先 true
  estimate: quote
  hint: "约 10 积分"

fixed_input:                       # [G1]
  mode: image_to_image

slots:
  - key: source_image
    title: 要修改的图片
    type: asset
    required: true
    accept: [image]
    maps_to: input.source_image_asset_id
    ask: "把要修改的图片发给我（也可以说『刚生成的那张』）"

  - key: prompt
    title: 修改要求
    type: text
    required: true
    maps_to: input.user_prompt
    ask: "想怎么改？比如『改成动漫风』『背景换成白色』『再亮一点』"
    extract_hint: "用户对修改方式的描述"

  - key: aspect_ratio
    title: 画幅
    type: enum
    required: false
    maps_to: input.aspect_ratio    # 不给 default：默认跟随源图比例由 workflow 归一化
    options:
      - { value: "1:1",  label: 方形 }
      - { value: "9:16", label: 竖版 }
      - { value: "16:9", label: 横版 }

plan_stages: ["理解修改需求", "重绘图片", "保存作品"]
gates: []
activity_steps:
  - { id: analyze,  label: 理解修改需求, match: "event:task_started" }
  - { id: generate, label: 重绘图片,    match: "node:provider_router" }
  - { id: save,     label: 保存作品,    match: "node:upload_result" }
stages:
  - { match: "event:task_started",   text: "正在理解修改需求" }
  - { match: "node:provider_router", text: "正在重绘图片" }
  - { match: "node:upload_result",   text: "正在保存作品" }
examples:
  - "把这张改成动漫风"
  - "背景换成白色"
  - "保持人物不变，再亮一点"
  - "把刚生成的那张改成黑白"
```
> `activity_steps`/`stages` 的 match 节点名需真机校准（同 [01 §2 注](01-text-to-image-skill.md)）。

## 5. 两个必填槽位的协同追问

`image_to_image` 有**两个**必填：`source_image`（asset）+ `prompt`（修改要求）。缺槽追问顺序与话术：

| 用户输入 | 缺失 | 追问 |
|---|---|---|
| 「把这张改成动漫风」（带图） | 无 | 直接出 PlanCard |
| 「把这张改成动漫风」（不带图、无历史可解析） | source_image | 「把要修改的图片发我，或说『刚生成的那张』」 |
| （上传一张图，无文字） | prompt | 「想怎么改这张图？」 |
| 「改一下」（无图无指令） | 两者 | 先要图，再要修改要求（可同 group 一并问，[v2/10 §5.1 group](../v2/10-skill-manifest-spec.md)） |

> **现状（G4）**：`RuleSlotExtractor` 不抽 asset、不抽通用 `prompt`。需主架构把 Slot 抽取通用化（按 slot.type：asset 取消息 assets / 指代解析；text 取自由文本）。

## 6. PlanCard：展示输入图片（验证 G2）

需求文档 4.2 / 6.3 要求「PlanCard 展示输入图片」。
```text
要修改的图片：[缩略图]   ← 来自 source_image 槽位的 preview_url
修改要求：<prompt>
画幅：<aspect_ratio 或「跟随原图」>
预计消耗：约 10 积分
阶段：理解修改需求 → 重绘图片 → 保存作品
[开始修改] [调整一下]
```
**现状（G2）**：通用 PlanCard 需支持 **asset 类 slot 的缩略图展示**（plan_card content 携带 `slots.source_image = {asset_id, preview_url}`）。

## 7. ResultCard：输入↔输出关系（验证 G12）

需求文档 4.2 要求「ResultCard 展示输入与输出关系」。
- **现状**：result_card 只有 `primary_file_url`（结果图），无源图（G12：TaskResultReader 不读 extras）。
- **两条可选路径**：
  - (a) 扩展 `TaskResult` 携带 `source_image_url/asset_id`（从 output extras 读），result_card 直接展示「前/后」对比。
  - (b) result_card 只给 task_id，客户端用 `/works/:id/creative-detail`（`build_image_to_image_creative_detail`，`tool/images/`）拉取含源图的详情展示。
- 建议第一版走 (b)（不改公共 Result 结构），(a) 作为体验增强。

## 8. 连续修改（上一轮结果作为下一轮输入）

这是 image_to_image 的杀手场景，也是对 G6 的最强验证：
```text
1 用户：上传图A，「改成动漫风」 → 结果图B（result_card，记 last_result_asset_id=B）
2 用户：「背景换成白色」          → source_image 解析为 B（previous_result）→ 结果图C
3 用户：「再亮一点」              → source_image 解析为 C → 结果图D
```
- 每轮都是 image_to_image 的新任务（whole-fork，`forked_from=上一 task`），但 **source 槽位换成上一轮结果**——这与 text_to_image 的纯 fork 不同：**fork 的同时要改 input.source_image_asset_id 为上一结果**。
- **关键依赖**：iterate 时 `mergeSlots`（`runtime.go:128`）需把 `source_image` 更新为 `last_result_asset_id`（而非沿用上一轮的源图）。这是 image_to_image 专属的 merge 语义，必须在**通用** merge 框架里通过「Manifest 声明」表达，**不得在 runtime 写 image_to_image 特判**（否则又制造一个像 short_drama 那样的硬编码）。
  - 建议：Manifest 声明「iterate 时 source 来自 previous_result」的语义（如 `iteration.source_from: previous_result`），由通用 runtime 读取。属 G6/G13 范畴。

## 9. 异常处理

| 情况 | 处理 |
|---|---|
| 缺源图且无法解析指代 | 追问要图（§3 降级策略），不执行 |
| 源图 ownership 不符（他人资产） | 拒绝并提示重新提供 |
| 源图已删除 / 过期 / 无签名权限 | 告知失效，请重新上传 |
| provider/model 非白名单 | 由 workflow param_validate 拦截 → error_card（应在 PlanCard 前用默认值规避） |
| 内容审核拦截 | param_validate 失败 → 一个 error_card，不产成功作品，计费退款（G8） |

## 10. 完整状态流

```text
1 POST /messages {text:"把这张改成动漫风", assets:[{asset_id:A}]}   # 需 G5 的 assets 入口
  ↳ Decide: intent=image_to_image; source_image=A(upload); prompt="改成动漫风"; 必填齐
2 → confirming，plan_card（展示缩略图A + 修改要求）          # 需 G2 asset 展示
3 confirm → executing；Outbox(create_task, workflow=image_to_image,
  input{source_image_asset_id=A, user_prompt, mode=image_to_image[G1]})
  → launcher 注册 A 为输入资产引用                          # 需 G7
4 Activity：理解修改需求 → 重绘图片 → 保存作品
5 completed → result_card(结果图B)；记 last_result_asset_id=B；进 /works
6 用户「背景换白色」→ iterate：source_image=B(previous_result)  # 需 G6
  → plan_card → confirm → image_to_image(source=B) → 结果图C
```

## 11. 测试清单

**Asset Slot / 指代**
- [ ] 带图说「改成动漫风」→ 用该图，不选错
- [ ] 不带图说「改成动漫风」（无历史）→ 追问要图，**不静默选图**
- [ ] 上传图无文字 → 追问「想怎么改」
- [ ] 「改刚生成的那张」→ 解析为上一轮 result 的 asset
- [ ] 引用他人/已删除图片 → 拒绝并提示

**意图边界**
- [ ] 「把这张改成动漫风」→ image_to_image，不命中 text_to_image
- [ ] 「生成一个动漫风女孩」（无「这张/已有图」）→ text_to_image，不命中 image_to_image
- [ ] 「让这张照片动起来」→ 提示对应 Skill **尚未接入**，不误命中 image_to_image

**闭环**
- [ ] PlanCard 展示输入图缩略图 + 修改要求
- [ ] Activity 三步正确；只产一个 result_card
- [ ] 结果图进 /works；creative-detail 能看到源图↔结果关系
- [ ] 失败链路一个 error_card + 退款，不产成功作品

**连续修改**
- [ ] B→C→D 链：每轮 source 正确切到上一轮结果
- [ ] 「再亮一点」在 C 基础上得 D，不回退到原图 A

**资产引用 / 计费 / 恢复**
- [ ] 输入资产引用已登记（G7），签名 URL 可访问
- [ ] 计费与工具路径一致（G8）
- [ ] 断线重连 / 退出重进后输入图、结果图均可恢复

---

返回：[00 总体方案](00-skill-integration-and-stability.md) · 下一篇：[03 稳定性与回归矩阵](03-agent-stability-regression.md)
