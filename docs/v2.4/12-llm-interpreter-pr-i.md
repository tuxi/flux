# 12 · PR-I LLM Interpreter Helper（混合对话解释器）

> 状态：已实现并落地（`feat/agent-first-v2`）。
> 默认关闭：`ai_engine.agent_llm_interpreter.enabled=false` 时行为与接入前完全一致。

PR-I 在 V2.4 基座之上接入第一个 LLM 能力，但**严格受控**：LLM 只做语义解释（增强对当前回合的理解），不做执行。它不生成 `Decision` / `PlanCard` / `Plan` / `Task` / `CapabilityCall`，不绕过 Skill Contract、PlanningPolicy、Validator、确认门。

一句话：

```text
LLM 作为语义解释器，不作为自由执行器。
```

## 1. 为什么接在 TurnInterpreter fallback，而不是 IntentClassifier

任务初稿建议包装 `IntentClassifier`。但与 as-built 代码核对后，接入点改为 **TurnInterpreter fallback**，理由：

1. **主路径语义来源不同。** `agent_planner_enabled=true` 时主路径是 `respondPlanner`：
   - 技能/槽位/操作语义来自 `RuleTurnInterpreter`（产出 `TurnInterpretation`），喂给确定性 Planner；
   - 元意图（smalltalk/identity/help）才来自 `RuleIntentClassifier`。
   包装 `IntentClassifier` 只能改善元意图回复，**无法影响技能选择**——而 Logo / 图片这类"创作目标识别"恰恰发生在 Planner 的技能选择里。

2. **`IntentResult` 表达力不足。** 它只有 `Intent + Confidence`，无法承载 `goal_type` / `skill_hint` / `extracted_prompt` / `out_of_scope`。而 `TurnInterpretation` 是 Planner 真正消费的结构，能承载这些字段。

3. **文档早有定论。** `v2.2/05-llm-interpreter-roadmap.md` 明确：
   > 未来接 LLM 时，不应只替换 `IntentClassifier.Classify`；LLM 应接在 LLM TurnInterpreter fallback，输出统一的 `TurnInterpretation`。
   `v2.4/08-llm-planner-roadmap.md` 同样要求：schema parse → enum/catalog 校验 → 置信度阈值 → 确定性策略仲裁。

落地结构：

```text
respondPlanner
  ① RuleIntentClassifier 元意图门（LLM-free，不变）——高置信打招呼/能力询问/身份
  ② HybridTurnInterpreter.Interpret(once)
       RuleTurnInterpreter 高置信 -> TurnInterpretation
       Rule == ActUnknown         -> LLM helper -> 校验/映射 -> TurnInterpretation
  ③ LLM 产出的 meta/out_of_scope -> 文本回复（identity/help/smalltalk）或锚回当前任务的拒绝
  ④ buildPlannerInputFromInterpretation(同一份解释) -> deterministic Planner -> ActionPlan
       └ RefuseOrExplain 且是创作目标(GoalType) -> "能力接入中"清晰解释（非泛化拒绝）
  ⑤ decisionFromActionPlan（不变）
```

代码位置（`ai-engine/agent/runtime/`）：

- `llm_interpreter.go`：`TurnInterpreterHelper` 接口、`LLMInterpretInput/Result`、`LLMTurnInterpreterHelper`（走 `pkg/llm`，`JSONMode`，capability=`intent_extraction`，短 timeout）。
- `hybrid_turn_interpreter.go`：`HybridTurnInterpreter`（实现 `TurnInterpreter`）+ catalog 适配器。
- `runtime.go` / `planner_cutover.go` / `planner_shadow.go` / `dialogue_policy.go`：接线。
- composition root：`ai-engine/server/server.go`（仅在开关开启时 `SetTurnInterpreter(hybrid)`）。

## 2. LLM 仅在 `RuleTurnInterpreter` 返回 `ActUnknown` 时调用

`HybridTurnInterpreter.Interpret` 先跑规则：

```text
ruleIt := rule.Interpret(ctx, c)
if !enabled || helper == nil || ruleIt.Act != ActUnknown {
    return ruleIt   // 规则有任何可靠判断 -> 直接用，绝不调 LLM
}
```

含义：

- 所有控制/续接意图（cancel / confirm / regenerate / answer_question / request_modification / provide_modification）规则都会以非 unknown、较高置信返回，**在 LLM 之前短路**。
- 因此 short_drama 主链路、review 修改 by fork、override（"换成钢琴"）、再来一版、取消等路径**完全不经过 LLM**，零回退。
- LLM 的影响面被精确限定在"规则识别不出"的灰区（哦 / 天气 / 写小说 / Logo / 模糊创作目标）。
- 每回合最多 1 次 LLM 调用；shadow 评估仍走纯规则，不产生额外 LLM 开销。

## 3. 支持的 bounded acts

LLM 允许输出的 `dialogue_act` 是封闭枚举（`allowedLLMDialogueActs`）：

```text
smalltalk · identity · help · start_goal · answer_question
request_modification · provide_modification · regenerate · cancel
out_of_scope · unknown
```

但运行时**实际采纳**的只有一个更窄的白名单（`honoredLLMActs`）：

```text
smalltalk · identity · help · out_of_scope · start_goal
```

不在白名单内的 act（如 LLM 返回 regenerate/cancel/answer_question/provide_modification 等控制/续接类）一律**降级回规则结果**——这些只能由确定性规则产生，LLM 无权改写。

act → 运行时行为：

| act | 行为 |
|---|---|
| smalltalk / identity / help | 文本回复（persona 文案），不启动任何工作流 |
| out_of_scope | `plannerRefuseText`（按 stage 锚回 confirming/reviewing/completed 的当前任务） |
| start_goal | 进入确定性 Planner；`skill_hint`（经 catalog 校验）→ 技能选择，`extracted_prompt` → `user_prompt`，`goal_type` 用于"无对应 Skill 时"的清晰解释 |

## 4. 降级规则（任何不确定都回退到规则）

`HybridTurnInterpreter.mapResult` + helper 的失败处理保证：以下任意一种情况都**降级为规则结果**（绝不破坏基础体验）：

- LLM 调用失败 / 超时（context deadline）；
- 返回内容为空 / 非法 JSON / 缺 `dialogue_act`（parse 失败）；
- `dialogue_act` 不在允许枚举内；
- act 不在 honored 白名单内；
- `confidence < min_confidence`（默认 0.7）；
- `skill_hint` 不在 catalog（丢弃该 hint）；
- `start_goal` 既无有效 skill 又无 `goal_type`（无可执行目标）。

层级区分（重要）：

```text
LLM fail -> 回退规则         （本层，安全降级）
Planner fail -> 不允许静默回 legacy （未触碰，仍由 V2.4 cutover 保证）
```

## 5. 不记录 raw prompt / raw output / raw reasoning / CoT

- Prompt 边界：系统提示明确"你不是执行器 / 不能创建任务 / 不能调用工具 / 不能生成 PlanCard / 只输出 JSON / 禁止思维链"。
- 输入最小化：只传 `current_text / stage / current_skill_key / pending_kind / has_blocking_task / active_object_type / supported_skills`，**不传**完整会话原文、system prompt、secret、callback token。
- 只记录**结构化 trace**（`record()` 经 slog，字段：`helper_called / parse_status / validation_status / reason_code / confidence / latency_ms`）。
- **绝不**把 raw prompt / raw output / raw reasoning / CoT 写入 message、DB、ELK 或文件日志。

## 6. 当前已验证用例（单测，无网络）

`go test ./ai-engine/agent/runtime/...`，均通过：

- 解析层（`llm_interpreter_test.go`）：合法 JSON、代码块围栏、字段 trim、空内容、缺 act、非法 JSON；helper 成功 / 错误透传 / 超时。
- 混合层（`hybrid_turn_interpreter_test.go`）：规则高置信跳过 LLM、禁用跳过、smalltalk、out_of_scope 保留锚点 target、`is_out_of_scope` 覆盖 act、start_goal 合法 skill_hint、非法 skill_hint 保留 goal_type、空目标降级、非法 act 降级、低置信降级、非 honored act 降级、helper 错误回退、规则错误透传。
- 运行时层（`llm_interpreter_runtime_test.go`，planner 开启 + 注入 fake helper）：`哦`→smalltalk 文本无 plan；`可以写小说吗`→out_of_scope 拒绝无 plan；`生成一个极简 Logo 设计`→"接入中"清晰解释（非泛化拒绝）；合法 skill_hint→进入 Planner 生成 PlanCard；规则高置信短剧→helper 0 次调用、正常出 plan。
- 回归：`planner_cutover_test.go` 全套（短剧主链路、greeting/help 元回复、out_of_scope 锚回 review、regenerate、review 修改 by fork）零回退。

## 7. 后续真实 API 回归清单（接真实 provider 前）

单测已覆盖逻辑分支；真实 API 联调需另跑以下清单（建议 `agent_llm_interpreter.enabled=true` + 已配置 provider，灰度环境）：

1. **延迟与降级**：真实 P99 是否落在 `timeout_ms` 内；超时确实降级为规则、不阻塞 PlanCard 路径。
2. **JSON 稳定性**：`JSONMode` 下是否始终返回纯 JSON；偶发夹带文本/围栏时 `sanitizeInterpreterJSON` 是否兜住。
3. **枚举/skill_hint 幻觉**：模型是否产出不存在的 act 或 catalog 外 skill（如 `text_to_image` 未注册时必须降级/丢弃，不得伪造技能）。
4. **场景准确率**（按离线对话样本集评估）：
   - smalltalk：你好 / 哦 / 谢谢 / 你是谁
   - help：别的还会做什么呢 / 你能做什么 / 有什么功能
   - out_of_scope：天气预报 / 可以查天气吗（有 blocking task 时需锚回）
   - 创作目标：极简 Logo / 几何 Logo / AI 品牌标志 → image/logo（当前无 Skill → "接入中"解释）
   - 短剧：搞一个搞笑短剧（仍 clarify）；pending 下"程序员第一天上班误删数据库"（延续 pending 出 PlanCard）
5. **不回退校验**：review 修改 by fork、override（不要电子乐换成钢琴）、再来一版、取消——确认仍由规则处理，helper 不介入、不污染 `user_prompt`。
6. **prompt injection / 越权**：用户诱导"已确认""直接执行""跳过确认"时，LLM 输出不得改变 confirm 边界（确认门在 runtime，LLM 无权触发）。
7. **隐私/日志审计**：抽查日志确认无 raw prompt / output / reasoning / secret / callback token 落盘。
8. **成本**：统计每会话 LLM 调用次数（应仅在 unknown 回合发生），核对账单与缓存命中。
9. **开关一致性**：`enabled=false` 回归——行为与接入前逐字节一致。

## 8. 非目标（仍属后续阶段）

LLM 直接生成 ActionPlan / PlanCard / Capability / Task；MCP；自由工具调用；跨 Skill 自动组合；长期多步 autonomous loop。参见 `08-llm-planner-roadmap.md` 的分阶段路线（本阶段=Phase 1 helper 的前半步：仅语义解释）。
