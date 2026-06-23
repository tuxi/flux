# 00 · Agent Planner Overview

## 1. 问题定位

V2.1~V2.3 已经让 Agent 拥有三类基础能力：

- V2.1：知道哪些 Skill 可用，Skill 的输入契约是什么，执行版本如何固定。
- V2.2：知道用户这一轮在当前上下文中是什么动作。
- V2.3：知道用户动作作用于哪个对象，以及当前对象允许什么能力。

但当前系统仍缺一个正式的 Planner 层来回答：

```text
在这些已知事实之上，下一步到底应该怎么规划？
```

典型缺口：

- Skill 匹配结果、缺参判断、默认值合并、PlanCard 生成仍容易分散在 runtime 分支里。
- 缺 required input 时只有粗粒度的“追问/不追问”，无法区分必须用户提供、可创意推断、可系统默认、影响费用的默认值、必须上传素材。
- PlanCard 能展示方案，但无法解释“为什么推荐这些值”。
- 用户可见的“AI 正在规划”如果没有结构化事实来源，容易滑向伪 Thought 或原始 CoT 暴露。

V2.4 的目标不是让 Agent 多说几句，而是让每一次规划都成为可测试、可审计、可解释的结构化结果。

## 2. V2.4 定位

V2.4 新增：

```text
AgentPlanner
InputPlanningPolicy
ActionPlan
PlannerAssumption
PlannerDecision
PlannerTrace
PlannerActivity
```

它回答：

```text
用户真正目标是什么？
当前有哪些可用 Skill？
哪个 Skill 最适合？
Skill 需要哪些输入？
用户已经提供了哪些输入？
缺哪些输入？
缺失输入应该追问，还是推荐默认值？
默认值推荐依据是什么？
下一步是 ask_user、create_plan_card、invoke_capability、refuse_or_explain，还是 cancel？
```

V2.4 的核心产物是 `ActionPlan` 和 `PlannerTrace`。

`PlanCard` 只是 `ActionPlan` 的一种 UI 投射：

```text
ActionPlan -> PlanCard
ActionPlan -> chat confirmation
ActionPlan -> API JSON
ActionPlan -> CLI confirmation text
```

因此 `PlanCard` 不得成为 Planner 的内部核心模型。

## 3. 与 V2.1~V2.3 的关系

V2.4 不是推翻 V2.1~V2.3，而是把它们组织起来：

```text
ConversationContext
  -> TurnInterpreter                 # V2.2
  -> TargetResolver                  # V2.3
  -> OperationInterpreter            # V2.3
  -> SkillCatalog / CompiledSkill    # V2.1
  -> AgentMemorySnapshot             # AgentState / Plan / History
  -> AgentPlanner                    # V2.4
  -> ActionPlan + PlannerTrace       # V2.4
  -> Decision / PlanCard / CapabilityCall
```

职责边界：

| 层 | 回答的问题 | 不负责 |
| --- | --- | --- |
| V2.1 Skill Contract | 这个 Skill 能不能被调用，输入契约是什么，版本如何固定 | 不判断用户这一轮想干什么 |
| V2.2 Conversation Semantics | 用户这一轮在当前上下文里是什么动作 | 不做对象能力调用，不生成通用 Planner Trace |
| V2.3 Object/Capability Semantics | 动作作用于哪个对象，允许什么 Capability | 不做全局输入规划和默认值推荐 |
| V2.4 Agent Planner | 综合上下文、契约、对象、能力和记忆生成 ActionPlan | 不绕过 contract，不直接执行 workflow validator 外的副作用 |

## 4. Agent 公式

```text
Agent = Reasoning + Planning + Memory + Tools
```

DreamAI 对应关系：

| Agent 组成 | DreamAI 对应 |
| --- | --- |
| Reasoning | `TurnInterpreter`、`OperationInterpreter`、future LLM Interpreter |
| Planning | `AgentPlanner`、`InputPlanningPolicy`、`ActionPlan`、`PlannerTrace` |
| Memory | `AgentState`、`PendingInteraction`、`CurrentPlan`、`TaskLinks`、conversation history |
| Tools | Skill、Workflow、Capability、Engine Task、Review Gate |

V2.1~V2.3 已经分别补了 Tools、Reasoning/Memory 的关键部分和 Capability 空间。V2.4 正式补 Planning。

## 5. 白盒感来自 PlannerTrace

V2.4 可以提供“看起来像 Thought”的白盒体验，但展示对象必须是结构化 `PlannerTrace` 的用户可见摘要，而不是原始 Chain-of-Thought。

允许：

- 展示真实 Planner 事件。
- 展示真实 Skill 观察。
- 展示真实缺参判断。
- 展示真实 Assumption 和 Decision。
- 展示真实 Engine / Workflow / Capability Observation。
- 把结构化 Trace 转成用户可见 Planning Summary。

禁止：

- 流式输出原始 CoT。
- 展示大模型脑内独白。
- 为了显得聪明编造不存在的 Thought。
- 把原始 LLM 输出直接当业务状态。
- 把完整 Trace 直接塞进普通 message 文本导致消息流膨胀。

## 6. 三层可见过程

### 6.1 Planner Activity

Planner Activity 展示 Agent 正在如何规划。它来自 `PlannerTrace` 的摘要，不是原始 Thought。

示例：

```text
识别目标：赛博朋克视频
匹配能力：视频生成
检查输入：缺少音乐风格
自动推荐：电子重低音 / Synthwave
生成方案
```

推荐复用现有 conversation WS 的 message upsert 机制：

```text
PlannerActivity = 一条可变 message
后端用 message.id upsert
客户端同 id replace
```

不得新增 token 级 Thought stream。

### 6.2 Tool / Capability Trace

Tool / Capability Trace 展示 Go 后端真实执行动作。

示例：

```text
[Skill] video_gen
[Plan] 生成视频方案
[Workflow] 创建任务
[Prompt Compiler] 生成分镜描述
[Image Generation] 生成第 1/9 张
[Video Generation] 生成第 1/8 段
[FFmpeg] 合成成片
```

这些事件必须来自真实 Engine / Workflow / Capability 事件，不得由 Planner 编造。

### 6.3 User Decision Points

结构化卡片仍然是用户决策边界：

```text
PlanCard   -> 确认方案
ReviewCard -> 确认脚本 / 确认画面
ResultCard -> 查看成片 / 再来一版 / 改一下
```

`PlannerTrace` 不替代卡片，只解释为什么生成这些卡片。

## 7. 研发红线

### 7.1 禁止原始 CoT Streaming

禁止将原始 CoT / internal reasoning 以 token streaming 形式推送给客户端。

允许流式或 upsert 展示用户可见 Planning Summary，但 Summary 必须由结构化 `PlannerTrace` 生成。

### 7.2 禁止持久化原始 CoT

禁止将大模型未经整理的原始思维链写入业务日志、ELK、文件日志或数据库。

调试只能记录：

- 输入摘要。
- 输出结构化结果。
- 错误码。
- 耗时。
- 校验失败原因。
- `trace_id`。

### 7.3 原始 LLM 输出不得直接成为业务事实

原始 LLM 输出不能直接驱动状态机、PlanCard 或 CapabilityCall。

只有经过以下步骤后的结果才能进入业务状态：

```text
structured parsing
  -> Skill Contract validation
  -> Planning Policy validation
  -> Validator validation
  -> ActionPlan / PlanCard / CapabilityCall
```

### 7.4 禁止绕过 Skill Contract

V2.1 Skill Contract 是系统硬约束。Planner 没有权力绕过 required input，也不能绕过 workflow validator。

Planner 只能决定缺失输入的处理策略：

- `ask_user`
- `creative_default`
- `system_default`
- `cost_affecting_default`
- `requires_asset`

最终输出必须通过 Contract Validator。

### 7.5 禁止假 Trace

用户可见的每一条规划过程，都必须追溯到真实 Planner event、Assumption、Decision 或 Engine Observation。

禁止为了制造高级感编造不存在的 Trace。

## 8. 第一版运行边界

V2.4 第一版不做自由工具调用，不做 MCP Server，不做跨 Skill 自动组合，不做长时间 autonomous loop。

第一版 Planner 只在受控输入上规划：

```text
ConversationContext
TurnInterpretation
TargetResolution
OperationIntent
CompiledSkill catalog
Agent memory snapshot
```

第一版可输出的 `NextAction`：

- `ask_user`
- `create_plan_card`
- `invoke_capability`
- `refuse_or_explain`
- `cancel`

其中 `invoke_capability` 仅用于 V2.3 已注册并通过 policy 的能力；不允许 Planner 自由发现外部工具。

## 9. 关键评审问题

后续文档必须逐项回答：

- `PlannerInput` 如何在 runtime 中拼装？
- `SkillCatalog` 是启动时加载，还是运行时解析？
- 多个 Skill 同时匹配时如何仲裁？
- `creative_default` 是否使用 LLM？如果使用，P99 如何控制在 1 秒以内？
- `Assumption confidence` 低于多少必须 confirm？
- 用户否定推荐值时，`Assumption` 如何 override？
- `PlannerTrace` 如何存储、清理、TTL？
- `PlannerActivity` 如何通过 WS `message.id` upsert 渲染？
- LLM 输出脏 `ActionPlan` 时，Validator 如何拦截和降级？
- V2.2 的“修改 / 取消”如何避免误触发新一轮 Planner？
- 第一版不自由工具调用，如何为未来 LLM Planner 预留接口？
- `out_of_scope` 如“今天天气”如何锚定回当前任务上下文？
- V2.1~V2.3 老数据如何兼容？是否需要 migration？
- 如何用 Mock Planner 输出做自动化测试？
- 如何保证 `short_drama` 闭环零回退？

## 10. 首轮裁决

V2.4 可以开始文档设计。

但必须按评审门推进：

```text
README + 00 + 01
  -> 首轮评审
  -> 02~10 详细设计
  -> 整体评审
  -> 才允许进入业务实现
```

在整体评审通过前，任何 Go 业务实现都不进入 V2.4 范围。
