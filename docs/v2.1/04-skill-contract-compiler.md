# V2.1 · 04 · Skill Contract Resolver / Auditor 与契约审计（V2.1-0）

> **本文是 text_to_image / image_to_image 正式接入的前置门槛。审核通过前不写 Skill 接入代码。**
> 核心命题：**Skill Manifest 不能凭经验手写输入契约**。每个 Skill 的真实输入字段/必填/默认值/枚举，应以**当前生效的 ToolModeVersion** + **Workflow Validator** 为事实来源，编译期解析、校验、合并，产出运行时契约。
>
> 字段与数据全部对账：实体 `internal/model/entity/tool_*.go`、仓储 `internal/repository/query/tool.go`、`internal/service/tool.go`、`ai-engine/tool/schema.go`、`ai-engine/workflows/images/*param_validate.go`，**以及真实开发库 `dream_log` 的实拉数据（§11）**。
> 上位：[00 总体方案](00-skill-integration-and-stability.md)。本文新增缺口 **G14–G21**。

---

## 0. 两层落地（最重要的结论 · 投资点 #1）

经真实代码 + DB 审计，**「ToolModeVersion = 完整输入契约唯一事实来源」当前不成立**：真实约束分散在三处（§6），且 `input_schema_json`（前端表单协议）与 Workflow Validator 的**执行约束并不一致**（§4 实证：i2i 的 `api_provider` UI 允许 `auto/kling/bytedance`，但 Validator 只接受 `aliyun/volcengine`）。因此把 V2.1-0 拆成两层：

```text
┌─ V2.1-0A / 0B：Skill Contract Resolver + Auditor（现在可落地）────────────┐
│  能做：解析 active ToolModeVersion → 合并 default_input_json →            │
│        对账 Manifest slots / Validator 声明式 required → 覆盖审计 →        │
│        固定 version_id + schema_hash。                                     │
│  不能做：证明 Validator Execute() 里所有命令式约束（enum/range/组合）。     │
│         这些只能「人工对账 + 测试」，无法静态编译保证。                      │
└──────────────────────────────────────────────────────────────────────────┘
                                  ⇩ 依赖
┌─ 未来：Strong Contract Compiler（需先有声明式强契约源）────────────────────┐
│  前提：Workflow 提供 InputContractProvider（§13 / G21），把 Execute() 里的 │
│        enum/range/组合约束声明化为机器可读 Schema。                         │
│  才能做：编译期完整校验「Manifest × 强契约」，真正保证 act() 不会被         │
│          Validator 拒绝。                                                   │
└──────────────────────────────────────────────────────────────────────────┘
```

> **现阶段交付 = Resolver/Auditor**，它**显式声明自己的边界**：对 Validator Execute() 的命令式约束（如 provider/model 白名单、size/aspect 合法集、`mode` 必须等于常量、`source_image_url|asset_id` 二选一）只能**审计提示 + 测试覆盖**，不能编译期证明。Strong Contract Compiler 是 InputContractProvider 就绪后的演进，不在 V2.1 范围。

## 1. 当前真实数据模型（对账）

| 表 | 实体 | 关键字段 |
|---|---|---|
| `tool_definitions` | `entity.ToolDefinition` | `id, key, route_key, status, is_active` |
| `tool_modes` | `entity.ToolMode` | `id, tool_definition_id, key, status(draft/published/offline), is_enabled, is_default` |
| `tool_mode_versions` | `entity.ToolModeVersion` | `id, tool_mode_id, version, status(draft/published/archived), is_current, workflow_name, input_schema_json, default_input_json, render/output_config_json, model_options_json, default_model, style_options_json, default_style` |

层级：`ToolDefinition(route_key) → ToolMode(mode_key) → ToolModeVersion(workflow_name + schema + defaults)`。
关键：**`workflow_name` 在 ToolModeVersion 上**（`tool_mode_version.go:44`）——active 版本本身指向 workflow，`CreateTaskByToolMode` 即 `GetLatestByWorkflowName(modeVersion.WorkflowName)` 启动（`tool.go:469`）。**契约源与执行源在 as-built 中本就统一于 active ToolModeVersion。**

## 2. active ToolModeVersion 解析规则（投资点 #2）

复用 `CreateTaskByToolMode`（`internal/service/tool.go:439-472`）真实链路：

```text
1. route_key → ToolDefinition   （is_active=true && status=published）
2. mode_key  → ToolMode under that tool_def （is_enabled=true && status=published）
3. ToolMode.id → active ToolModeVersion
   = GetCurrentPublishedByToolModeID: WHERE tool_mode_id=? AND status='published' AND is_current=true  （tool.go query:260-267）
4. ToolModeVersion.workflow_name → GetLatestByWorkflowName → WorkflowVersion（执行）
```

**🔴 G16（实证）**：`GetByModeKey`（`tool.go:173`）只按 `tool_modes.key` `First`、**不带 tool_def / status 过滤**。真实库 `dream_log` 存在**两个 `short_drama` mode_key**：

| mode_id | status | tool_def | route_key |
|---|---|---|---|
| 2059859401782607872 | published | short_drama | `short_drama` |
| 2059736557887176704 | draft | dream_studio | `dream_studio` |

→ `GetByModeKey('short_drama')` 结果**非确定**，可能命中 draft/dream_studio 那条。**Compiler 必须按 (route_key → tool_def → mode_key under that def) 两级解析**，绝不能只用 mode_key。
（已验证：`route_key='image_generation'` 唯一对应 1 个 tool_def(id=2)、其下 6 个 mode —— 两级解析可消歧。）

## 3. 0 个 / 多个 active version（投资点 #3）+ 真实审计（投资点 #4）

`is_current` **无 DB 唯一约束**，仅靠 admin 发布流程 `SetAllVersionsNonCurrent` 后置新 current（`tool.go:241`）保证「应当唯一」。`GetCurrentPublishedByToolModeID` 用 `First` → **多 current 时非确定**。

**真实库全量审计结果（`dream_log`，2026-06；按 mode 统计 current+published 版本数）**：

| 类别 | 结果 |
|---|---|
| published mode 且恰好 1 个 current+published | ✅ 全部（含 text_to_image=1、image_to_image=1、short_drama(published)=1、oral_sales、ui_asset_design…） |
| ≥2 个 current+published | **本库未发现**（风险存在但当前数据干净） |
| current+published = 0（draft-only mode） | `hook_video / compare_video / poster_bundle / video_cut_timeline / short_drama(draft, dream_studio)` ——若 Skill 误指这些 mode 则解析失败 |

| 情况 | Compiler 行为（目标） |
|---|---|
| 恰好 1 个 | ✅ 正常编译 |
| 0 个 | ❌ 编译失败：Skill **不注册**（无契约不可被 Agent 调用） |
| ≥2 个 | ❌ 编译失败 + 告警；**不靠 `First` 静默选一个**（🟡 G17：需显式 count 校验，不依赖 `First`） |

## 4. input_schema_json 真实格式（投资点 #4 · 含重要校正）

> ⚠️ **校正上一版**：实体注释（`tool_mode_version.go:29`）把它描述为「UI 渲染协议（show_prompt/max_images）」，但**真实库里它是一份 form-field schema**：`{"fields":[{key,type,title,sort,visible,required,rules.allowedValues,placeholder}]}`。它**确实带 `required` 与 `allowedValues`**，比注释丰富。**但它仍是「前端表单契约」，与「执行契约」并不等价**——见下三点实证。

text_to_image active(v15) 的 `fields`（真实）：`user_prompt(required,visible)`、`model(required,visible)`、`quality(required,visible,[standard,hd])`、`aspect_ratio(required,visible,[9:16,16:9,1:1,4:3,3:4,3:2,21:9])`、`style(optional)`、`negative_prompt(optional,hidden)`、`size(hidden,[1024x1024])`、`api_provider(hidden,[auto,kling,bytedance])`、`transparent_background(optional)`、`enable_prompt_optimize(optional)`。

**三点实证（为什么 input_schema_json ≠ 执行契约）**：
1. **`mode` 根本不在 fields 里**（t2i/i2i 都没有）。但 Validator **强制要求** `input.mode` 且必须等于常量（`text_to_image_param_validate.go:131` / `image_to_image_param_validate.go:135`）。→ `mode` 由「mode 身份」隐含，必须由 **skill_constant / fixed_input** 注入（G14）。`callback_token` 同理（system_injected）。
2. **UI-required ⊋ Validator-required**。UI 把 `model/quality/aspect_ratio` 标 required（表单 UX）；但 Validator **只硬性要求** `user_prompt`(+`mode`)（i2i 再加 `source_image_*`），`model/quality/aspect_ratio` 在 Validator 里可空（走 normalize/默认）。→ 「必填」有三种口径：UI-required、Validator-required、有默认值。**Agent 的硬契约以 Validator 为准**，UI-required 仅作「尽量收集/默认」。
3. **UI allowedValues 与 Validator 不一致（硬证据）**：i2i 的 `api_provider` UI allowed = `[auto,kling,bytedance]`，而 i2i Validator 只接受 `aliyun/volcengine`（`image_to_image_param_validate.go:147`）。两者**不相交**。→ **不能把 input_schema_json 的 allowedValues 当执行枚举真值。**

**🔴 G15（核心缺口）**：缺少**机器可读的强类型执行契约**。input_schema_json 是表单契约，可能与 Validator 漂移；真正的执行约束（含 enum/白名单/组合）部分在 Validator `Execute()` 代码里（§6）。

## 5. default_input_json 合并语义（投资点 #5）

`default_input_json = {"values":{...}}`，真实值：

| mode | default_input_json.values |
|---|---|
| text_to_image | `model=qwen-image-plus, style=photography, aspect_ratio=1:1, enable_prompt_optimize=true, transparent_background=false` |
| image_to_image | `model=doubao-seedream-4.0, style=photography, aspect_ratio=3:4, enable_prompt_optimize=true, content_audit_enabled=false` |

注意：**两者都没有 `quality` 默认**，但 UI 标 `quality` required → 客户端靠用户选。Agent 无表单 → 必须补 `quality`（Validator 对空 quality 会 normalize 到 `standard`，故可安全省略，但需测试确认）。

现状：`CreateTaskByToolMode` **不在后端二次合并 default_input_json**（直接用 `req.Input`）——默认合并发生在**客户端表单**。
**🔴 G18**：Agent 无表单 → **Resolver 必须在后端承担 default_input_json 合并**，否则 model/style/aspect_ratio 缺省为空。这是 Agent 与工具路径的语义差异点。

## 6. 输入契约权威矩阵（投资点 #2 · 新增）

「谁对什么负责」——这是本轮要求的**权威矩阵**：

| 关注点 | 权威来源 | 证据 | 备注 / 局限 |
|---|---|---|---|
| **该能力存在/可用** | ToolDefinition.status+is_active、ToolMode.status+is_enabled | `tool.go:449/465` | 任一未发布 → 不可编译 |
| **active 版本选择** | ToolModeVersion `is_current && status=published` | `tool.go:260` | 无唯一约束（G17） |
| **执行用 Workflow** | `active ToolModeVersion.workflow_name` → 最新 WorkflowVersion | `tool.go:469` | Manifest 不应另写权威 workflow_name（G14） |
| **字段「有默认值」** | `default_input_json` | §5 | quality 无默认 |
| **model 枚举/默认** | `model_options_json` / `default_model` | §11 | i2i options 已与 Validator 白名单一致；t2i 需逐项核对 |
| **style 枚举/默认** | `style_options_json` / `default_style` | §11 | — |
| **表单显隐/UI 必填/UI 候选** | `input_schema_json.fields[].{visible,required,rules.allowedValues}` | §4 | **表单口径**，可能与执行漂移（api_provider 实证） |
| **执行「类型/必填」（声明式）** | Workflow Validator `tool.DataSchema`（`InputSchema()`） | `*_param_validate.go:28` | `FieldSchema` 仅 `{Type,Required,Desc}`，**无 enum/默认/范围** |
| **执行「enum/范围/组合」约束** | Workflow Validator `Execute()` **代码** | `*_param_validate.go:Execute` | **命令式、非声明式**（mode==常量、provider/model 白名单、size/aspect/quality 合法集、source 二选一）→ Resolver **无法静态证明**，只能审计+测试（G19；未来 G21 解决） |
| **该能力如何被 Agent 发现/询问/规划/展示** | Skill Manifest | `agent/skill/manifests/*.yaml` | slot.ask / examples / plan_stages / activity_steps |
| **常量/系统注入/派生入参** | Manifest bindings（fixed_input / system_injected / derived） | §7（G14） | mode=skill_constant；callback_token=system_injected |
| **运行时合并后的最终契约** | CompiledSkill（§14） | 编译产物 | 固定 version_id + schema_hash（§9） |

**「有效必填集」EffectiveRequired = Validator-required ∪ Validator Execute() 硬规则**（**不**直接采信 UI-required；UI-required 仅用于「建议收集」）。每个 EffectiveRequired 字段必须由**恰好一种来源**满足：`slot | mode_default | skill_constant | derived | system_injected`；**有 required 无来源 → 编译失败、禁止注册**。

## 7. 固定 / 系统注入 / 派生输入声明（投资点 #8）

Manifest 当前结构体（`skill/manifest.go:17`）**无**这些段（🔴 G14）。目标：
```yaml
fixed_input:   { mode: text_to_image }     # skill_constant：act() 直接写入，覆盖 mode 默认
system_injected: [ callback_token ]        # 仅声明，值由 launcher 注入（agent_outbox_launcher.go:42）
derived:       [ { key: shot_count, from: "grid_size - 1" } ]   # 仅 short_drama 示例
```

## 8. 最终输入合并顺序（投资点 #9）

```text
1. default_input_json（mode 默认，最低优先）
2. Manifest fixed_input（skill_constant 覆盖）
3. 用户 / Agent collected slots（maps_to）
4. derived
5. system_injected（callback_token，最高优先，不可被覆盖）
```
- **Manifest 不重复 mode 默认**；只在需改变默认时用 `fixed_input` 显式覆盖，覆盖值必须过校验。
- **default_input_json 优先于 Manifest slot.default**（避免 Skill 漂移 mode 行为）——此优先级须在 G14 落地时固化并测试。
- **PlanCard 必须展示合并后的最终有效值**（非 Manifest 原始默认）。

## 9. Plan / Task 版本与契约固定（投资点 #10 · 强化为 MUST）

**要求（MUST）**：编译/出 PlanCard 时，Resolver MUST 把以下固定到 **Plan**，并在 confirm→launch 时透传到 **Task**：
```text
resolved_tool_mode_version_id   # 本次解析到的 active 版本 id
contract_schema_hash            # 规范化(input_schema_json + Validator DataSchema) 后 SHA-256
```
**理由（实证场景）**：active 版本随 admin 发布**随时可切**（`SetAllVersionsNonCurrent`）。若 Plan 确认前被切换，用旧契约静默执行 = 错误扣费/错误参数。固定后：confirm 时比对 `resolved != 当前 active` → **提示「方案已过期，请重新生成」**，不静默执行。

**现状差距**：
- Task **已有 `ToolModeVersionID`**（工具路径 `tool.go:509` 写入），但 **Agent 路径 `engineTaskLauncher.CreateTask` 未写**（🔴 G20）。
- **`contract_schema_hash` 不存在**（无该列；agent `Plan` 结构 `domain/types.go:139` 也无固定字段）。需新增。

## 10. active 切换重校验 + 编译失败分环境（投资点 #11/#12）

**切换重校验（#11）**：
- 启动期：`LoadEmbedded → Resolve active → Audit/Compile → Register`，全量校验。
- 运行期切换：admin 发布新 active 应触发**按 (route_key,mode_key) 反查 Manifest → 重新编译**；失败 → 该 Skill 标 `disabled` 摘除，不影响其它。
- 执行中保护：已出 Plan 用 §9 的固定字段做过期检测。

**编译失败分环境（#12）**：
| 环境 | 处理 |
|---|---|
| 开发 | fail-fast：启动报错中断，打印精确原因（缺 required 无来源 / 无 active / 多 active / maps_to 对不上） |
| 测试/CI | 作为契约测试，失败即红，阻断合并 |
| 生产 | **隔离降级**：单 Skill 失败 → 不注册/标 disabled，**不影响进程与其它 Skill**；告警 + 指标 |

## 11. 契约审计报告（投资点 · 真实 DB 对账，非模板）

> 数据来源：开发库 `dream_log`（localhost），2026-06 实拉。下列均为**真实 rows**，并与 Workflow Validator 逐项对账。
> （取数方法：`route_key='image_generation'` → tool_def(id=2) → mode_key → `is_current && status='published'`。）

### 11.1 text_to_image — 真实契约
| 项 | 真实值 |
|---|---|
| route_key / mode_key | `image_generation` / `text_to_image` |
| tool_definition_id | `2`（key=image_generation, published） |
| tool_mode_id | `2001`（published, is_enabled） |
| **active tool_mode_version_id** | `2049520426400366592`（**version 15**, published, is_current） |
| workflow_name | `text_to_image` ✅（= 已注册 workflow `server.go:530`） |
| default_input_json | `{model:qwen-image-plus, style:photography, aspect_ratio:1:1, enable_prompt_optimize:true, transparent_background:false}`（**无 quality**） |
| default_model / default_style | `qwen-image-plus` / `photography` |
| model_options（9 个） | qwen-image, qwen-image-plus, wan2.7-image-pro, wan2.7-image, wan2.6-image, wan2.6-t2i, doubao-seedream-4.0/4.5/5.0-lite |
| style_options（8 个） | photography, illustration, poster, realistic, anime, cinematic, cyberpunk, fantasy |
| UI required（input_schema） | user_prompt, model, quality, aspect_ratio |
| UI aspect_ratio allowed | 9:16,16:9,1:1,4:3,3:4,3:2,21:9 |

**Validator 对账（`text_to_image_param_validate.go`）**
| 字段 | Validator | 来源（CompiledSkill 目标） |
|---|---|---|
| `user_prompt` | **required**（:122 + DataSchema:33） | **slot** `prompt` → input.user_prompt |
| `mode` | **硬规则 == `text_to_image`**（:131）；**不在 UI schema** | **skill_constant** `fixed_input.mode` |
| `callback_token` | signal 路由所需；不在 UI schema | **system_injected**（launcher:42） |
| `model` | 可空（normalize） | **mode_default** qwen-image-plus（可选开 slot） |
| `quality` | 可空（空→standard）；UI required 但 **无 mode 默认** | **skill 兜底**（建议 fixed_input 或 slot default=standard） |
| `aspect_ratio` | 可空（normalize）；UI required | **slot**（默认 mode_default=1:1）；options 对齐 UI allowed |
| `style` | 可空 | **mode_default** photography（可选 slot） |

**EffectiveRequired = {user_prompt, mode}**（+ callback_token 系统）。覆盖✅（prompt slot / mode fixed / callback system）。
**最终执行输入样例**：`{user_prompt, aspect_ratio:"9:16", mode:"text_to_image", model:"qwen-image-plus", style:"photography", callback_token}`。

### 11.2 image_to_image — 真实契约
| 项 | 真实值 |
|---|---|
| route_key / mode_key | `image_generation` / `image_to_image` |
| tool_definition_id / tool_mode_id | `2` / `2046825768654225408`（published） |
| **active tool_mode_version_id** | `2059447577333481472`（**version 10**, published, is_current） |
| workflow_name | `image_to_image` ✅（`server.go:531`） |
| default_input_json | `{model:doubao-seedream-4.0, style:photography, aspect_ratio:3:4, enable_prompt_optimize:true, content_audit_enabled:false}`（**无 quality**） |
| model_options（6 个） | wan2.7-image-pro, wan2.7-image, doubao-seedream-4.0/4.5/5.0-lite, wan2.5-i2i-preview ——**全部 ∈ Validator 白名单 ✅** |
| UI required（input_schema） | user_prompt, model, quality, aspect_ratio, **source_image_asset_id** |
| UI source 字段 | `source_image_asset_id`（type=image, required, visible）——**asset_id 优先，无 url 字段** ✅ |
| ⚠️ UI api_provider allowed | `[auto,kling,bytedance]` —— **与 Validator(`aliyun/volcengine`) 不一致**（§4 实证；该字段 visible=false） |

**Validator 对账（`image_to_image_param_validate.go`）**
| 字段 | Validator | 来源 |
|---|---|---|
| `source_image_url` **或** `source_image_asset_id` | **硬规则 二选一**（:123） | **asset slot** `source_image` → input.source_image_asset_id（依赖 G5–G7） |
| `user_prompt` | **required**（:128） | **slot** `prompt` |
| `mode` | **硬规则 == `image_to_image`**（:135）；不在 UI schema | **skill_constant** |
| `model` | 白名单（:154）；options 已对齐 | **mode_default** doubao-seedream-4.0 |
| `api_provider` | 仅 aliyun/volcengine（:147）；**UI allowed 不一致** | **不暴露**（留空→默认路由）；勿采信 UI allowed |
| `quality` | 空→standard | skill 兜底 |
| `aspect_ratio` | normalize | slot（mode_default=3:4） |

**EffectiveRequired = {source_image(url|asset_id), user_prompt, mode}**（+callback_token）。
**最终执行输入样例**：`{source_image_asset_id:8842, user_prompt:"改成动漫风", mode:"image_to_image", model:"doubao-seedream-4.0", callback_token}`。

### 11.3 审计结论
- 两个 mode 的 active 版本干净（各 1 个 current+published），workflow_name 与已注册 workflow 一致 ✅。
- route_key 均为 `image_generation`（[01](01-text-to-image-skill.md)/[02](02-image-to-image-skill.md) 中「待核对」**现已确认**）。
- **`mode` 确实不在 UI schema**——必须由 skill_constant 注入，不暴露用户 ✅（满足本轮硬要求）。
- **input_schema_json 不可作执行枚举真值**：i2i `api_provider` UI/Validator 不一致是确凿反例。
- i2i model_options 与 Validator 白名单一致；**t2i 的 9 个 model_options 是否都被 t2i Validator 接受需逐项核对**（t2i Validator 也有 `supportsConfiguredTextToImageModel` 白名单）——列为接入前 checklist。

## 12. Manifest 结构升级（执行源 vs 契约源）

```yaml
source:
  type: workflow
  # workflow_name 不再手写为权威；编译期从 active ToolModeVersion.workflow_name 取并对账
  contract:
    route_key: image_generation
    mode_key: text_to_image          # 配合 route_key 两级解析（G16）
    version_policy: active           # active（默认）| pinned:<version_id>（后续）
```
现状 `skill.Source`（`manifest.go:74`）只有 `Type/WorkflowName/RouteKey/ModeKey`，**无 contract 子结构 / version_policy**（G14）。

## 13. InputContractProvider（投资点 #6 · 提交主架构评估 · G21）

为支撑未来 **Strong Contract Compiler**，建议主架构评估让 Workflow 暴露一个 **InputContractProvider**：把现在散落在各 `*_param_validate.go` 的 `Execute()` 命令式约束**声明化**为机器可读的强契约：
```text
InputContract {
  field -> { type, required, enum[], range{min,max}, const, one_of_group, depends_on }
}
```
- 来源：可由各 Validator 主动声明（扩展 `tool.FieldSchema` 增加 enum/range/const/group），或独立契约文件。
- 价值：① Resolver 可编译期完整校验 Manifest×契约；② 消除 input_schema_json 与 Validator 的漂移（UI schema 可由强契约生成/校验）；③ 计费/Quote/Agent/客户端共用同一契约。
- **演进而非 V2.1 目标**：先做 Resolver/Auditor（§0 上层），InputContractProvider 就绪后再升级为 Strong Contract Compiler。

## 14. CompiledSkill（Resolver 产物）

```text
CompiledSkill {
  manifest_key
  resolved_tool_mode_version_id     # §9 MUST 固定
  workflow_name                     # 来自 active version（与 Manifest 对账）
  contract_schema_hash              # §9 MUST 固定
  effective_required[]              # = Validator-required ∪ Execute() 硬规则（§6）
  required_coverage{ field -> source }   # 全覆盖才编译成功（slot/mode_default/skill_constant/derived/system_injected）
  merged_defaults                   # §8 step1+2
  fixed_input / system_injected / derived
  slots[]                           # 经 Validator DataSchema 类型/必填校验
  audit_warnings[]                  # Execute() 命令式约束无法静态证明的提示（provider/model 白名单、enum 等）
}
```
- 编译失败（无/多 active、required 无来源、maps_to 对不上 Validator 字段）→ §10 分环境处理。
- 这是「无 Manifest / 无法编译的 Workflow 不被 Agent 调用」的强制执行点。
- **audit_warnings 体现 §0 边界**：Resolver 如实标注它「证明不了」的命令式约束，交给测试与人工对账。

## 15. 本文新增架构缺口（提交主架构负责人）

| 编号 | 缺口 | 关联 |
|---|---|---|
| 🔴 G14 | Manifest 缺 `source.contract` / `version_policy` / `fixed_input` / `system_injected` / `derived` | §7/§12 |
| 🔴 G15 | 无机器可读强类型执行契约（input_schema_json 是表单契约，会与 Validator 漂移） | §4/§6 |
| 🔴 G16 | 契约解析须 (route_key→tool_def→mode_key) 两级；`GetByModeKey` 仅按 key `First`，真实库 short_drama mode_key 重复 | §2 |
| 🟡 G17 | active 0/多 处理：`is_current` 无唯一约束，`First` 非确定（当前库数据干净但风险在） | §3 |
| 🔴 G18 | Agent 无表单 → default_input_json 合并须移后端 | §5/§8 |
| 🟡 G19 | enum/范围/组合非声明式（`FieldSchema` 仅 Type/Required/Desc；约束在 `Execute()`） | §6 |
| 🔴 G20 | Plan/Task 未固定 `resolved_tool_mode_version_id`（Agent 路径）+ 无 `contract_schema_hash` | §9 |
| 🟢 G21 | 建议新增 Workflow `InputContractProvider`，把 Execute() 约束声明化（Strong Contract Compiler 前提） | §13 |

> Resolver / Auditor / Binding / Active Resolver / 默认合并 / Version Pin / Asset Slot 公共契约 / InputContractProvider——**全部属主架构负责人**。Skill 接入负责人写 Manifest、examples、Skill 专属测试与本类文档，**不得在单 Skill 内硬编码绕过**。

## 16. V2.1-0 验收标准
- [x] 文档明确两层落地：Resolver/Auditor（现在）vs Strong Contract Compiler（未来，依赖 G21）。
- [x] 输入契约权威矩阵（§6）。
- [x] 两份**真实 DB** 契约报告（§11），与 Validator 逐项对账（非模板）。
- [x] 全量 current+published 版本审计（§3）：发现 draft-only=0 的 mode 与 **short_drama mode_key 重复**。
- [x] Plan/Task MUST 固定 `resolved_tool_mode_version_id` + `contract_schema_hash`（§9）。
- [x] `input.mode` 由 skill_constant 满足、不暴露用户（§11 实证其不在 UI schema）。
- [ ] 主架构评估 G14–G21（尤其 G21 InputContractProvider 是否纳入 V2.1-0B）。
- [ ] 接入前 checklist：核对 t2i 9 个 model_options 是否都被 t2i Validator 白名单接受。

---

返回：[00 总体方案](00-skill-integration-and-stability.md) · [01 text_to_image](01-text-to-image-skill.md) · [02 image_to_image](02-image-to-image-skill.md) · [03 回归矩阵](03-agent-stability-regression.md)
