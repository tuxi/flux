# V2.1 · 05 · 主架构裁决与 V2.1-0B 实施计划（已接受决策）

> **本文是「已接受决策」与「新 Skill 接入解锁依据」。**
> - [04](04-skill-contract-compiler.md) = **真实系统调查、契约审计与风险事实**（G14–G21 的事实来源）。
> - **本文（05）= 主架构在 04 之上做出的已接受决策、0B 实施规则与解锁门。** 04 回答「现状是什么」，05 回答「我们决定怎么做、做到什么算完、谁什么时候解锁」。
>
> **裁决依据已对账代码**（非仅采信文档）：`agent_outbox_launcher.go`（G20：Agent 路径不固定 version、直接 `GetLatestByWorkflowName`）、`internal/service/tool.go:439-472`（G16/G17：tool 路径靠 UI 给定 `ToolModeID` 才安全，`GetCurrentPublishedByToolModeID` 用 `First`）、`CreateTaskByToolMode` 用 `req.Input` 不二次合并（G18）。

---

## 1. 裁决摘要（Q1–Q8 全部接受）

| # | 议题 | 裁决 | 关键约束 |
|---|---|---|---|
| Q1 | Resolver/Auditor（现在）vs Strong Compiler（未来）两层 | ✅ 接受 | Resolver 证明 **Coverage**（每个有效必填有来源），**不**证明 **Validity**（值过 `Execute()`）；后者靠契约测试 + 未来 G21 |
| Q2 | 输入契约权威矩阵（04 §6） | ✅ 接受 | `EffectiveRequired = Validator-required ∪ Execute() 硬规则`；**UI-required 仅建议**；`input_schema_json.allowedValues` **不得**作执行枚举真值 |
| Q3 | route_key→ToolDefinition→mode_key 两级解析 | ✅ 接受，强制 | **count==1** 校验；0/≥2 → 编译失败；**永不 `First()`** |
| Q4 | Manifest Binding 来源模型 | ✅ 接受（**见 §2 修正**） | mode_default 是 fallback；每 target ≤1 显式 Producer；skill_constant/system_injected **sealed** |
| Q5 | 统一默认合并顺序 | ✅ 接受（**见 §4**） | 后端合并（G18）；sealed 不可被覆盖；**PlanCard 展示合并后最终有效值** |
| Q6 | Plan/Task 固定 resolved 版本 | ✅ 接受（**见 §3 修正：pin_on_plan**） | 默认 active 切换**不**使 Plan 失效；仅版本不可用 / hash 漂移 / skill 显式 `require_current_on_confirm` 才需重生成 |
| Q7 | 第一版无法证明 Execute() 全部命令式约束 | ✅ 接受 | 能证 → error（禁注册）；不能证 → warning + 契约测试；**不重构 Validator** |
| Q8 | 未来 InputContractProvider 声明化 | ✅ 接受为方向 | 逐 workflow opt-in；**不进 0B**；落地后反向校验/生成 input_schema_json，根除 G15 |

**架构定性**：现在不是「Agent 架构没做完」，而是为「多 Skill」补**公共契约地基**。这层地基属**主架构**，Skill 接入人员不得在单 Skill 内硬编码绕过（解析 active version / 硬编码 `input.mode` / 手工合并默认 / 绕过 Registry）。

---

## 2. Binding 来源模型（修正 1 · 决定版）

**不是「每个 target 只能有一个来源」。** 正确模型：

```text
每个执行字段（target）的值 =
  ┌─ baseline：mode_default（来自 default_input_json）   ← fallback，不是 Producer
  └─ 至多一个【显式 Producer】，覆盖 baseline：
        · slot            （用户 / Agent 收集，maps_to）
        · skill_constant  （fixed_input 常量）         ← sealed
        · derived         （由其它字段推导）
        · system_injected （launcher 注入，如 callback_token）← sealed
```

**规则**：
1. **mode_default 是 baseline fallback**，覆盖所有字段的兜底值；它**不计入「显式 Producer」**，因此「字段有 mode_default + 又有一个显式 Producer」是合法的（Producer 覆盖默认）。
2. **每个 target 至多一个显式 Producer**。两个显式 Producer 指向同一 target（例：一个 slot 与一个 skill_constant 都写 `input.mode`）→ **编译失败**。
3. **skill_constant 与 system_injected 默认 sealed**：用户 slot / derived **不能**指向 sealed target（Auditor 拒绝），合并阶段也保证 sealed 值不被覆盖。这是「`mode` 不能被用户猜/改」的强制点。
4. **manifest 不重复声明 mode_default**；只在需要改变默认时用 `fixed_input`（skill_constant）显式覆盖，且覆盖值必须过校验。

> 与「exactly one source」的区别：mode_default 始终作为兜底层存在，显式 Producer 是叠加在它之上的**至多一个**覆盖来源；sealed 限定了用户输入永远到不了 `mode`/`callback_token`。

---

## 3. Version Pin 策略（修正 2 · 决定版：pin_on_plan）

**默认策略 = `pin_on_plan`。** Plan 在编译/出卡时固定 `resolved_tool_mode_version_id` + `resolved_contract_hash`；**active version 切换默认不使 Plan 失效**——honor 用户当时规划的版本。

**confirm 时的过期判定（只有以下三种才要求「重新生成 Plan」）**：

| 条件 | 判定 | 处理 |
|---|---|---|
| 固定版本**仍可执行**（行存在且未下线/归档）且 hash 未漂移 | ✅ 用固定版本执行 | 即使它已不再是 `is_current`，仍按 pin 执行 |
| 固定版本**不可用**（已 archived / offline / 删除） | ❌ 过期 | 「方案已过期，请重新生成」 |
| `resolved_contract_hash` **漂移**（admin 原地改了同一版本的 defaults/options/workflow_name…） | ❌ 过期 | 同上 |
| Skill 显式声明 `version_policy: require_current_on_confirm` | ❌ 若 pinned ≠ 当前 active 则过期 | 仅这类「强一致」Skill 需要 confirm 复核 active |

**`version_policy`（manifest，§5 / 04 §12）**：`pin_on_plan`（默认）| `require_current_on_confirm`（强一致）| `pinned:<version_id>`（后续，长期钉死）。

**为什么改默认**：admin 发布频繁，若「active 一切就让 Plan 失效」会把正常运营变成对用户的打断。`pin_on_plan` 让用户确认的就是执行的；真正危险的只有「pinned 版本没了」或「同版本被原地改」——这两种 hash/可用性都能抓到。

---

## 3.1 resolved_contract_hash（修正 · 命名与覆盖范围）

**`contract_schema_hash` → 更名 `resolved_contract_hash`。** 它不是「input schema 的 hash」，而是「**本次解析到的执行契约**的指纹」。

```text
resolved_contract_hash = SHA-256( normalize(
    workflow_name,            # active version 指向的执行 workflow
    defaults,                 # default_input_json.values（合并基线）
    options,                  # model_options_json / style_options_json / default_model / default_style
    effective_required,       # = Validator-required ∪ Execute() 硬规则（排序后的字段集）
    bindings                  # manifest 的 fixed_input / system_injected / derived / slot→target 映射
) )
```

- **不只覆盖 input_schema_json**——它是表单契约，本就会与执行漂移（04 §4 `api_provider` 实证）。
- 覆盖以上五项后：admin 改默认值/选项/换 workflow/调整必填，或 Skill 改 binding → hash 漂移 → confirm 时被识别为过期。
- `normalize` 必须**确定性**（键排序、去空白、数组排序），保证同输入同 hash、可比对、可复算。

---

## 4. 最终输入合并顺序（决定版）

```text
1. mode_default            （default_input_json.values）        — baseline，最低
2. 显式 Producer（每 target ≤1，覆盖 baseline）：
     · slot / derived      （可被更高层覆盖）
     · skill_constant      （sealed：用户/derived 不可覆盖）
     · system_injected     （sealed：最后应用，任何来源不可覆盖，最高）
```
- **合并在后端 Resolver 执行**（G18）——Agent 无客户端表单。
- **sealed 不可覆盖**由 §2 的 Auditor（禁止 slot 指向 sealed target）+ 合并顺序双重保证。
- **PlanCard 展示合并后的最终有效值**（非 manifest 原始默认），用户确认即所见即所执行。

---

## 5. CompiledSkill（Resolver 产物 · 决定版字段）

```text
CompiledSkill {
  manifest_key
  resolved_tool_mode_version_id   # §3 MUST 固定（pin_on_plan）
  workflow_name                   # 来自 active version，与 manifest 对账
  resolved_contract_hash          # §3.1 MUST 固定
  version_policy                  # pin_on_plan(默认) | require_current_on_confirm | pinned
  effective_required[]            # = Validator-required ∪ Execute() 硬规则
  binding[ target -> producer ]   # producer ∈ {slot, skill_constant, derived, system_injected}；mode_default 为隐式 baseline
  required_coverage{ target -> source }   # 每个 effective_required 必须被覆盖，否则编译失败
  merged_defaults                 # §4 step1（baseline）
  sealed[]                        # skill_constant ∪ system_injected 的 target，禁止 slot 指向
  audit_warnings[]                # Execute() 命令式约束（provider/model 白名单、enum、组合）无法静态证明 → 标注，交契约测试
}
```
**编译失败的硬条件**：无/多 active version · effective_required 有字段无来源 · 同 target 多显式 Producer · slot 指向 sealed target · maps_to 对不上 Validator 字段。失败按 04 §10 分环境处理（dev fail-fast / CI 红 / prod 隔离 disable）。

---

## 6. V2.1-0B 实施计划（PR 拆分与验收）

**架构约束（先定，贯穿全部 PR）**：
- **依赖方向单向**：Resolver 通过**只读 ToolContract 端口**（接口在 agent/skill 层定义，组合根用薄适配器包 `internal/repository/query/tool.go`）读 tool_* 表；`internal/` 不得反向依赖 agent（同 Observer 读 engine domain 的模式）。
- **Asset Slot（G5–G7）不在 0B**——它是 image_to_image 专属地基，独立 epic（**0C**），并行调研、0B 后落地。**0B 只解锁 text_to_image。**

| PR | 范围 | 验收标准 |
|---|---|---|
| **0B.1 两级 Resolver**（只读，无行为改动） | `ResolveActive(route_key, mode_key)`：route_key→tool_def(active+published)→该 def 下 mode_key(enabled+published)→`current+published` 版本，**count==1** | t2i→v15、i2i→v10 正确；**重复 `short_drama` mode_key 经 route_key 唯一消歧**；0/≥2 → `ErrNoActiveVersion`/`ErrAmbiguousVersion`（不 `First`） |
| **0B.2 Manifest binding schema**（解析+校验，未强制注入） | 扩展 `skill.Manifest`：`source.contract{route_key,mode_key,version_policy}`、`fixed_input`、`system_injected`、`derived`；short_drama 迁移到新结构 | 加载通过；**同 target 多显式 Producer / slot 指向 sealed → Validate 报错**；short_drama 行为不变 |
| **0B.3 Compiler/Auditor + 后端合并** | 产出 `CompiledSkill`：EffectiveRequired = `DataSchema.required` ∪ manifest 声明的 hard-rule 字段（从 04 §11 审计转录）；Coverage 全覆盖否则失败；§4 合并 + §2 sealed 保护 | t2i：`effective_required={user_prompt,mode}` 全覆盖（prompt=slot / mode=skill_constant / callback=system_injected）；**删掉 mode fixed_input → 编译失败**；**slot 指向 mode → 编译失败**；合并产出含 model/style/aspect 默认 |
| **0B.4 Registry 集成 + 分环境失败** | `LoadEmbedded→Resolve→Compile/Audit→Register`；无/多 active 或 coverage 缺失 → dev fail-fast / CI 红 / prod 隔离 disable+告警 | 坏 manifest 不进 Registry、不影响其它 Skill；日志给精确原因 |
| **0B.5 Plan/Task Version Pin（G20）** | agent `Plan`+entity 加 `resolved_tool_mode_version_id`+`resolved_contract_hash`+`version_policy`；经 LaunchIntent→outbox→LaunchSpec→`Task.ToolModeVersionID` 串联（复用 works-metadata 同款管道）；confirm 按 §3 过期判定 | Plan 存版本+hash；Task 带 ToolModeVersionID；**pin_on_plan：active 切换后 confirm 仍按固定版本执行**；**版本下线 / hash 漂移 → confirm 提示「方案已过期」**；short_drama Task 现也带 version |
| **0B.6 short_drama 回归（金丝雀）** | short_drama 走通新 Resolver/Pin 路径 | 端到端不变：launch→双审核→result→/works，现带 pinned version；**契约测试**：构造合并 input → 调真实 `Validator.Execute` → 期望通过 |

**0B Done 的定义（全局验收）**：
1. 两级解析消歧 `short_drama` 重复 mode_key；
2. 没有任何 Skill 在代码里硬编码 `input.mode`（全走 skill_constant binding）；
3. 后端默认合并生效；
4. Plan/Task version pin + `pin_on_plan` 过期判定可用；
5. short_drama 回归全绿（金丝雀）；
6. **负向测试**：required 无来源 / slot 指向 sealed / 多显式 Producer 的 manifest 注册失败。

**契约/静态分工（落实 Q7）**：0B.3 做**静态 Coverage**；0B.6 契约测试做**动态 Validity**（真实 `Validator.Execute`）。能证的报错 + 不能证的测试。

---

## 7. 解锁门（新 Skill 接入依据）

| Skill | 解锁条件 | 阶段 |
|---|---|---|
| **text_to_image** | 0B.1–0B.6 全绿 + 接入前 checklist：**逐项核对 t2i 9 个 model_options 是否都过 t2i Validator 白名单**（04 §16） | V2.1-1 |
| **image_to_image** | 0B 全绿 **+ Asset Slot 公共契约（0C / G5–G7）**：Conversation 携带/引用图片资产、AgentState 存资产引用、Task 登记输入资产引用、asset slot 绑定 `source_image_asset_id` | V2.1-2 |

**在 text_to_image 解锁门通过前，Skill 接入人员不开始正式接入代码。** 接入期间可并行：真实数据审计、Manifest 草案、Intent 正负例、Slot/PlanCard/Activity/Result 规格、Skill 专属测试、Asset Slot 客户端调研——但不得自行解析 active version / 硬编码 `input.mode` / 手工合并默认 / 自行实现 Asset Slot / 绕过 Registry。

---

## 8. 当前明确不做

Strong Contract Compiler · 全量 Validator 重构 · 全面声明化 enum/range/cross-field · 动态 CMS Manifest · RAG · LLM Intent · Multi-Agent · 节点级 Patch。
第一版 Resolver 目标：**阻止明显错误 · 统一 Binding · 统一默认 · 固定版本 · 暴露不可证风险**——不是证明 Workflow 永不失败。

---

## 9. 推进顺序

```text
V2.1-0A  契约与真实 DB 审计              ✅ 完成（04）
V2.1-0   主架构裁决与 0B 计划            ✅ 本文（05）
V2.1-0B  主架构实施 0B.1→0B.6           ← 下一步（按本文顺序）
V2.1-0C  Asset Slot 公共契约（i2i 前置） 并行调研、0B 后落地
V2.1-1   接入 text_to_image             0B 解锁门通过后
V2.1-2   接入 image_to_image + Asset Slot
V2.1-3   三级稳定性回归 t2i → i2i → short_drama
```

> 一句话给接入同学：**Workflow 决定怎么执行；ToolModeVersion 决定当前产品版本/默认/选项；Manifest 决定 Agent 怎么用；Resolver 在注册与 Plan 阶段把三者对齐。**

---

返回：[README](README.md) · 事实来源：[04 契约审计](04-skill-contract-compiler.md) · [00 总体方案](00-skill-integration-and-stability.md)
