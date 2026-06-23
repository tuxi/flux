# 09 · Shadow Mode

V2.2-5B 的目标不是让新旧决策完全一致，而是在不影响真实用户结果的前提下，观察新架构如何解释真实会话，并把差异分成可审查的类别。

真实路径仍然返回 legacy decision：

```text
legacyDecision := legacyRespond(...)
shadowReport := v22Shadow.Evaluate(...)
return legacyDecision
```

Shadow 不允许：

- 写数据库。
- 修改 `AgentState`。
- 创建持久 Plan。
- 追加消息。
- 写 Outbox。
- 改 HTTP / WS 响应。
- 调 LLM。

## 1. Pipeline

Shadow 输入使用 `ConversationContext` 的深拷贝：

```text
ConversationContext
  -> RuleTurnInterpreter
  -> RuleSkillSufficiencyEvaluator
  -> RuleDialoguePolicy
  -> DialogueDirective
  -> optional DialogueDecisionBuilder
  -> DecisionShape
```

第一层比较 legacy shape vs directive shape。

第二层在 `BuildDecisionShape=true` 时比较 builder 生成的 shadow decision shape。Builder 错误只记录 `evaluation_error`，不影响 legacy 返回。

## 2. DecisionShape

Shadow 不比较完整 `service.Decision`。完整 Decision 包含文案、临时 Plan ID、map 顺序、空值差异和展示字段，直接 deep equal 会产生噪音。

比较模型：

```go
type DecisionShape struct {
    Stage             domain.Stage
    SkillKey          string
    Intent            string
    Action            string
    CreatesPlan       bool
    LaunchesTask      bool
    PlanSlots         map[string]any
    OutboundKinds     []domain.MessageKind
    PendingKind       domain.PendingInteractionKind
    PendingAskedSlot  string
    PendingTargetPlan *int64
    ClearsPending     bool
    PreservesPending  bool
}
```

## 3. Diff Class

Shadow diff 分类：

```go
const (
    ShadowEquivalent          = "equivalent"
    ShadowExpectedImprovement = "expected_improvement"
    ShadowPotentialRegression = "potential_regression"
    ShadowUnsupported         = "unsupported"
    ShadowEvaluationError     = "evaluation_error"
)
```

已知预期改进：

| 场景 | Legacy | V2.2 | 分类 |
|------|--------|------|------|
| completed + "改一下" | 创建新版 Plan | clarify + collect_modification | expected_improvement |
| idle + "做一个搞笑短剧" | 直接创建 Plan | clarify story brief | expected_improvement |
| pending + smalltalk | 可能扰动 pending | preserve pending | expected_improvement |
| cancel | 清理不稳定 | clear pending | expected_improvement |

已知等价：

| 场景 | 分类 |
|------|------|
| "再来一版" | equivalent |
| "改成横屏" | equivalent |
| pending answer creates plan | equivalent |

未知差异先标记 `potential_regression`，不直接切流。

## 4. Privacy

Shadow 记录默认不保存完整用户文本、完整 recent messages、完整 prompt 或授权信息。

输入只记录：

- hash
- rune length
- class

生产环境不得记录：

- 上传素材 URL 签名参数。
- 用户隐私文本。
- Authorization。
- 模型密钥。
- 完整 prompt。

## 5. Latency

规则版 Shadow 目标总耗时 `< 10ms`。

记录：

- `interpreter_duration_ms`
- `sufficiency_duration_ms`
- `policy_duration_ms`
- `builder_duration_ms`
- `total_shadow_duration_ms`

Shadow 出错或超时只记录 `evaluation_error`，继续返回 legacy decision。

## 6. Config

第一版配置：

```go
type ShadowConfig struct {
    Enabled            bool
    SampleRate         float64
    SkillKeys          []string
    Acts               []DialogueAct
    BuildDecisionShape bool
    Timeout            time.Duration
}
```

建议初始策略：

- `SampleRate <= 0` 表示不采样。
- 开发 / 测试环境 100%。
- 生产环境按采样率开启。
- 第一批优先观察 `short_drama`。
- LLM Interpreter 不进入全量 Shadow，未来必须单独采样。

## 7. Runtime Wiring

V2.2-5B 已接入启动装配点：

```text
ai-engine/server/server.go
  -> configureAgentShadowMode(...)
  -> AgentRuntime.SetShadowMode(...)
  -> SlogShadowRecorder
```

配置位置：

```yaml
ai_engine:
  agent_shadow:
    enabled: true
    sample_rate: 1.0
    skill_keys: ["short_drama"]
    acts: ["request_modification", "regenerate", "cancel", "smalltalk"]
    build_decision_shape: true
    timeout_ms: 10
```

测试方式：

1. 在本地 `config/config.yaml` 开启上述配置。
2. 启动服务。
3. 通过真实会话触发 Agent turn。
4. 搜索日志事件：

```bash
rg 'agent_v22_shadow' /var/log/dream-ai/app.log
```

日志只记录脱敏字段：

- input hash / length / class
- dialogue act
- sufficiency summary
- directive kind
- legacy / V2.2 decision shape
- slot keys only
- diff class
- reason code
- shadow durations

不得通过 recorder 记录 slot values、完整 prompt 或 recent messages。

## 8. 5C 裁决更新

Agent 尚未上线，处于早期开发阶段，因此 5C 不再要求先完成 Shadow 审查或分批灰度切流。

Shadow Mode 保留，但默认关闭。它不再是 5C 的解锁门；V2.2 主流程下不再执行 Shadow。只有短期整体切回 legacy 调试时，Shadow recorder 才会被装配。

5C 的真实路径改为：

```text
TurnInterpreter
  -> SkillSufficiencyEvaluator
  -> DialoguePolicy
  -> DialogueDecisionBuilder
  -> service.Decision
```

允许保留一个短期全局 legacy 调试开关：

```yaml
ai_engine:
  agent_use_legacy_runtime: true
```

该开关只能整体切回 legacy，不允许按场景长期双轨运行。V2.2 主路径出错时必须显式返回错误，禁止静默 fallback 到 legacy。
