# 05 · LLM Interpreter Roadmap

## 1. 原则

未来接 LLM 时，不应只替换：

```go
IntentClassifier.Classify(currentText)
```

LLM 应接在：

```text
LLM TurnInterpreter fallback
```

也就是让 LLM 输出统一的 `TurnInterpretation`。

## 2. 推荐流程

```text
ConversationContext
  -> RuleTurnInterpreter
      high confidence -> TurnInterpretation
      low confidence  -> LLMTurnInterpreter
  -> DialoguePolicy
  -> Decision
```

## 3. LLM 只负责理解，不负责执行

LLM 可以判断：

- 用户是不是在回答 pending。
- 用户是否想修改当前 plan。
- 修改内容是什么。
- 是否可能在切换目标。
- 缺什么信息。

LLM 不可以直接决定：

- 创建 Plan。
- 启动 Workflow。
- 覆盖受保护 slots。
- 选择收费执行路径。
- 清理 pending。

这些必须由 `DialoguePolicy` 和 manifest / contract 规则决定。

## 4. 输出格式

LLM fallback 输出必须是结构化 JSON，对齐 `TurnInterpretation`：

```json
{
  "act": "provide_modification",
  "intent": "short_drama",
  "target": "current_plan",
  "target_plan_id": "123",
  "extracted_slots": {
    "aspect_ratio": "16:9"
  },
  "confidence": 0.82,
  "reason": "用户说'改成横屏'，当前有确认中的短剧方案"
}
```

## 5. Guardrails

必须校验：

- `act` 必须属于枚举。
- `intent` 必须来自 active manifests。
- `extracted_slots` 必须符合 manifest slots。
- `target_plan_id` 必须等于当前会话可访问 Plan。
- 低置信结果进入 clarification，不直接 Plan。

## 6. 何时接入

LLM fallback 不是 V2.2 第一阶段目标。

接入条件：

- Rule interpreter + policy 已覆盖回归矩阵。
- `PendingInteraction` 已持久化。
- Trace / reason 已能记录。
- 有离线对话样本评估集。

## 7. 评估指标

LLM fallback 必须按样本集评估：

- Act accuracy。
- Pending answer attribution accuracy。
- Plan creation false positive rate。
- Modification vs request-modification distinction。
- Cancel / smalltalk 不打断目标。
