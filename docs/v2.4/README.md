# DreamAI Agent V2.4 文档

> V2.4 = Agent Planner & Planner Trace。
> 本阶段只新增 `ai-engine/docs/v2.4/` 设计文档，不写 Go 业务实现代码。

V2.4 不继续扩大自然语言特判，也不是让 Agent 更会聊天。它在 V2.1、V2.2、V2.3 之后补正式的 Planner 抽象层：

```text
V2.1 Skill Contract
  -> Agent 能调用什么 Skill，Skill 的输入契约是什么，执行版本如何固定。

V2.2 Conversation Semantics
  -> 用户这一轮在做什么，是发起目标、回答追问、修改、取消、闲聊，还是再来一版。

V2.3 Object Semantics & Capability Runtime
  -> 用户动作作用于哪个对象，要做什么操作，当前对象允许调用哪个 Capability。

V2.4 Agent Planner
  -> 综合契约、回合语义、对象语义、能力空间和记忆，生成 ActionPlan，并解释为什么下一步是追问、生成 PlanCard、调用 Capability，还是拒绝/锚回当前任务。
```

一句话：

```text
V2.4 不是做“流式脑内独白”，而是做可审计的 PlannerTrace。
```

## 1. 核心定位

V2.4 需要回答：

- 用户真正目标是什么？
- 当前有哪些可用 Skill？
- 哪个 Skill 最适合？
- Skill 需要哪些输入？
- 用户已经提供了哪些输入？
- 还缺哪些输入？
- 缺失输入应该追问，还是可以自动推荐默认值？
- 默认值推荐的依据是什么？
- 为什么生成这个 PlanCard？
- 整个规划过程如何以白盒但安全的方式展示给用户？

Planner 的核心产物是后端语义实体：

- `PlannerInput`
- `ActionPlan`
- `PlannerAssumption`
- `PlannerDecision`
- `PlannerTrace`

`PlanCard` 只是 `ActionPlan` 的一种 UI 投射，不是 Planner 内部模型。

## 2. 三条红线

1. 本阶段只写文档，不写 Go 业务实现代码。
2. `PlannerTrace` 不是原始 Chain-of-Thought，不允许设计原始 Thought streaming 或原始 CoT 持久化。
3. `PlanCard` 只是 `ActionPlan` 的 UI 投射，不能把 `PlanCard` 当 Planner 内部模型。

扩展红线：

- 原始 LLM 输出不得直接成为业务事实。
- Planner 不能绕过 V2.1 Skill Contract。
- 用户可见 Trace 必须来自真实 Planner event、Assumption、Decision 或 Engine Observation，禁止假 Trace。
- Planner 主路径出错时必须可解释降级，不能静默回退到旧的短剧特判路径。

## 3. Agent 公式

```text
Agent = Reasoning + Planning + Memory + Tools
```

在 DreamAI 当前架构中：

| Agent 组成 | DreamAI 对应 |
| --- | --- |
| Reasoning | `TurnInterpreter`、`OperationInterpreter`、future LLM Interpreter |
| Planning | V2.4 `AgentPlanner`、`InputPlanningPolicy`、`ActionPlan`、`PlannerTrace` |
| Memory | `AgentState`、`PendingInteraction`、`CurrentPlan`、`TaskLinks`、conversation history |
| Tools | Skill、Workflow、Capability、Engine Task、Review Gate |

V2.4 是 Planner 能力的正式抽象层。它不是推翻 V2.1~V2.3，而是把它们组织起来。

## 4. 第一阶段评审交付

先交付前三篇文档进行评审：

| 文档 | 内容 |
| --- | --- |
| [README.md](README.md) | V2.4 定位、边界、目录、首轮评审范围 |
| [00-agent-planner-overview.md](00-agent-planner-overview.md) | Planner 总览、与 V2.1~V2.3 的关系、红线、可见过程 |
| [01-planner-input-output.md](01-planner-input-output.md) | `PlannerInput` / `ActionPlan` / `PlannerTrace` 模型，runtime 拼装与输出流向 |

前三篇评审通过后，再继续补齐：

| 文档 | 内容 |
| --- | --- |
| [02-input-planning-policy.md](02-input-planning-policy.md) | 缺参策略、required input 处理、默认值推荐与确认规则 |
| [03-skill-selection-and-catalog-observation.md](03-skill-selection-and-catalog-observation.md) | SkillCatalog 观察、Skill 匹配、多 Skill 仲裁 |
| [04-assumptions-and-defaults.md](04-assumptions-and-defaults.md) | `PlannerAssumption`、置信度、用户覆盖、默认值来源 |
| [05-planner-trace-and-visible-thinking.md](05-planner-trace-and-visible-thinking.md) | Trace 类型、存储、TTL、WS 摘要、懒加载、禁止 CoT |
| [06-plan-card-generation.md](06-plan-card-generation.md) | 从 `ActionPlan` 生成 PlanCard 的规则与边界 |
| [07-react-loop-boundary.md](07-react-loop-boundary.md) | 第一版 ReAct 边界、修改/取消/再来一版/out_of_scope 的 Planner 入口控制 |
| [08-llm-planner-roadmap.md](08-llm-planner-roadmap.md) | LLM Planner、function calling、MCP、工具发现的未来路线 |
| [09-implementation-and-migration-plan.md](09-implementation-and-migration-plan.md) | 实施计划、migration、灰度/Shadow、兼容老数据 |
| [10-regression-matrix.md](10-regression-matrix.md) | 回归矩阵、Mock Planner 自动化测试、short_drama 零回退 |
| [11-final-review-and-implementation-gate.md](11-final-review-and-implementation-gate.md) | 文档冻结、最终红线、PR-A 实施入口 |
| [12-llm-interpreter-pr-i.md](12-llm-interpreter-pr-i.md) | PR-I 混合对话解释器：LLM 接 TurnInterpreter fallback、仅 unknown 触发、bounded acts、降级规则、不记 CoT、回归清单 |
| [13-result-card-and-activity-ux.md](13-result-card-and-activity-ux.md) | P0 完成态语义修复 + P1 确认入Activity + P2 浮动显示 |
| [14-contextual-sufficiency-pr-l.md](14-contextual-sufficiency-pr-l.md) | PR-L 故事 brief 充分性 + user_prompt 污染守卫 + LLM 上下文复核 |
| [15-contextual-meta-intent-pr-m.md](15-contextual-meta-intent-pr-m.md) | PR-M stage-aware meta：confirming 自然语言确认、smalltalk 锚回、pending help |
| [16-multi-skill-batch-1-plan.md](16-multi-skill-batch-1-plan.md) | Batch 1 接入计划：1A 公共地基（Catalog-driven help / 通用 scoreSkill / 去 demo 特判）+ text_to_image + logo_design（复用 t2i mode）；i2i/i2v/product 推迟；四件套 + 回归矩阵 + 验收门 |

## 5. 第一版范围

第一版只做设计：

- Skill 选择。
- 输入规划。
- 合理默认值推荐。
- `PlannerTrace`。
- 用户可见 Planning Summary。
- 从 `ActionPlan` 生成 `PlanCard`。

第一版不做：

- 自由 LLM 工具调用。
- MCP Server。
- 跨 Skill 自动组合。
- 复杂多步自动执行。
- 原始 Chain-of-Thought 展示。
- 长时间 autonomous loop。
- 动态外部工具发现。

## 6. 第一版验收场景

用户输入：

```text
帮我做一个赛博朋克视频
```

假设 `video_gen` contract required：

```text
prompt
bgm_style
aspect_ratio
duration
```

Planner 输出：

```text
Goal = create_video
Skill = video_gen
prompt = 赛博朋克视频
bgm_style = synthwave_electronic_bass
aspect_ratio = 9:16
duration = 15s
NextAction = create_plan_card
```

PlannerTrace 摘要：

```text
Observation: observed user goal cyberpunk video
Observation: matched skill video_gen
Observation: missing required input bgm_style
Assumption: inferred bgm_style from cyberpunk theme
Decision: create plan for confirmation
```

用户可见 PlannerActivity：

```text
识别目标：赛博朋克视频
匹配能力：视频生成
检查输入：缺少音乐风格
自动推荐：电子重低音 / Synthwave
生成方案
```

PlanCard 展示的是 `ActionPlan` 中已校验的最终有效值，而不是 Planner 内部 Trace 或原始 LLM 输出。

## 7. 评审重点

文档评审必须重点回答：

- `PlannerInput` 如何从现有 `ConversationContext` / `TurnInterpretation` / `TargetResolution` / `OperationIntent` / `CompiledSkill` 组装。
- `InputPlanningPolicy` 如何处理 required input。
- 多个 Skill 同时匹配时如何仲裁。
- creative default 是否使用 LLM；如果使用，P99 如何控制在 1 秒以内。
- 用户否定推荐值时，`PlannerAssumption` 如何 override。
- `ActionPlan` 如何通过 Skill Contract / Validator 校验后生成 PlanCard。
- `PlannerTrace` 如何存储、展示、TTL、懒加载。
- `out_of_scope`、修改、取消、再来一版如何避免误触发新 Planner。
- LLM 输出脏 `ActionPlan` 时，Validator 如何拦截和降级。
- `short_drama` 现有闭环如何零回退。

## 8. 当前裁决

可以进入 V2.4 文档编写阶段。

但在 README、00、01 评审通过前，不继续写后续 02~10 的细节文档；在整套 V2.4 文档评审通过前，不进入业务实现。
