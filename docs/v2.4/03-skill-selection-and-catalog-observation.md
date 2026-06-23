# 03 · Skill Selection and Catalog Observation

## 1. 问题定位

Planner 不能靠散落关键词判断要调用哪个 Skill。

V2.1 已经把 Skill 接入推进到 contract / manifest / compiled skill 的方向。V2.4 必须在这个基础上做 Skill 观察和选择：

```text
Planner 观察 CompiledSkill catalog
  -> 过滤不可用 Skill
  -> 根据回合语义、目标对象、操作意图、用户输入、历史状态生成候选
  -> 多 Skill 仲裁
  -> 记录 Observation / Decision
  -> 输出 ActionPlan.SkillKey
```

目标不是让 Planner 自由发现外部工具，而是让它在已注册、已编译、已校验的 Skill 空间内做可解释选择。

## 2. Catalog 权威来源

Planner 观察的是 `CompiledSkill` 快照。

建议模型：

```go
type SkillCatalogSnapshot struct {
    Version      string
    LoadedAt     time.Time
    Skills       []CompiledSkill
    Disabled     []DisabledSkillSummary
}
```

`CompiledSkill` 至少包含：

```go
type CompiledSkill struct {
    SkillKey           string
    DisplayName        string
    RouteKey           string
    ModeKey            string
    ToolModeVersionID  int64
    ContractSchemaHash string
    InputContract      InputContract
    PlanningPolicy     PlanningPolicy
    IntentHints        SkillIntentHints
    ObjectBindings     []ObjectBinding
    CapabilityBindings []CapabilityBinding
    Examples           []SkillExample
    Status             SkillStatus
}
```

权威边界：

- Skill 是否存在、active version、contract hash 由 V2.1 resolver / compiler 决定。
- Planner 不直接读取 DB 自行拼 Skill。
- Planner 不使用未编译成功的 Skill。
- disabled Skill 不进入可执行候选，但可以进入 Trace 作为不可用 observation。

## 3. Catalog 生命周期

第一版建议：

```text
服务启动
  -> Load embedded manifests
  -> Resolve active ToolModeVersion
  -> Compile Skill Contract
  -> Compile PlanningPolicy
  -> Register SkillCatalogSnapshot in memory

运行时
  -> AgentRuntime 读取 immutable snapshot
  -> Planner 基于 snapshot 选择 Skill
```

后续支持 admin 发布新 active version 时：

```text
active version changed
  -> recompile affected Skill
  -> create new SkillCatalogSnapshot
  -> new plans use new snapshot
  -> existing ActionPlan keeps ToolModeVersionID + ContractSchemaHash
```

如果已出 PlanCard 后 active version 变化，confirm 时必须触发版本固定检查，不能用新 contract 静默执行旧 Plan。

## 4. 什么时候进入 Skill Selection

并非每个回合都需要重新选择 Skill。

| 回合语义 | Skill selection 行为 |
| --- | --- |
| `start_goal` | 进入新目标 Skill selection |
| `answer_question` | 延续 pending 的 Skill，不重新全局选择 |
| `request_modification` | 优先使用 V2.3 target / operation / current plan skill |
| `provide_modification` | 延续当前 review / plan / result 对象，不当作新 Skill |
| `cancel` | 不做 Skill selection，进入 cancel action |
| `regenerate` / 再来一版 | 继承 current plan / result skill，必要时进入 regenerate capability |
| `chitchat` | 不做 Skill selection |
| `out_of_scope` | 锚回当前任务或拒绝解释，不创建无关 Skill plan |

只有 V2.2/V2.3 判断为新目标或确实需要切换能力时，才做全局 Skill selection。

如果当前存在 blocking pending、reviewing 或 executing task，而用户输入命中新 Skill，这属于可能的 goal switch，不能静默切换。Planner 必须先询问用户是否切换任务或保留当前任务。

示例：

```text
当前短剧还在等待你确认分镜。
用户: 我想做一张图

Planner:
NextAction = ask_user
Question = 当前短剧还在等待你确认分镜。你是想先暂停这个短剧，改做图片，还是继续处理当前短剧？
```

这条规则用于保护 V2.2/V2.3 的 pending、review 和 task 生命周期，避免一个新 Skill 命中直接打断当前闭环。

## 5. 候选过滤

候选生成前先过滤：

```text
all compiled skills
  -> status enabled
  -> contract compiled
  -> planning policy compiled
  -> mode version available
  -> user/context allowed
  -> object binding allowed
  -> operation binding allowed
```

过滤失败必须记录 trace：

```text
Observation: skill image_to_image skipped because source asset required and no asset context
Observation: skill video_gen disabled because contract compile failed
Observation: skill short_drama eligible by current plan skill
```

过滤不是仲裁。过滤只决定“能不能进候选池”。

## 6. 匹配信号

Skill selection 使用多个结构化信号，不依赖单个关键词。

| 信号 | 来源 | 示例 |
| --- | --- | --- |
| explicit skill mention | 当前用户输入 / TurnInterpretation | “做图”、“生成视频”、“短剧” |
| goal type | TurnInterpretation | `create_image` / `create_video` / `create_short_drama` |
| operation | V2.3 OperationIntent | `revise` / `regenerate` / `confirm` |
| target object | V2.3 TargetResolution | review artifact / result image / current plan |
| active skill | AgentState / CurrentPlan | 当前 plan 是 `short_drama` |
| provided assets | current turn / ActiveObjects | 用户上传了图片 |
| slot compatibility | InputContract | 用户提供字段能映射到 Skill slot |
| examples | CompiledSkill examples | manifest examples 命中 |
| negative examples | CompiledSkill intent hints | “天气”不是创作 Skill |

第一版可以使用确定性 scoring；未来可接 LLM matcher，但 LLM 输出也只能作为候选信号，不能绕过过滤和仲裁规则。

## 7. CandidateScore

建议模型：

```go
type SkillCandidate struct {
    SkillKey     string
    Score        float64
    Reasons      []SkillMatchReason
    Penalties    []SkillMatchPenalty
    RequiredGaps []MissingInputPreview
}

type SkillSelectionResult struct {
    SelectedSkillKey string
    Candidates       []SkillCandidate
    Ambiguous        bool
    DecisionReason   string
}
```

即使最终 `ActionPlan` 只使用 `SelectedSkillKey`，PlannerTrace 也必须保留候选集摘要，用来解释为什么选中某个 Skill、为什么没有选其它 Skill、以及为什么需要追问消歧。

推荐评分：

| 信号 | 建议权重 |
| --- | --- |
| explicit skill mention | +0.35 |
| goal type exact match | +0.30 |
| target object binding match | +0.25 |
| operation binding match | +0.20 |
| active current plan skill continuation | +0.35 |
| asset compatibility | +0.15 |
| slot compatibility | +0.10 |
| manifest example match | +0.10 |
| negative hint match | -0.40 |
| required asset missing | -0.10, 不直接淘汰 |
| disabled / contract invalid | ineligible |

权重是第一版建议，不是业务事实。必须用回归矩阵锁定行为，而不是让分数无测试地漂移。

## 8. 仲裁规则

多 Skill 同时匹配时按以下顺序裁决。

### 8.1 当前上下文延续优先

如果 V2.2/V2.3 判断用户是在回答追问、修改、确认、取消、再来一版，则优先延续当前对象所属 Skill。

示例：

```text
Agent: 你想要什么风格？
用户: 赛博朋克
```

即使 `video_gen` 和 `image_gen` 都能匹配“赛博朋克”，也应延续 pending 的 Skill。

### 8.2 对象能力优先

如果目标对象明确，例如当前等待审核的 `prompt_review_card`，则由 V2.3 Capability binding 决定可执行能力。

示例：

```text
用户: 把风格改成电影感
Target = current review artifact
Operation = revise
```

应走 `short_drama` review revision by fork，不重新选择 text/image/video Skill。

### 8.3 显式 Skill 优先

如果用户明确说“做一张图”、“生成视频”、“做短剧”，且没有 pending / target continuation，显式 Skill 类别优先。

但显式 Skill 仍必须通过 eligibility 和 contract 校验。

### 8.4 高置信单候选直接选择

如果最高候选明显领先，选择最高候选。

建议阈值：

```text
top.score >= 0.75
top.score - second.score >= 0.15
```

阈值需通过 `10-regression-matrix.md` 最终固化。

### 8.5 分数接近则追问消歧

如果多个候选接近，不能随便选。

示例：

```text
用户: 帮我做一个赛博朋克
```

可能是图片、视频、短剧。若无更多上下文，应追问：

```text
你想做图片、视频，还是短剧？
```

输出：

```text
NextAction = ask_user
MissingInput = desired_skill_type
```

### 8.6 无候选则拒绝或锚回

如果没有可用 Skill：

- 当前有 active task 或 pending：锚回当前任务上下文。
- 没有 active task：解释当前不支持，并给出可用方向。

不得编造外部工具或自由调用。

## 9. SkillSelectionTrace

Skill selection 必须写入 `PlannerTrace`。

建议事件：

```go
type SkillSelectionObservation struct {
    CatalogVersion string
    CandidateCount int
    Candidates     []SkillCandidateSummary
    Selected       string
    Ambiguous      bool
    DecisionReason string
}
```

Trace 示例：

```text
Observation: catalog snapshot v2026-06-07 loaded with 5 enabled skills
Observation: user goal matched create_video
Observation: candidate video_gen score=0.86 because goal exact match
Observation: candidate short_drama score=0.52 because cyberpunk can be story theme but no drama intent
Decision: selected video_gen because top candidate exceeded threshold
```

Trace 不展示原始 CoT，也不把评分过程伪装成大模型脑内独白。

## 10. Disabled Skill 观察

disabled Skill 不应静默消失。Planner 可以记录：

```text
Observation: image_to_image exists but disabled because contract compile failed
```

用户可见摘要默认不展示内部 disabled 原因，除非用户请求或产品需要解释“为什么不能做”。

开发调试 Trace 可以关联 disabled reason：

- no active ToolModeVersion。
- multiple current versions。
- contract missing required source。
- planning policy compile failed。
- validator mismatch。

## 11. 与 InputPlanningPolicy 的关系

Skill selection 不要求所有 required input 已经满足。

它只需要判断：

```text
这个 Skill 是否最适合当前目标？
缺失输入是否可由 InputPlanningPolicy 处理？
```

示例：

```text
用户: 把这张改成动漫风
```

如果当前 turn 带图：

```text
image_to_image 高分
source_image 已满足
NextAction = create_plan_card
```

如果没图且无 ActiveObject：

```text
image_to_image 仍可被选中
InputPlanningPolicy 发现 source_image requires_asset
NextAction = ask_user
```

不得因为缺图就错误选择 text_to_image。

## 12. 与 out_of_scope 的关系

`out_of_scope` 不应触发新 Skill selection。

示例：

```text
当前正在等用户确认短剧脚本
用户: 今天天气怎么样？
```

V2.2 应识别为 out_of_scope / chitchat，V2.4 应锚回当前任务：

```text
NextAction = refuse_or_explain
Message = 我现在不能查询天气。当前短剧脚本还在等你确认，要继续修改或确认吗？
```

不得选择不存在的 weather Skill，也不得打断当前 review 状态。

## 13. 示例：赛博朋克视频

输入：

```text
帮我做一个赛博朋克视频
```

候选：

| Skill | 原因 | 分数 |
| --- | --- | --- |
| `video_gen` | explicit video + create_video goal | 0.9 |
| `short_drama` | cyberpunk 可作为剧情主题，但无短剧意图 | 0.45 |
| `text_to_image` | cyberpunk 可作为画面主题，但用户明确视频 | 0.35 |

裁决：

```text
Selected = video_gen
Decision = top candidate exact skill category
```

## 14. 示例：模糊创作目标

输入：

```text
帮我做一个赛博朋克
```

候选：

| Skill | 原因 |
| --- | --- |
| `text_to_image` | cyberpunk visual theme |
| `video_gen` | cyberpunk video theme |
| `short_drama` | cyberpunk story theme |

如果没有上下文偏向，输出：

```text
NextAction = ask_user
Question = 你想做图片、视频，还是短剧？
```

不要默认选择最熟悉的 `short_drama`。

## 15. 示例：Review 修改

当前：

```text
Stage = reviewing
ActiveObject = prompt_review_card
CurrentPlan.SkillKey = short_drama
```

用户：

```text
把风格改成电影感
```

裁决：

```text
No global skill reselection
Selected Skill = short_drama from target object
Operation = revise_review_by_fork
NextAction = invoke_capability or create replacement PlanCard according to V2.3 policy
```

这保证 V2.3 review revision by fork 闭环不回退。

## 16. LLM Matcher 边界

未来可以引入 LLM matcher，但第一版不依赖。

如果引入：

- LLM 只能输出结构化 candidate hints。
- LLM 不能返回可执行 SkillKey 后直接跳过过滤。
- LLM 不能发明 catalog 中不存在的 Skill。
- LLM 输出必须包含 confidence，但最终仲裁仍由 deterministic policy 完成。
- LLM 原始输出不持久化为业务事实。

## 17. 回归要求

必须覆盖：

- 明确视频目标选择 `video_gen`。
- 明确图片目标选择 `text_to_image`。
- 图生图缺图仍选择 `image_to_image` 并追问素材。
- 模糊“做一个赛博朋克”追问消歧。
- pending answer 不重新选择 Skill。
- review 修改不重新选择 Skill。
- cancel 不进入 Skill selection。
- out_of_scope 不选择无关 Skill。
- disabled Skill 不进入候选。
- 多 Skill 分数接近时不随机选择。
- `short_drama` 创建、确认、review 修改闭环零回退。

## 18. 裁决

V2.4 第一版采用受控 Skill selection：

- 只观察 compiled catalog。
- 只在新目标或明确切换能力时全局选择。
- 上下文延续和对象能力优先于关键词匹配。
- 多候选接近时追问消歧。
- 选择过程必须写入 `PlannerTrace`。
- 不做自由外部工具发现。
