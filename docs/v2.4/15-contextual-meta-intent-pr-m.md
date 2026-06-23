# 15 · PR-M Contextual Meta Intent Policy

> 真测修复（会话 2063793139004477440）：meta intent 没有按 stage 条件化。

## 问题

1. pending clarify + `还可以做什么` → 重复追问，没回答 help。
2. confirming + `哈哈` → 用了完整 persona greeting，自我介绍过重。
3. confirming + `好的` → 重新生成 PlanCard v2，而非确认当前 Plan。

根因：confirm / smalltalk / help / identity 的回应没有结合 Stage + 当前对象。

## 方案

### 1. confirming 自然语言确认（问题3）
`conversation_service.go` 扩充 `confirmPhrases`（加 `好的/好/可以/行/开始吧/来吧/嗯…`）。`advanceTurn` 在 `StageConfirming` 拦截 → `confirmPlan`（launch）。
- exact-match，`可以改成横屏吗` 不误判；仅 `StageConfirming` 触发。

### 2. 规则识别补齐（问题1/2）
`rule_intent.go`：`helpPhrases` += `还可以做什么/还能做什么/还有呢/什么意思…`；`smalltalkPhrases` += `哈哈/呵呵/嘿嘿/不错…`。元意图门 `plannerMetaDecision` 确定性命中并区分 help/smalltalk/identity。

### 3. Stage-aware meta 回应
`runtime.go` `metaReply`：阻塞 stage（`pendingAnchor!=""`）下——
- smalltalk → 只回锚点（不前置 greeting）；confirming 锚点：「方案已经准备好了，你可以确认开始，也可以继续说要调整哪里。」
- help → 「我现在主要能做 AI 短剧。」+ 缺失信息锚点（pending：「当前还需要一个故事点，比如人物、场景或冲突。」）。
- identity → 「我是 DreamAI 创作助手。」+ 锚点。
- idle（非阻塞）→ 维持完整 persona 文案。

### 4. LLM 兜底（词表外措辞）
`hybrid_turn_interpreter.go`：pending 下规则判 `ActAnswerQuestion` 但回答非具体 brief 时，调 helper 复核（带 LastAgent 上下文）；高置信判为 meta（help/smalltalk/identity/out_of_scope）→ 覆盖为该 act → stage-aware 回应；否则保留回答。受开关 + 置信度 + 降级约束。

## 行为对照

| 场景 | 之前 | 现在 |
|---|---|---|
| confirming + 好的/可以/行 | 重新出 PlanCard v2 | 直接确认 → executing |
| confirming + 哈哈 | 完整自我介绍 | 「方案已经准备好了…」 |
| pending + 还可以做什么 | 重复追问 | 「我现在主要能做 AI 短剧。当前还需要一个故事点…」 |
| pending + 好的 | clarify | clarify（不变） |
| 具体故事 brief | 出卡 | 出卡（不变） |
| result/review 再来一版/改一下 | — | 不回退 |

## 客户端
无新增契约。confirming 阶段自然语言确认现在会直接进入执行（与点击「确认方案」等价）。

## 测试
runtime `meta_intent_test.go`（哈哈锚点 / help-at-pending / 什么意思 / 好的仍 clarify）；service `meta_confirm_test.go`（confirming 好的→launch 同一 plan、pending 好的不被确认）；hybrid pending-meta 兜底用例。`go test ./ai-engine/agent/... ./config/...` 全绿，PR-I/P0/PR-L 不回退。
