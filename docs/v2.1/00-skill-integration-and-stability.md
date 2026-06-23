# V2.1 · Skill 接入与 Agent 稳定性验证 · 总体方案

> 本轮**文档先行，不写业务代码**（呼应需求文档「在文档审核通过前，不开始正式编码」）。
> 本文是 V2.1 的入口与索引，并在 §9 直接回答需求文档第十四节要求的 10 个问题。
>
> 审计基准：`feat/agent-first-v2` 分支真实代码（已 fast-forward 到本工作分支）。审计时间：2026-06。
> 上位依据：[v2/17 实现现状审计](../v2/17-implementation-status-and-roadmap.md)（single source of truth）、[v2/10 Manifest 规范](../v2/10-skill-manifest-spec.md)、[v2/13 short_drama 闭环](../v2/13-short-drama-skill-walkthrough.md)、[v2/15 决策引擎](../v2/15-agent-decision-engine.md)、[v2/16 Activity 流](../v2/16-activity-stream.md)。

---

## 1. V2.1 目标

用**两个简单、低成本、快执行**的图片 Skill，验证 Agent First 公共架构是否**真正通用**，并建立「由简到繁」的稳定性测试梯度。

```text
Level 1  text_to_image   验证基础 Agent 闭环（最便宜、最快、最高频回归）
Level 2  image_to_image  验证资产输入 / 上下文引用 / 连续修改
Level 3  short_drama     验证复杂 Workflow / 双 Review Gate / 子任务 / 长执行（低频回归）
```

V2.1 不是「加图片功能」，而是：**用简单 Skill 把 short_drama 时期隐性固化的「短剧特判」逼出来，证明（或证伪）Manifest 是接入 Agent 的唯一入口。**

## 2. 为什么先接简单 Skill

[v2/17 §5.4 / §6 Phase C](../v2/17-implementation-status-and-roadmap.md) 已点明：第二个 Skill 是「通用性的真正考验」，且应选**结构明显不同、流程相对简单**的 Skill（**不要**直接上最复杂的 goods_video）。

- text_to_image / image_to_image 与 short_drama 结构差异大：**无 await 闸门、无 Map/Loop 大规模子任务、成本低（8 / 10 积分 vs ~300）、执行快**。差异越大，越能暴露公共层对 short_drama 的隐性耦合。
- 成本低 ⇒ 可作为**高频回归**：每次改动公共能力，先跑 Level 1/2，再按需跑昂贵的 Level 3。
- 闭环短 ⇒ 调试快、失败点少、问题易定位。

## 3. 开发职责边界

完全遵循需求文档第三节。下面把边界**落到本仓库的真实文件**上。

### 3.1 主架构负责人（公共能力，需评审）
`ai-engine/agent/**` 下的公共层 + 与之耦合的 server 装配：

| 公共能力 | 真实位置 |
|---|---|
| Conversation / Message / AgentState / Plan / TaskLink / Outbox | `agent/domain/types.go`、`agent/repository/**` |
| ConversationContext + canonical turn | `agent/service/conversation_service.go` |
| AgentRuntime（Intent/Slot/Plan/Confirm 决策） | `agent/runtime/runtime.go`、`runtime/rule_intent.go`、`runtime/rule_slots.go` |
| Skill Registry / Manifest 规范与校验 | `agent/skill/{manifest,loader,registry}.go` |
| Outbox Worker / TaskLauncher | `agent/worker/outbox_worker.go`、`server/agent_outbox_launcher.go` |
| Observer → Activity / Review / Result | `agent/observer/observer.go`、`agent/activity/**` |
| Conversation WS + 客户端消息协议 | `server/agent_ws.go`、`agent/handler/dto.go` |

**§7 列出的「架构缺口」全部属于主架构负责人范畴**——它们是把 short_drama 特判改成通用能力的工作，Skill 接入负责人**不得自行绕过或临时特判**。

### 3.2 新 Skill 接入负责人（本轮产出）
- 编写两份 Manifest：`agent/skill/manifests/text_to_image.yaml`、`image_to_image.yaml`（与真实 workflow 字段对账，§见 01/02）。
- 提供 intent 边界负例与 examples（few-shot）。
- 写接入文档（本目录 01/02/03）与测试清单。
- **不允许**：重构 AgentRuntime 主流程、绕过 Skill Registry、绕过 PlanCard/confirm_plan、从会话代码直调 Workflow、为单 Skill 在 Observer 写硬编码、改 Workflow Engine 核心、复制一套新的 Conversation/Task/WS、破坏 short_drama 链路。

## 4. 接入顺序（与需求文档第十一节一致）

```text
Stage 1（本轮）   调查 + 文档 + 架构缺口清单                → 主架构负责人评审
V2.1-0A          契约与真实 DB 审计                        → [04](04-skill-contract-compiler.md)（已完成实拉对账）
V2.1-0B          最小公共 Resolver / Binding / Version Pin → 主架构（落地 G14/G16/G18/G20）
Stage 2 (t2i)    text_to_image 最小闭环                    → 建 Level 1 高频回归
Stage 3 (i2i)    image_to_image + Asset Slot               → 建 Level 2 回归
Stage 4          稳定性回归 short_drama                    → 确认未被破坏
```

接入顺序的**前置门槛**：
1. **V2.1-0A**（[04](04-skill-contract-compiler.md)）：以 active ToolModeVersion + Workflow Validator 为事实来源做契约审计（**本轮已用真实库 `dream_log` 实拉对账**，非模板）。
2. **V2.1-0B**：主架构落地**最小公共 Contract Resolver / Auditor + Binding（fixed_input/system_injected）+ Version Pin**（缺口 G14/G16/G18/G20）。其中「证明 Validator Execute() 命令式约束」**不在 0B 范围**——它需要 G21 的 InputContractProvider，属未来 Strong Contract Compiler。
3. 之后才进 Stage 2/3，并需主架构先消化 §7 中标 🔴 的公共缺口。

> Skill 接入负责人：调查、Manifest、Skill 专属测试。
> 主架构负责人：公共 Contract Resolver / Binding / Version Pin / Asset Slot / InputContractProvider。缺口编号见 §7（G1–G13）+ [04 §15](04-skill-contract-compiler.md)（G14–G21）。

## 5. 对 Agent 公共架构的验证点

接入后必须确认以下能力**不依赖 short_drama 特判**（详见 [03 回归矩阵](03-agent-stability-regression.md)）：

- 通用性：Manifest 加载/注册、Intent 选择、Slot 抽取、Clarify、PlanCard、confirm_plan、Outbox、Task 创建、TaskLink、Observer、Activity、Result/Error、Works、Fork/iterate、Conversation WS。
- 正确性：一个主 Task 只有一个最终 Result/Error；子任务不升格为独立结果；Activity 同 id 原地更新；Agent 启动任务进入 /works；失败链路不产生成功作品。
- 恢复性：退出重进、切后台、WS 断线重连、完成时不在线、漏帧后 snapshot 恢复。

## 6. 非目标（V2.1 不做）

RAG、Multi-Agent、节点级 patch / 单镜重跑、复杂 Blueprint、goods_video、视频生成新 Skill、动态 CMS Manifest、运行时远程 Skill 下载、全面开启 LLM Intent/Slot、大规模重构 Workflow Engine。

> 注意：本轮发现的若干「缺口」（如 Quote 计费、Asset 上传通道）**不是** V2.1 的扩展功能，而是**让两个简单 Skill 能正确闭环的最小公共能力**。它们属于 Phase C 的「Manifest 工程化」范畴，需主架构负责人评估优先级。

## 7. 架构缺口清单（提交主架构负责人评估）

> 图例：🔴 阻塞接入（不补则无法在不特判的前提下跑通）· 🟡 影响体验/正确性（可先降级但需明确） · 🟢 优化项。
> 每条都给出真实代码证据。

### 🔴 G1 · Manifest 无「固定输入 / 常量入参」机制
两个图片 workflow 都**强制要求** `input.mode`，且严格校验：
- `text_to_image_param_validate.go:131` `if mode != ImageGenModeTextToImage` → 报错；`requiredFields = ["user_prompt", mode]`。
- `image_to_image_param_validate.go:135` `if mode != ImageGenModeImageToImage` → 报错。

而 `runtime.buildWorkflowInput`（`runtime.go:232`）**只**把 slot 按 `maps_to` 写进 input，没有「非 slot 的常量入参」通道。short_drama 不暴露 `mode/model/fps/bgm_url`（[v2/13 §2](../v2/13-short-drama-skill-walkthrough.md) 说明这些由 workflow 系统默认），所以从没需要这个能力。
**影响**：Agent 启动 text_to_image/image_to_image 时 `input.mode` 为空 → 参数校验失败。
**建议**：Manifest 增加 `fixed_input`（或 `const_input`）映射段，`act()` 时合并进 input；或允许「隐藏 slot」（有 default、不入 PlanCard 展示）。

### 🔴 G2 · PlanCard 构建器对 short_drama 硬编码
`runtime.buildPlanCard`（`runtime.go:391-449`）整段是短剧专用：`grid_size→keyframe_count`、`segment_count`、`shot_duration`、`est_duration_sec`、`estimatedCost = segmentCount*40`、`editable:[style,grid_size,shot_duration,aspect_ratio]`。
**影响**：text_to_image 没有 grid_size → derived 全是 0/脏值；图片 Skill 的方案卡语义错误。
**建议**：PlanCard 改为 **Manifest 驱动**——展示「已填 slots（按 title）+ plan_stages + estimated_cost」，derived/editable 由 Manifest 声明，runtime 不再写死短剧字段。

### 🔴 G3 · Intent 规则只认 short_drama
`rule_intent.go:23` `shortDramaKeywords=["短剧","微短剧","短片","drama"]`；`contextSuggestsShortDrama` 也只看「短剧」。没有 text_to_image / image_to_image 的任何规则。
**影响**：「画一张猫」「把这张改成动漫风」不会命中任何 Skill → 走 fallback。
**建议**：让 Intent 规则**从 Manifest 取关键词/examples**（而非写死），或由主架构负责人决策开启 `HybridIntentClassifier`（外壳已在 `runtime/hybrid_intent.go`，默认关）。**接入负责人不得自行开 LLM fallback**（需求文档第九节）。

### 🔴 G4 · Slot 抽取只认 short_drama 槽位
`RuleSlotExtractor.Extract`（`rule_slots.go:20`）只抽 `user_prompt / duration→grid_size+shot_duration / style / aspect_ratio`，且 `parseStyle` 是短剧题材枚举。没有通用 `prompt / count` 抽取，**完全没有 asset 抽取**。
**影响**：text_to_image 的 `prompt`、image_to_image 的 `source_image` 无法被抽取/填充。
**建议**：Slot 抽取改为 Manifest 驱动（按 slot.type 通用化），或为图片 Skill 增加规则；asset 槽位需配合 G8/G9。

### 🔴 G5 · 会话 API 无资产/附件入口（image_to_image 致命）
入站 DTO 只有 `text`：`createConversationReq/firstMessageReq/postMessageReq`（`agent/handler/dto.go:24-37`）。`ConversationContext.Input` 是 `*domain.Message`（`conversation_service.go:38`），虽含 `ContentJSON` 字段，但**从不被入站填充**。
**影响**：用户**无法把图片带进会话** → image_to_image 的 source image 无来源。
**建议**：扩展消息 DTO 携带 `assets:[{asset_id,asset_type,...}]`，service 写入 `Message.ContentJSON`，ConversationContext 暴露，runtime/slot 绑定到 asset 槽位。详见 [02 §Asset Slot 契约](02-image-to-image-skill.md)。

### 🔴 G6 · Asset Slot 解析（「这张/上一张/刚生成的图」）不存在
没有任何代码把指代解析为具体 asset_id；`AgentState.CollectedSlots` 能存 asset_id 但无人填。previous_result（上一轮 result_card 的 `image_asset_id`）无回取机制。
**影响**：连续修改、引用历史图片均无法实现。
**建议**：AgentState 增「资产上下文」（last_result_asset_id、recent_uploaded_assets）；解析优先级 + 不确定时**追问，不静默选错**（需求文档第八节）。

### 🔴 G7 · Agent 启动路径不注册输入资产引用
普通工具路径 `internal/service/tool.go:522` 调 `storageAssetService.RegisterTaskInputAssetRefs`；而 Agent 的 `engineTaskLauncher.CreateTask`（`server/agent_outbox_launcher.go:31`）**没有**这一步。
**影响**：image_to_image 的 `source_image_asset_id` 不会登记为任务输入资产引用（所有权/生命周期/签名/可见性链路缺失）。
**建议**：launcher 在创建 task 后对 asset 类 input 调用 `RegisterTaskInputAssetRefs`（与 G5/G6 配套）。

### 🔴 G8 · Quote / 计费未接入 Agent 启动路径
- PlanCard `estimated_cost` 是占位 `segmentCount*40`（`runtime.go:403`，[v2/17 §2](../v2/17-implementation-status-and-roadmap.md) 已标注）。
- `CreateTaskWithFreeze`（计费冻结）只在 `internal/service/tool.go:519`、`template.go:489` 调用；Agent 的 `engineTaskLauncher.CreateTask` **不冻结、不报价**（只 `tasks.Create + Enqueue`）。结算/退款在引擎 worker 执行期发生（`worker/worker.go:244`、`engine/resume.go:265` `billing_action:refund`）。
**影响**：Agent 启动的任务**计费冻结语义与工具路径不一致**（是否扣费、失败是否退款需端到端核验）；方案卡报价不准确。
**建议**：Agent 启动接入 `BuildQuoteReq(modeKey,…)` 报价（写入 Plan.estimated_cost）+ `CreateTaskWithFreeze` 冻结，保证与工具路径**计费一致**。这是 short_drama 也存在的既有缺口，V2.1 一并暴露。

### 🟡 G9 · Manifest 深度校验缺失（[v2/17 §5.5 / Phase C](../v2/17-implementation-status-and-roadmap.md)）
`manifest.Validate`（`manifest.go:114`）仅查 key / intent / slot.maps_to / required 有 ask|default。**未查**：workflow_name 是否注册存在、route/mode 合法、gate signal 真实、activity step 唯一且 matcher 合法、result contract。
**影响**：Manifest 配错可致错误路由 / 作品不进 /works / 错误扣费 / 审核失效 / Activity 不更新。
**建议**：接第二个 Skill 前补深度校验 + 注册纪律「无 Manifest 的 Workflow 不允许被 Agent 自动调用」。

### 🟡 G10 · Runtime 残留 short_drama 文案/锚点特判
- `pendingAnchor`（`runtime.go:331`）：`if c.State.SkillKey != "short_drama" { return "" }`，并引用 `duration_hint/user_prompt`。
- `contextualFallback`（`runtime.go:316`）：写死「我可以先帮你做一条短剧…」。
- `ConfirmPlan`（`runtime.go:188`）：Activity headline 写死 `"正在创作短剧"`。
- `clarify`（`runtime.go:378`）：`user_prompt + duration_hint` 特判话术。
- `planOrClarify`（`runtime.go:109`）：`next.Goal = collected["user_prompt"]` 假设槽名。
**影响**：图片 Skill 复用这些路径会出现「短剧」串味文案、错误锚点。
**建议**：文案/headline/anchor 改为 Manifest 驱动（如 `title`、`activity_headline`、各 slot 的 `ask`）。

### 🟡 G11 · 多 Skill 选择/消歧未实现
`selectSkill`（`runtime.go:252`）：候选 == 1 才返回，>1 或 0 直接 nil → fallback。[v2/15 §13-B](../v2/15-agent-decision-engine.md) 设计的「命中多个 → 澄清」尚未实现。
**影响**：3 个 Skill 后，模糊句（如「做个视频」「做张图」）可能落空或误选。
**建议**：实现「多候选 → 澄清卡」。

### 🟡 G12 · Result Card 不携带输入图 / 多图
`engineTaskResultReader.ReadResult`（`server/agent_observer_reader.go:17`）只读 `ResultType/PrimaryFileUrl/CoverUrl`，**不读 Extras**。image_to_image 输出的 `source_image_url/asset_id`、text_to_image 的多图（`n`）无法进入 result_card。
**影响**：image_to_image 的 result_card 无法展示「输入→输出」关系（需求文档 4.2 / 验收项）。
**建议**：扩展 TaskResult 携带 Extras 子集（source_image、变体列表）；或 result_card 仅给 task_id，客户端用 `/works/:id/creative-detail` 拉详情展示关系（已有端点，见 §8）。

### 🟢 G13 · Manifest 缺 `iteration` 字段（文档有、代码无）
[v2/10 §8](../v2/10-skill-manifest-spec.md) 定义了 `iteration`，但 `skill.Manifest` 结构体（`manifest.go:17-34`）**没有该字段**——YAML 里写了会被忽略。当前 Fork 走 service 层启发式 `mergeSlots`（`runtime.go:128`）+ confirm 时自动 `forked_from`（`conversation_service.go:374`），= 整体重生成。
**影响**：节点级 patch / 精确「改某处」无法声明（V2.1 非目标，但需知现状）。
**建议**：iteration 留作 Phase D；V2.1 的「再来一张/换风格」用现有 whole-fork 即可。

## 8. 已具备、可直接复用的能力（好消息）

- ✅ **两个 workflow 已注册**：`server.go:530-531` `Register(images.TextToImageWorkflowDSL())` / `ImageToImageWorkflowDSL()`，名为 `text_to_image` / `image_to_image`。Agent launcher 的 `GetLatestByWorkflowName` 直接可用（`agent_outbox_launcher.go:32`）。
- ✅ **source.type=workflow 落地路径通**：launcher 完整支持 `workflow_name`。**因此两个图片 Skill 用 `source.type:workflow` 即可，不需要 tool_mode**（[v2/10 §11.3](../v2/10-skill-manifest-spec.md) 示例用的 `tool_mode` 当前 **launcher 不支持**——`parseLaunchSpec` 要求 `workflow_name` 非空，无 route/mode 落地分支）。
- ✅ **图片 Result Card 通用**：Observer.handleSuccess（`observer.go:284`）用 TaskResultReader 产出 `result_type/primary_file_url/cover_url`；图片 = 单图、cover 空、primary=图。
- ✅ **/works 归属机制通用**：task 带 `entry_type/route_key/mode_key` 即进 `ListByUserV2`（`internal/service/user.go:219`）；空则被过滤。short_drama Agent 任务已验证进 /works（[v2/17 §4](../v2/17-implementation-status-and-roadmap.md)）。
- ✅ **作品详情端点齐全**：`/works/info/:id`、`/works/:id/creative-detail`、`/works/:id/fork`、`/works/:id/patch-preview`（`router.go:222-232`）。图片有 `build_text_to_image_creative_detail` / `build_image_to_image_creative_detail`（`tool/images/`）。
- ✅ **Activity / Review / 终态幂等互斥通用**：Observer 读 `manifest.ActivitySteps / GateByCardType`（`observer.go:170/181`），非 short_drama 专用；finalize 守卫通用（`observer.go:351`）。图片 Skill 无 gate（`gates: []`），活动流是纯进度。
- ✅ **Fork/iterate 通用**：confirm 时自动 `forked_from=最近 task`（`conversation_service.go:374`），whole-fork = 整体重生成；「再来一张/换比例」可直接用。

---

## 9. 对需求文档第十四节 10 个问题的回答

**Q1. text_to_image / image_to_image 当前真实 Workflow 输入输出是什么？**
两者均为**已注册 workflow**（`server.go:530-531`），详见 [01](01-text-to-image-skill.md) / [02](02-image-to-image-skill.md)。摘要：
- text_to_image：必填 `input.user_prompt` + `input.mode="text_to_image"`；可选 `negative_prompt/style/model/api_provider/size/aspect_ratio/quality/seed/n/enable_prompt_optimize/content_audit_enabled`。输出 `result_type=image`、`primary_file_url=image_url`、`extras{image_url,image_asset_id}`、width/height（**单图**）。
- image_to_image：必填 `source_image_url` 或 `source_image_asset_id`（≥1）+ `input.user_prompt` + `input.mode="image_to_image"`；可选同上（provider 限 aliyun/volcengine，模型白名单）。输出同结构，extras 额外含 `source_image_url/source_image_asset_id`。

**Q2. 现有 Manifest 规范能否直接支持它们？**
**部分支持，需补 1 项关键能力。** `source.type=workflow + slots/maps_to/stages/activity_steps/cost` 足以描述两者；但**两个 workflow 强制 `input.mode`，而 Manifest 无固定/常量入参机制**（🔴 G1）。必须先补 `fixed_input`（或隐藏 slot 默认值）。此外深度校验缺失（🟡 G9）。

**Q3. image_to_image 的 Asset Slot 是否需要公共架构扩展？**
**是，需要较大扩展（这是 Level 2 的主要工作）。** 至少四处公共改动：会话 API 资产入口（🔴 G5）、Asset Slot 指代解析（🔴 G6）、launcher 注册输入资产引用（🔴 G7）、Slot 抽取支持 asset（🔴 G4）。详见 [02](02-image-to-image-skill.md)。

**Q4. 当前 Rule Intent 能否正确区分三个 Skill？**
**不能。** `rule_intent.go` 只内置短剧关键词（🔴 G3），对「画一张…」「把这张改成…」无规则。需把 Intent 关键词/examples 改为 **Manifest 驱动**，或由主架构负责人决策开启 Hybrid LLM fallback（**接入负责人不得自行开**）。多候选消歧亦未实现（🟡 G11）。

**Q5. 当前 Observer / Activity / Result 是否完全通用？**
**基本通用，两处不通用。** Observer/Activity/finalize/ReviewGate 都读 Manifest，非短剧专用（§8）。不通用处：(a) Result Card 不携带 Extras → image_to_image 输入图与多图无法展示（🟡 G12）；(b) Activity **headline** 在 runtime 写死「正在创作短剧」（🟡 G10）。

**Q6. 图片 ResultCard 与 /works 是否已有完整能力？**
**/works 完整可用**（entry 元数据机制 + 详情端点齐全，§8）。**ResultCard 基础可用但不完整**：单图能展示；image_to_image 的「输入→输出」关系、text_to_image 多图需 G12 扩展或走 creative-detail 端点。

**Q7. 接入过程中发现了哪些 short_drama 隐性特判？**
主要 5 处：PlanCard 构建器（🔴 G2）、Intent 关键词（🔴 G3）、Slot 抽取器（🔴 G4）、pendingAnchor/contextualFallback/Activity headline/clarify 文案（🟡 G10）、普通工具路径 `mode.Key=="short_drama"` grid 默认（`internal/service/tool.go:477`，不在 Agent 路径，仅供参考）。

**Q8. 需要主架构负责人修改哪些公共能力？**
🔴 G1（固定入参）、G2（通用 PlanCard）、G3（通用 Intent）、G4（通用 Slot 抽取）、G5（资产入口）、G6（Asset 指代解析）、G7（资产引用登记）、G8（Quote/计费一致）；🟡 G9（深度校验）、G10（去文案特判）、G11（多 Skill 消歧）、G12（Result Card 扩展）。**Level 1 最小集**：G1+G2+G3+G4（+G8 计费正确性）。**Level 2 追加**：G5+G6+G7（+G12）。

**Q9. 哪些能力可以由 Skill 接入负责人独立完成？**
两份 Manifest（与真实字段对账）、intent 负例与 examples、route_key/mode_key/entry 元数据对账（需与运营核对 route_key）、接入文档（本目录）、决策快照测试与回归清单。**前提**：相关公共缺口已由主架构负责人落地后，Manifest 才能真正驱动闭环。

**Q10. V2.1 的 PR 切分和回归计划是什么？**
见 §10。回归矩阵见 [03](03-agent-stability-regression.md)。

---

## 10. 路线图、PR 切分与验收标准

### PR 切分（建议）
| PR | 范围 | Owner | 依赖 |
|---|---|---|---|
| PR-A 文档（V2.1-0A） | 本目录 5 篇（含 [04 契约审计](04-skill-contract-compiler.md)）+ 缺口 G1–G21 + **真实 DB 契约对账** | 接入 | — |
| PR-0B 公共契约基座 | Contract Resolver/Auditor（两级解析 G16 + 0/多 active G17）+ Binding（fixed_input/system_injected G14）+ 后端默认合并（G18）+ Version Pin（G20） | 主架构 | PR-A 评审通过 |
| PR-B 公共·L1 基座 | G1 固定入参 + G2 通用 PlanCard + G3 通用 Intent + G4 通用 Slot（+G8 计费）+ G9 深度校验 | 主架构 | PR-0B |
| PR-C text_to_image Manifest + 接入 | text_to_image.yaml + 决策快照测试 + 端到端 | 接入 | PR-B |
| PR-D 公共·L2 资产能力 | G5 资产入口 + G6 指代解析 + G7 资产引用 + G12 Result 扩展 | 主架构 | PR-C 稳定 |
| PR-E image_to_image Manifest + 接入 | image_to_image.yaml + 连续修改 + 端到端 | 接入 | PR-D |
| PR-F 回归 short_drama | Level 3 全链路回归 | 双方 | PR-C/E |

### 验收标准（汇总需求文档第十三节）
- **text_to_image**：对话生成图片；模糊需求追问；正确 PlanCard；确认后执行；Activity 正确更新；只产**一个**最终 Result/Error；图片进 /works；可「再来一张/换风格/改横版」。
- **image_to_image**：可上传/引用图片；缺图追问；**输入图不选错**；PlanCard 展示输入图+修改要求；结果图进 /works；可继续改结果图。
- **架构**：接入后**无新增 short_drama 特判**；公共改动均经主架构负责人评审；**Manifest 成为每个 Skill 唯一注册入口**；无 Manifest 的 Workflow 不被 Agent 自动调用；三个 Skill 走统一 Conversation/Activity/Result/Works 链路。

---

本目录其余文档：[01 text_to_image 接入规格](01-text-to-image-skill.md) · [02 image_to_image 接入规格](02-image-to-image-skill.md) · [03 稳定性与回归矩阵](03-agent-stability-regression.md)
