# 14 · PR-L Contextual Sufficiency & Prompt Pollution Guard

> 真测修复：泛化请求 / 认可回复被误当成 short_drama 创意，污染 user_prompt。

## 问题

会话 2063774793294942208：

1. `帮我生成一部短剧吗` → 直接出 PlanCard，`user_prompt="帮我生成一部短剧吗"`。应 clarify。
2. help 后 `好的` → 出 PlanCard，且继承上一轮错误的 `user_prompt`。应 clarify，不继承。

## 根因

- 一套较好的 brief 充分性启发式（`hasShortDramaStoryBrief`/`normalizeStoryBriefCandidate`）只接在不活跃的 V2.2 路径；
- 活跃的 Planner 路径用窄的 `isGenericShortDramaPrompt`（仅认「搞一个/做一个」），漏判 `帮我生成一部短剧吗`；
- 规则槽位抽取「skill=short_drama 且非 accept → 整句写 user_prompt」污染。

## 方案：充分性升级 + 单一事实源

### 1. 共享 `ai-engine/agent/brief` 包
- `IsSpecificStoryBrief(text)`：**严格**冷启动充分性——剥离技能词/请求脚手架/语气词/时长/风格/比例后，残余 <4 字 → 否；命中人物/场景/冲突标记 → 是；残余 ≥8 字 → 是；否则否。
- `IsContentful(text)`：**宽松**污染守卫——剥离后是否还有残余内容。阻断纯泛化请求/壳，但保留短修改语（「第二幕改成夜晚」）。

### 2. Planner 唯一选取点收紧
`planner.go` `valueForField`（user_prompt 的唯一来源选取点，覆盖 turn / collected / current_plan 三源）改用 `!brief.IsSpecificStoryBrief`。一处修复问题 1 与问题 2（含 carryover：陈旧泛化 prompt 被拒）。

### 3. 规则层防污染
`rule_turn_interpreter.shouldExtractStoryBrief` 与 `rule_slots.RuleSlotExtractor`：非「正在追问 user_prompt」时，仅 `IsContentful` 才写 user_prompt；ack/control 由 `isAccept`/`isControlText` 单独过滤。

### 4. 去重 V2.2
`sufficiency.go` 复用 `brief.IsSpecificStoryBrief`，删除本地重复启发式。

### 5. LLM 参与「rule 命中但不充分」
`HybridTurnInterpreter` 不再只在 `ActUnknown` 调 LLM：当规则判出 short_drama `start_goal` 但 brief 非具体时，触发 **Contextual Sufficiency Review**：
- `LLMInterpretInput` 增 `LastAgentKind` + `LastAgentText`；
- `LLMInterpretResult` 增 `is_specific_brief` + `should_fill_user_prompt`；
- LLM 抽出具体 brief → 救回填 user_prompt；判非具体/不确定 → 剥离 → 由 Planner clarify。
- 仍受 min_confidence / 超时 / 错误降级；受 `agent_llm_interpreter.enabled` 开关控制。关闭时确定性门控（1–4）已能通过全部验收。

## 判定标准映射

| 输入 | 行为 |
|---|---|
| 帮我生成一部短剧吗 / 做个短剧 / 做一个搞笑短剧 | clarify，不出卡，user_prompt 不被污染 |
| help 后「好的」 | 不出卡、不继承旧 prompt，提示给故事点子 |
| clarify 后「好的」 | 继续追问 |
| clarify 后「程序员第一天上班误删数据库」 | 出卡，user_prompt 正确 |
| 旧错误 prompt + 「好的」 | 不 carryover、不出卡 |

## 客户端

无新增契约。行为变化：泛化请求/认可现在返回 `clarify`（`kind=clarify`）而非 `plan_card`，客户端照常渲染 clarify。

## 测试
`brief_test.go`（充分/不充分样本）、`hybrid_turn_interpreter_test.go`（brief review 救回/不填/降级）、runtime `contextual_sufficiency_test.go`（验收 A/B/C/D/E/F）。`go test ./ai-engine/agent/... ./config/...` 全绿；PR-I + P0 不回退。
