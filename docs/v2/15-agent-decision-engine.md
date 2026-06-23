# 15 · AgentRuntime Decision Engine（Agent 的大脑）

> 到这一步，「**存什么 / 怎么存 / 怎么同步 / 怎么恢复**」都已锁定（09 API / 10 Manifest / 11 State / 12 Data / 14 Repository）。唯一还没落地的，是真正决定产品体验的东西：
>
> > **Agent 收到一句「帮我做个短剧」，到底如何一步步决策——识别意图、选 Skill、补槽位、生成 Plan、启动 Workflow。**
>
> 这就是 `AgentRuntime.Decide()`。它不是 Conversation，不是 Repository，不是 DDL——是 V2 的大脑。本文给它的精确契约。[04](04-agent-runtime.md) 讲「为什么」，本文讲「**Decide 到底怎么算**」。

## 1. 定位：它决定，但不持久化、不发副作用

```text
            ┌───────────────────────────────────────────────┐
 用户输入 ──▶│  AgentRuntime.Decide(ctx, state, input)        │──▶ AgentDecision
            │  · 纯读：可调 LLM / RAG / Skill 仓库           │   （要追加哪些消息、
            │  · 不写库、不建任务、不发通知                   │     推进到哪个 stage、
            └───────────────────────────────────────────────┘     是否建 Plan / 启动 Skill）
                                  │
                                  ▼
            ┌───────────────────────────────────────────────┐
            │  ConversationService + UoW（[14 §4.1]）        │  ← 唯一的「写」：原子持久化 decision
            │  Outbox（[14 §4.2]）                           │  ← 唯一的「副作用」：Post-Commit 建任务
            └───────────────────────────────────────────────┘
```

**铁律**：`Decide` 对数据库**零副作用**。它读 `state`，读外部（LLM/RAG/Manifest），**产出一份「要做什么」的声明**（`AgentDecision`），由 service 通过 UoW 落库、由 Outbox 投递副作用。
这正是 [11 §8](11-agent-state-machine.md#8-恢复语义过一天回来)「无状态处理器 + rehydrate」能成立的根因，也让 Decide **可单测**（注入假 ports，给定 `state+input` 断言 `decision`）。

> 引擎事件（task 进度/完成）**不走 Decide**，走对称的 `AgentObserver.Translate`（[14 §5](14-repository-contracts.md#5-agentobserver--translator红线-3-的落点)）。Decide 管**入站用户回合**，Observer 管**引擎回合**。§9 说明两者关系。

## 2. 签名与契约

```go
// Decide 计算「这一回合该怎么走」。datastore-effect-free。
func (rt *AgentRuntime) Decide(ctx context.Context, state domain.AgentState, input Input) (AgentDecision, error)

type Input struct {
    Kind        InputKind          // user_message | signal
    Message     *UserMessage       // Kind=user_message
    Signal      *Signal            // Kind=signal（agent 归宿的卡片回应，如 confirm_plan / answer_slot）
}
```

契约：
- **纯读**：只通过注入的 ports（§6）访问外部；不调用任何 `*Repository` 的写方法。
- **确定性（模 LLM）**：给定相同 `state`、`input` 与**相同 ports 返回**，`Decide` 必产出相同 `AgentDecision`。不确定性被收敛在 ports 内部（LLM 抽样）。
- **全量推进**：`AgentDecision.NextState` 是推进后的**完整** AgentState（含 stage/slots/current_plan_id），供 CAS 整体写入。
- **失败安全**：ports 报错（LLM 超时等）→ Decide MUST 返回**可降级的 decision**（如「我没太懂，能再说一遍吗」），而非 error 让整轮失败（§10）。

## 3. AgentDecision 结构

```go
type AgentDecision struct {
    NextState domain.AgentState   // 推进后的完整状态（[11 §2]）
    Outbound  []domain.Message    // 追加的 agent/system 消息（clarify/plan_card/text/error_card...）
    NewPlan   *domain.Plan        // 进入 planning/confirming 时生成（snapshot 槽位）
    Launch    *LaunchIntent       // 进入 executing 时：要 Post-Commit 创建的任务意图（→ Outbox）
    SideEffects []SideEffect      // 其他出箱副作用（通知/审计…，一期通常空）
}

// LaunchIntent 一次 Skill 启动的全部信息（交给 Outbox 的 create_task 投递）
type LaunchIntent struct {
    SkillKey string                 // 选定的 Skill
    Source   SkillSource            // workflow_name 或 route_key+mode_key（[10 §3]）
    Input    map[string]any         // 由 slots 经 maps_to 组装的 workflow 输入（[10 §5.3]）
    PlanID   int64
    Fork     *ForkSpec              // 迭代修改时：基于哪个 task、patch 哪些节点（[04 §7]）
}
```

> `Decide` 只把 `Launch` 放进 decision；**真正建任务由 Outbox Worker 在事务提交后做**（[14 §4.2]）——大脑只下指令，不直接拨电话。

## 4. 回合类型路由（Decide 的第一步）

Decide 先根据**当前 stage + 输入**判定「这是哪种回合」，再分派。这张表就是 Agent 的反射弧：

| 当前 stage | 输入 | 回合类型 | 处理 |
|-----------|------|---------|------|
| `idle` | user_message | 新目标 / 闲聊 | `coldStart` / `smalltalk` |
| `collecting` | user_message | 补充信息 | `extractAndRecheck` |
| `awaiting_user` | signal `answer_slot` / user_message | 答槽 | 合并槽位 → `recheck` |
| `awaiting_user` | user_message（明显跑题） | 换目标 | `coldStart`（重分类，清旧槽） |
| `confirming` | signal `confirm_plan` | 确认方案 | `launch` |
| `confirming` | signal/msg 改参数 | 改方案 | `replan` |
| `confirming` | user_message「取消」 | 取消 | → `idle`/`collecting` |
| `executing` | user_message | 执行中插话 | `smalltalk` 或排队「改」（不打断当前任务） |
| `reviewing` | signal | **不经 Decide** | 该 signal `routed_to=engine`，由 PostSignal→`await.HandleSignal` 处理（[09 §2.7]） |
| `completed` | user_message | 改 / 新目标 | `modifyPath`（[04 §7] fork）/ `coldStart` |
| `failed` | user_message | 重试 / 新目标 | `retryPath` / `coldStart` |

> `reviewing` 那行是关键边界：审核闸门的确认**回流引擎**，不是会话决策。Decide 不碰它。

## 5. 冷启动主流水线（架构师给的那张图）

`coldStart`（idle/completed/failed 收到新目标）按固定顺序走，每步用哪个 port、读哪份 Manifest 字段、产出什么——全部钉死：

```text
用户消息
  │
  ▼ ① Intent Recognition         port: IntentClassifier   读: Manifest.intent/examples（few-shot）
  │     → intent | chitchat | unknown
  ▼ ② Skill Selection            port: SkillSelector       读: Manifest.description/intent
  │     → 命中 1 个 / 多个(消歧) / 0 个(澄清能力)
  ▼ ③ Slot Filling               port: SlotExtractor       读: Manifest.slots（type/maps_to/extract_hint）
  │     → collected{} + 三段式：抽取 → 默认 → 缺口（[10 §5.2]）
  ▼ ④ Need Clarification?        规则: required && 无 default && 抽不到
  │     是 → 出 clarify 卡，stage→awaiting_user，结束本轮
  │     否 ↓
  ▼ ⑤ Generate Plan              port: Quoter              snapshot 槽位 → Plan + estimated_cost
  │
  ▼ ⑥ Need Confirmation?         读: Manifest.cost.needs_plan_confirmation
  │     是 → 出 plan_card，stage→confirming，结束本轮
  │     否(轻量 Skill) ↓
  ▼ ⑦ Launch Skill               → AgentDecision.Launch（slots 经 maps_to 组装 Input），stage→executing
  │
  ⋯ 之后：引擎跑 → Observer 翻译进度（§9）→ 审核闸门 reviewing → completed
```

### 各步产出（决策视角）
| 步 | 命中分支 | NextState.Stage | Outbound | 其他 |
|----|---------|-----------------|----------|------|
| ① | chitchat | idle | text（能力介绍/引导） | — |
| ① | unknown | idle | text（请说得具体些 + 快捷建议） | — |
| ② | 多个候选 | collecting | clarify（「你是想做 A 还是 B？」） | pending_message_id |
| ② | 0 候选 | idle | text（暂不支持，给相近能力） | — |
| ④ | 缺必填槽 | awaiting_user | clarify（带 options/批量同组问） | pending_message_id |
| ⑥ | 需确认 | confirming | plan_card（带 Quote/可调整项） | NewPlan |
| ⑦ | 直跑/确认后 | executing | text（「好的，开始啦」可选） | Launch + NewPlan(confirmed) |

## 6. 能力端口（Ports）—— 为什么注入

把「需要外部智能/数据」的点抽成接口，注入实现。好处：**可测**（注入 fake）、**可换**（LLM ↔ 规则 ↔ 混合）、**可灰度**（某 Skill 用规则、某 Skill 用 LLM）。

```go
type IntentClassifier interface { // 通常 LLM；few-shot 来自各 Manifest.examples
    Classify(ctx context.Context, c ConversationContext, msg UserMessage) (IntentResult, error)
}
type SkillSelector interface {     // 规则(intent 精确匹配)优先，歧义时 LLM 兜底
    Select(ctx context.Context, intent string, signals Signals) (SkillChoice, error)
}
type SlotExtractor interface {     // LLM 抽参 + Manifest 校验/默认
    Extract(ctx context.Context, skill SkillManifest, msg UserMessage, known Slots) (Slots, error)
}
type Quoter interface {            // 复用现有 Quote 接口（[10 §4]）
    Quote(ctx context.Context, skill SkillManifest, slots Slots) (points int64, err error)
}
type Retriever interface {         // 二期 RAG（[07]）；一期注入 no-op 实现
    Recall(ctx context.Context, intent string, c ConversationContext) ([]Memory, error)
}
```

> ⚠️ **实现校正（2026-06，以代码为准）**：端口理念与方向**已落地且一致**，签名做了简化合并：
> - `IntentClassifier.Classify(ctx, c *ConversationContext)`、`SlotExtractor.Extract(ctx, c *ConversationContext, m *Manifest)`——`UserMessage` 已折叠进 `ConversationContext.Input`（= AgentState + 最近窗口 + 当前输入 + Plan）。位置：`ai-engine/agent/runtime/ports.go`。
> - **已实现**：规则版 `RuleIntentClassifier` / `RuleSlotExtractor`（带上下文）；`HybridIntentClassifier` 外壳（LLM fallback **默认关**，`hybrid_intent.go`）。
> - **未实现 / 延后**：`SkillSelector`（当前单 Skill，`selectSkill` 直接按 intent 取唯一 Manifest）、`Quoter`（估价为占位常量）、`Retriever`（RAG 未接）。详见 [17](17-implementation-status-and-roadmap.md) §2。

- **一期不接 RAG**：`Retriever` 注入空实现，Decide 逻辑不变（[07](07-rag-roadmap.md) 的「RAG 是 Agent Runtime 的增强而非主架构」在此体现——它只是给 ③④⑤ 多一份上下文，缺了也能跑）。
- `SkillManifest` 由 `SkillRegistry` 提供（[10](10-skill-manifest-spec.md)）；Decide 不读 DAG，只读 Manifest。

## 7. Decide 骨架（编排）

```go
func (rt *AgentRuntime) Decide(ctx context.Context, st domain.AgentState, in Input) (AgentDecision, error) {
    switch rt.routeTurn(st, in) {          // §4 路由表
    case TurnNewGoal:      return rt.coldStart(ctx, st, in.Message)
    case TurnAnswerSlot:   return rt.recheckSlots(ctx, st, in)        // 合并槽位 → ④ 重判
    case TurnConfirmPlan:  return rt.launch(ctx, st)                  // ⑦
    case TurnModifyPlan:   return rt.replan(ctx, st, in)             // 回 ⑤
    case TurnModifyResult: return rt.modifyPath(ctx, st, in.Message)  // completed→fork（[04 §7]）
    case TurnRetry:        return rt.retryPath(ctx, st, in)
    case TurnCancel:       return rt.cancel(st)
    case TurnSmalltalk:    return rt.smalltalk(st, in)               // 不改主 stage
    default:               return rt.fallback(st)                     // §10 兜底
    }
}

func (rt *AgentRuntime) coldStart(ctx context.Context, st domain.AgentState, msg *UserMessage) (AgentDecision, error) {
    intent := rt.intent.Classify(ctx, ctxOf(st), *msg)        // ①
    if intent.IsChitchat() || intent.IsUnknown() { return rt.replyGuidance(st, intent) }
    choice := rt.selector.Select(ctx, intent.Intent, signalsOf(st)) // ②
    if choice.NeedsDisambiguation() { return rt.askWhichSkill(st, choice) }
    if choice.Empty()               { return rt.replyUnsupported(st, intent) }

    skill := rt.skills.Get(choice.SkillKey)
    slots := rt.extractor.Extract(ctx, skill, *msg, nil)       // ③（三段式：抽取/默认/缺口）
    if miss := skill.MissingRequired(slots); len(miss) > 0 {   // ④
        return rt.askClarify(st, skill, slots, miss)           // stage→awaiting_user
    }
    plan := buildPlan(skill, slots, rt.quoter.Quote(ctx, skill, slots)) // ⑤
    if skill.NeedsPlanConfirmation() {                          // ⑥
        return rt.presentPlan(st, skill, plan)                 // stage→confirming
    }
    return rt.launchWith(st, skill, plan)                       // ⑦ stage→executing + Launch
}
```

> 每个 `rt.ask*/present*/launch*` 只是**构造 `AgentDecision`**（拼 NextState + Outbound + 可选 NewPlan/Launch），不碰库。

## 8. 迭代修改路径（completed → 改 → fork）

「第二幕改成夜晚」这类是 Agent 的高价值场景（[01](01-competitive-analysis.md) 竞品最难补的一块）。`modifyPath`：

```text
completed + 用户「第N幕改成…」
  │ ① 识别为修改意图（不是新目标）：参照 current_plan + Manifest.iteration.intent_hint（[10 §8]）
  │ ② 定位作用域：scope=node(target_node)/whole；算 PatchPreview（重跑哪些节点 + 代价）
  ▼ 出「修改预览卡」(plan_card 变体)，stage→confirming
确认 → AgentDecision.Launch{Fork: {from_task, patch}} → Outbox 建 fork 任务（复用引擎 fork/patch）
```

要点：修改路径**复用引擎既有 fork/patch**，Decide 只负责「把口语映射成 patch 作用域 + 报价 + 出确认卡」。这是「不改引擎」红线下，迭代能力几乎零成本落地的体现（[02](02-architecture-overview.md)）。

## 9. 与 Observer 的对称（引擎回合）

Decide 与 Observer 是一对镜像，都产出「要追加的消息 + stage 推进」，区别只在触发源：

| | 触发 | 输入 | 翻译依据 | 产出 |
|---|------|------|---------|------|
| `Decide` | 用户回合 | UserMessage/Signal | Manifest intent/slots/cost | AgentDecision（含 Launch） |
| `Observer.Translate` | 引擎回合 | TaskEvent | Manifest **stages/gates**（[10 §6/§7]） | 进度/审核/结果消息 + stage（executing→reviewing/completed/failed） |

二者都经 UoW 落库（[14](14-repository-contracts.md)）。**进度是翻译不是透传**：未在 Manifest.stages 白名单的事件不出消息（[04 §6](04-agent-runtime.md#6-进度翻译translate-dont-forward)）。

## 10. LLM 用量与护栏

- **结构化输出**：①③ 用 LLM 时要求 JSON schema 输出（intent 枚举、slot 键值），解析失败→当作「没抽到」走澄清，绝不让脏输出污染 Launch。
- **失败兜底**：任一 port 报错/超时→返回降级 decision（追问或安全默认），stage 不前进，**绝不**在信息不全时擅自 Launch。
- **不可幻觉建任务**：Launch 的前置硬条件——必填槽齐备 **且**（需确认时）用户已 `confirm_plan`。这把「LLM 瞎编参数直接烧钱」挡在门外（[04 §10](04-agent-runtime.md#10-llm-用量与护栏)）。
- **成本上限**：每个用户回合的 LLM 调用次数设硬上限（建议 ≤2：一次 intent+slot 合并抽取，一次消歧/澄清话术生成）；超限走规则兜底。
- **报价前置**：昂贵 Skill 必经 `confirming` + Quote 展示，确认才扣分建任务（与现有计费一致）。

## 11. 可测性（这是把大脑写对的保证）

Decide 是纯函数式编排 → 单测注入 fake ports：

```go
// 给定 state + input + ports 返回，断言 decision
dec, _ := rt.Decide(ctx, stateCollectingShortDrama, answerSlot("characters", 2))
assert.Equal(t, StagePlanning_or_Confirming, dec.NextState.Stage)
assert.Len(t, dec.Outbound, 1)              // plan_card
assert.NotNil(t, dec.NewPlan)
assert.Nil(t, dec.Launch)                    // 未确认前不启动
```

建议：对每个 Skill 的冷启动/缺槽/确认/修改各写一条「state→decision」快照测试（呼应现有 `dsl_graph_validity_test.go` 的工程习惯）。

## 12. 一期边界（明确不做）
- **单 Skill / 回合**：一轮只推进一个目标；不做 multi-agent、并行多 Skill 编排。
- **不接 RAG**：`Retriever` 空实现（[07](07-rag-roadmap.md) 二期）。
- **不自动改作品**：修改必须用户发起 + 确认，Agent 不主动「优化」成品。
- **不做长期记忆/跨会话学习**：决策只依赖当前 `state` + 本会话上下文。
- **消歧深度有限**：Skill 选择最多一次澄清；再不明确则给快捷建议引导。

## 13. Worked traces（决策视角）

**A. 冷启动短剧**
```text
state=idle  input="帮我做个一分钟都市爱情短剧"
① intent=short_drama  ② skill=short_drama(唯一命中)
③ extract→{duration:60, style:urban_romance}; default chars=2; 缺 idea
④ 缺必填 idea → decision{ NextState.stage=awaiting_user, Outbound=[clarify "想讲个什么故事？"] }
```
**B. 二义性消歧**
```text
state=idle  input="用这几张图做个视频"
① intent=image_to_video?  ② selector 命中多个(图生视频 / 带货视频)
→ decision{ stage=collecting, Outbound=[clarify "是想做带卖点的带货视频，还是让图片动起来？"] }
```
**C. 迭代改第二幕**
```text
state=completed(plan v1, task 70010)  input="第二幕改成夜晚"
routeTurn→TurnModifyResult; 比对 Manifest.iteration(intent_hint 命中 scope=node, target_node=shots)
PatchPreview: 重跑 1 个分镜, 约 Y 积分
→ decision{ stage=confirming, Outbound=[plan_card「修改预览」], NewPlan=v2(revised) }
确认 → decision{ stage=executing, Launch{Fork:{from:70010, patch:shots[1].time=night}} }
```

## 14. 落地清单
1. `ai-engine/agent/service/runtime.go`：`AgentRuntime` + `Decide` + 路由表（§4）+ coldStart/recheck/launch/modifyPath（§5/§7/§8）。
2. `ai-engine/agent/service/ports/`：5 个 port 接口（§6）；一期实现：`IntentClassifier`/`SlotExtractor` 走 LLM，`SkillSelector` 规则优先，`Quoter` 接现有 Quote，`Retriever` no-op。
3. `AgentDecision` / `LaunchIntent` / `Input` 类型（`agent/domain`）。
4. 决策快照测试：每 Skill × {冷启动/缺槽/确认/修改}（§11）。
5. 与 [14](14-repository-contracts.md) 对接：service 在事务外调 `Decide`，事务内落 `AgentDecision`，Launch 走 Outbox。

---

返回：[v2/README.md](README.md) · 相关：[04 Agent Runtime（为什么）](04-agent-runtime.md) · [10 Skill Manifest](10-skill-manifest-spec.md) · [11 状态机](11-agent-state-machine.md) · [14 Repository 契约](14-repository-contracts.md)
