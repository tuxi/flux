# V2.1 · 01 · text_to_image Skill 接入规格（Level 1）

> 目标：用最便宜（8 积分）、最快、最简单的 Skill 验证 Agent 基础闭环。
> 字段全部与真实 workflow `ai-engine/workflows/images/text_to_image_workflow_dsl.go` 对账，不杜撰。
> 上位：[00 总体方案](00-skill-integration-and-stability.md)（含架构缺口编号 G1–G13）。

---

## 1. 真实 Workflow 输入输出（对账）

**Workflow**：`Name: "text_to_image"`，注册于 `server.go:530`。**Desc**：文本生成图片的主工作流。
执行特征：内容审核（可关）→ 参数校验 → 供应商路由 → prompt 增强 → 缓存查询/命中 → 各家 submit/wait（aliyun/volcengine 为 `await` 异步，openai/kling 为 wait 工具）→ 下载/后处理/上传/存缓存。**无 await 用户闸门**（与 short_drama 的两道审核不同）。

### 1.1 输入（`input.*`）
| input key | 必填 | 类型 | 说明 / 证据 |
|---|---|---|---|
| `user_prompt` | ✅ | string | 主描述；`text_to_image_param_validate.go:122` `requiredFields=["user_prompt", mode]` |
| `mode` | ✅(常量) | string | **必须 = `"text_to_image"`**；`param_validate.go:131`。非用户槽位，需 G1 固定入参注入 |
| `negative_prompt` | — | string | 反向提示词 |
| `style` | — | string | 风格倾向（自由串） |
| `model` | — | string | 模型/别名；非法则 `param_validate.go:140` 报错（有白名单） |
| `api_provider` | — | string | 显式厂商（aliyun/kling/openai/volcengine） |
| `size` | — | string | 目标尺寸 `1024x1024` 等 |
| `aspect_ratio` | — | string | `1:1 / 9:16 / 16:9`（归一化+校验） |
| `quality` | — | string | `standard / hd` |
| `seed` | — | number | 固定种子 |
| `n` | — | int | 生成数量（provider_router 读 `input.n`）。**注意输出契约见 §1.2** |
| `enable_prompt_optimize` | — | bool | 默认 true；false 跳过 prompt 增强 |
| `content_audit_enabled` | — | bool | true 先过审核（start 边条件） |
| `background` / `transparent_background` | — | — | 透明背景等高级项 |
| `feature_flags` / `enable_sequential` | — | — | 高级能力位 |
| `callback_token` | 系统注入 | string | launcher 注入 = task_id（`agent_outbox_launcher.go:42`） |

### 1.2 输出（`OutputDefinition`）
```text
result_type      = "image"
primary_file_url = nodes.cache_hit_return.output.image_url ?? nodes.upload_result.output.url
preview_url      = 同上
width / height   = postprocess 结果
extras.image_url        = 最终图 URL
extras.image_asset_id   = 最终图 asset_id（task_result / image / private）
```
> ⚠️ **单图契约**：尽管有 `input.n`，OutputDefinition 的 `primary_file_url` 是**单个** URL。第一版 Skill **建议固定 count=1**（多图需 G12 + 输出契约确认，作为后续）。

### 1.3 计费（真实）
`billing_seed.go:293` `text_to_image: 8`（base_points，resource=image），乘子：quality（hd×1.25）、model_tier（premium×1.15）、image_size_tier。
报价由 `BuildQuoteReq("tool", mode_key, input)` 解析（`internal/service/tool.go:518`）。
> 现状：Agent 启动路径**未接 Quote/冻结**（G8）；第一版 PlanCard 估价需接入真实 Quote，否则显示占位值。

## 2. Manifest 草案（`agent/skill/manifests/text_to_image.yaml`）

> 标注 `# [G1]` 的字段**依赖**主架构补「固定输入」能力后才生效。

```yaml
schema_version: 1
key: text_to_image
intent: [text_to_image, image_generation]
title: AI 图片生成                 # 同时作 task 的 entry_title/subtitle（与线上图片工具一致）
summary: 根据文字描述生成图片
description: >
  适合：从一句文字描述生成图片/插画/海报/封面/素材。
  不适合：基于已有图片修改（用 image_to_image）、让图片动起来或做视频（用短剧/视频类 Skill）。
result_type: image
status: active

source:
  type: workflow
  workflow_name: text_to_image
  # 作品归类：与线上图片生成工具一致，使 Agent 任务进入 GET /works
  route_key: image_generation       # ✅ 已实库确认（dream_log: tool_def id=2, route_key=image_generation）
  mode_key: text_to_image           # ✅ 实库确认 tool_mode_id=2001；权威 contract 见 [04 §11.1]

cost:
  needs_plan_confirmation: false    # 轻量幂等→可免确认直跑（[v2/10 §4]）；首版可先 true 便于联调
  estimate: quote                   # 接 BuildQuoteReq；未接前用 hint 兜底
  hint: "约 8 积分"

# [G1] 固定输入（非 slot 常量）。当前 Manifest 结构体无此段，需主架构补。
# 过渡方案：用一个带 default、不展示的隐藏 slot 映射 input.mode。
fixed_input:
  mode: text_to_image

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

  # count 暂不开放为 slot（单图契约，§1.2）；如开放需 maps_to: input.n + G12
  # - { key: count, type: int, required: false, default: 1, min: 1, max: 4, maps_to: input.n }

plan_stages: ["理解图片需求", "生成图片", "保存作品"]

# 无 await 用户闸门
gates: []

# 执行期活动流骨架（doc 16）。text_to_image 无 await，纯进度。
activity_steps:
  - { id: analyze,  label: 理解图片需求, match: "event:task_started" }
  - { id: generate, label: 生成图片,    match: "node:provider_router" }
  - { id: save,     label: 保存作品,    match: "node:upload_result" }

stages:
  - { match: "event:task_started",     text: "正在理解你的图片需求" }
  - { match: "node:provider_router",   text: "正在生成图片" }
  - { match: "node:upload_result",     text: "正在保存作品" }

examples:
  - "画一只赛博朋克风格的猫"
  - "生成一张小红书封面，主题是露营，竖版"
  - "帮我画一个动漫风格的女孩"
  - "来张极简风的咖啡店海报"
```

> ⚠️ **activity_steps / stages 的 match 需用真机事件校准**（[v2/17 Phase C](../v2/17-implementation-status-and-roadmap.md)：activity step matcher 用真实事件校准）。`node:provider_router` 是否如期触发、是否有更合适的「生成中」节点（如各家 submit/wait），需联调确认。

## 3. Intent 与边界负例（验证 G3）

```text
正例（→ text_to_image）：
  "画一只猫"                        "生成一张露营封面"
  "做张极简海报"                    "来个动漫风女孩"
负例（不应命中 text_to_image）：
  "把这张图改成动漫风"   → image_to_image（有「这张/已有图」语义）
  "做个都市爱情短剧"     → short_drama
  "让这张照片动起来"     → 图片转视频类 Skill（V2.1 未接 → 应提示「尚未接入」，不可误命中）
```

**现状结论（G3）**：`rule_intent.go` 无任何 text_to_image 规则，上述正例**全部落空**。需主架构把 Intent 关键词/examples 改为 Manifest 驱动（例如从 `examples` + 动词「画/生成图/做张图」构造规则），或决策开启 Hybrid LLM。**接入负责人只提供 examples 与负例，不改 runtime、不自开 LLM。**

## 4. Slot 收集、默认值与追问

| slot | 必填 | 默认 | 缺失行为 |
|---|---|---|---|
| `prompt` | ✅ | — | **追问**：「想画什么？描述越具体越好」 |
| `aspect_ratio` | — | `1:1` | 不追问（记入 defaults_applied，方案卡告知可改） |
| `style` | — | 无 | 抽不到不追问，交给模型/prompt 增强 |

- 唯一硬门槛是 `prompt`。一句「帮我画张图」→ 缺 prompt → clarify（少打扰原则，[v2/10 §5.2](../v2/10-skill-manifest-spec.md)）。
- **现状（G4）**：`RuleSlotExtractor` 不抽 `prompt`（它抽的是短剧 `user_prompt`），也不抽通用 `aspect_ratio` 之外的图片语义。需通用化或补图片规则。
- **画幅口语映射**（横版/竖版/方形）：`parseAspectRatio`（`rule_slots.go:93`）已能识别，可复用，但产出键是 `aspect_ratio`，与本 slot 一致 ✅。

## 5. PlanCard 内容（验证 G2）

第一版方案卡尽量简单：
```text
图片描述：<prompt>
画幅：<aspect_ratio>（默认方形，可改）
风格：<style 或「自动」>
预计消耗：约 8 积分        # 接 Quote 后为精确值
生成阶段：理解需求 → 生成图片 → 保存作品
[开始生成] [调整一下]
```
`editable: [aspect_ratio, style]`。
**现状（G2）**：`runtime.buildPlanCard` 写死短剧 derived（keyframe/segment/shot），对 text_to_image 会产生脏 derived。需改为 Manifest 驱动：展示已填 slots（按 title）+ plan_stages + estimated_cost。

> `needs_plan_confirmation`：[v2/10 §4](../v2/10-skill-manifest-spec.md) 允许轻量幂等免确认直跑。**建议联调期先 `true`**（看 PlanCard 链路），稳定后改 `false` 降摩擦。

## 6. Activity steps（执行期）

```text
理解图片需求  → 生成图片  → 保存作品
```
确认后由 `ConfirmPlan` 立即铺成 pending（`runtime.go:188` `activity.New`），随事件激活；终态 finalize 收尾（`observer.go:351`）。
**现状（G10）**：headline 写死「正在创作短剧」，图片 Skill 需 Manifest 驱动 headline（如「正在生成图片」）。

## 7. ResultCard 字段

Observer.handleSuccess（`observer.go:284`）+ TaskResultReader（`agent_observer_reader.go:17`）产出：
```json
{
  "kind": "result_card",
  "task_id": "<id>",
  "result_type": "image",
  "primary_file_url": "<image_url>",
  "cover_url": "",
  "actions": [{"label": "再来一张"}, {"label": "换个风格/横版"}]
}
```
- 单图直接展示 ✅。
- actions 文案当前写死「再来一版/改一下」（`observer.go:293`），图片语义建议「再来一张 / 换风格 / 改横版」——属文案优化（可 Manifest 驱动或客户端按 result_type 渲染）。
- 打开作品：result_card 的 task_id → `/works/info/:id` / `/works/:id/creative-detail`（`build_text_to_image_creative_detail`）。

## 8. /works 元数据

launcher 写入（`agent_outbox_launcher.go:55-69`，值来自 Manifest，见 `runtime.ConfirmPlan` `runtime.go:200`）：
```text
entry_type    = "tool"            # entryTypeFor：有 route/mode → tool（runtime.go:219）
entry_title   = manifest.title    = "AI 图片生成"
entry_subtitle= manifest.title
route_key     = manifest.source.route_key   = image_generation（✅ 实库确认）
mode_key      = manifest.source.mode_key    = text_to_image（✅ 实库确认）
```
→ 进 `ListByUserV2`（`internal/service/user.go:219`），与普通图片工具任务同列表归类。
> route_key/mode_key 已在 `dream_log` 实库确认（tool_def id=2 / tool_mode 2001 / active version 15）；完整契约对账见 [04 §11.1](04-skill-contract-compiler.md)。

## 9. iterate / fork 行为

- 「再来一张」「换个风格」「改成横版」均在 completed 后触发 `iterate`（`runtime.go:79`）→ `mergeSlots`（结构化槽位覆盖：aspect_ratio/style；纯「再来一张」保留原 prompt）→ 新 Plan → confirm → **whole-fork**（`conversation_service.go:374` 自动 `forked_from=上一 task`）。
- 引擎层是**全新任务**（launcher 不做引擎级 patch，`agent_outbox_launcher.go` 忽略 `forked_from`，仅 TaskLink 记 relation=fork）——对图片即「重新生成一张」，符合 [v2/17](../v2/17-implementation-status-and-roadmap.md) 「Fork=完整重生成」。
- **现状（G4）**：`mergeSlots`/`regeneratePhrases` 已通用（不绑短剧），但 `parseStyle` 只认短剧题材；图片风格抽取需补。

## 10. 完整状态流

```text
1  POST /conversations {first:"画一只赛博朋克的猫，竖版"}
   ↳ Decide: intent=text_to_image; slots{prompt="赛博朋克的猫", aspect_ratio=9:16}; prompt 齐
2  needs_confirm? 
   - true:  → confirming，推 plan_card（§5）
   - false: → 直接 launch（免确认）
3  (confirm) POST /signals{confirm_plan} → executing；Outbox.Enqueue(create_task, workflow=text_to_image,
   input{user_prompt, aspect_ratio, mode=text_to_image[G1]}, entry/route/mode)
4  Outbox Worker → engineTaskLauncher.CreateTask → task + Enqueue；TaskLink(primary)
5  Activity：理解需求 → 生成图片 → 保存作品（原地更新，WS 推）
6  引擎 completed → result_card(单图)；stage→completed；进 /works
7  "再来一张/换横版" → iterate → plan_card → confirm → whole-fork → 新 result_card
```
（对比 short_drama 的 17 步含两道 review_card——图片**无 reviewing 阶段**，链路更短。）

## 11. 测试清单

**意图/槽位**
- [ ] 「画一只猫」→ text_to_image，不误命中 short_drama / image_to_image
- [ ] 「帮我画张图」（缺 prompt）→ clarify 追问描述
- [ ] 「画只猫，横版」→ aspect_ratio=16:9
- [ ] 「做个都市爱情短剧」→ short_drama（不被图片 Skill 抢）

**闭环**
- [ ] PlanCard 字段正确（无短剧 grid 脏 derived）
- [ ] 确认后 Activity 三步正确步进、同 id 原地更新
- [ ] 只产**一个** result_card（无子任务升格——text_to_image 含 content_audit_map 子任务，须验证不刷屏）
- [ ] 图片进 /works，可打开作品详情 / creative-detail
- [ ] 失败（如审核拦截/参数非法 param_validate.is_valid=false）→ **一个** error_card，不产成功作品

**iterate/fork**
- [ ] 「再来一张」保留 prompt 重生成；「换横版」改 aspect_ratio
- [ ] fork 任务在 /works 与会话双入口可见

**计费（G8）**
- [ ] 估价与实际扣费一致；失败退款语义正确

**恢复**
- [ ] 退出重进 / 切后台 / WS 断线重连后，Activity 与 result_card 可由 GET 兜底恢复

---

下一篇：[02 image_to_image 接入规格](02-image-to-image-skill.md)
