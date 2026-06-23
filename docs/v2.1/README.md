# Agent First V2.1 文档

> V2.1 = 用两个简单图片 Skill（text_to_image / image_to_image）验证 Agent First 公共架构的**通用性、稳定性、可扩展性**，并建立「由简到繁」的三层回归梯度。
> **本轮文档先行，不写业务代码**（需求：文档审核通过前不开始正式编码）。
> 基线：`feat/agent-first-v2`（V2 主体已成立，见 [v2/17](../v2/17-implementation-status-and-roadmap.md)）。

## 文档角色（04 = 事实 · 05 = 决策）

- **[04](04-skill-contract-compiler.md) = 真实系统调查、契约审计与风险事实。** 真实库 `dream_log` 实拉对账（非模板），定义缺口 G14–G21、权威矩阵、两份真实契约报告。回答「**现状是什么、风险在哪**」。
- **[05](05-arch-decision-and-0b-plan.md) = 主架构已接受决策、0B 实施规则与新 Skill 接入解锁依据。** 在 04 之上拍板 Q1–Q8、定 Binding 模型 / Version Pin 策略 / 合并顺序 / 0B PR 拆分与验收 / 解锁门。回答「**我们决定怎么做、做到什么算完、谁何时解锁**」。

## 阅读顺序

1. [00 · 总体方案 + 架构缺口 + 10 问回答](00-skill-integration-and-stability.md) ← **从这里开始**
2. [01 · text_to_image 接入规格（Level 1）](01-text-to-image-skill.md)
3. [02 · image_to_image 接入规格（Level 2，含 Asset Slot 契约）](02-image-to-image-skill.md)
4. [03 · 稳定性与三层回归矩阵](03-agent-stability-regression.md)
5. [04 · 真实系统调查与契约审计（事实）](04-skill-contract-compiler.md) ← **事实来源 / 接入前置门槛**
6. [05 · 主架构裁决与 V2.1-0B 实施计划（决策）](05-arch-decision-and-0b-plan.md) ← **✅ 已接受决策 / 解锁依据**

> **接入门槛**：[05](05-arch-decision-and-0b-plan.md) 的 **0B.1–0B.6 全绿 + text_to_image 解锁门通过前**，不写 text_to_image / image_to_image 接入代码。
> 04 把「Manifest 凭经验手写输入契约」改为「以 active ToolModeVersion + Workflow Validator 为事实来源解析校验」；05 把它定为已接受决策并给出实施与验收。
>
> **两层落地**（05 §1 接受）：① V2.1-0A/0B = Skill Contract **Resolver/Auditor**（现在可落地，证明 Coverage、**不**证明 Validity）；② **Strong Contract Compiler** = 未来，依赖 G21 `InputContractProvider` 把约束声明化。
>
> **接入顺序**（05 §9）：0A 审计 → **0B 最小公共 Resolver/Binding/Version Pin（0B.1→0B.6）** → 0C Asset Slot（i2i 前置）→ text_to_image → image_to_image → 三级回归。

## 一句话结论

- 两个图片 workflow **均已注册**（`text_to_image` / `image_to_image`），`source.type=workflow` 落地路径可用，**无需 tool_mode**。
- Manifest / Observer / Activity / Result / Works / Fork 的**骨架基本通用**；但 Intent、Slot 抽取、PlanCard、若干文案对 short_drama **仍有硬编码**（缺口 G2/G3/G4/G10）。
- 接入必须先补的公共能力：**G1 固定入参（mode）**、**G2 通用 PlanCard**、**G3 通用 Intent**、**G4 通用 Slot 抽取**；image_to_image 另需 **G5 会话资产入口 / G6 指代解析 / G7 资产引用登记**。
- 契约层（[04](04-skill-contract-compiler.md)）：`tool_mode_versions.input_schema_json` 是**前端表单协议**，与 Workflow Validator 的执行约束**会漂移**（实证：i2i `api_provider` UI 允许 `auto/kling/bytedance`，Validator 只接受 `aliyun/volcengine`）；真实约束分散在 default/options + Validator `DataSchema`/`Execute()` 三处。故现阶段只做 **Resolver/Auditor**，不宣称 ToolModeVersion 是完整契约唯一事实来源。`mode` 不在 UI schema → 必须 skill_constant 注入。
- 这些缺口**全部属于主架构负责人**；接入负责人产出 Manifest、examples、文档、测试，**不得自行特判或开 LLM fallback**。

详细缺口编号与证据见 [00 §7](00-skill-integration-and-stability.md#7-架构缺口清单提交主架构负责人评估)。
