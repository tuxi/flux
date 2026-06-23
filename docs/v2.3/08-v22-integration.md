# 08 · V2.2 Integration

## 1. V2.2 保持职责

V2.2 继续负责：

```text
用户这一轮在做什么？
```

输出：

```text
start_goal
answer_question
confirm
request_modification
provide_modification
regenerate
cancel
smalltalk
switch_goal
unknown
```

不要继续把 ReviewCard、Task、AwaitBinding、Capability 操作塞进 V2.2。

## 2. V2.3 插入点

现状：

```text
ConversationContext
  -> TurnInterpreter
  -> SkillSufficiencyEvaluator
  -> DialoguePolicy
  -> DialogueDecisionBuilder
  -> Decision
```

V2.3：

```text
ConversationContext
  -> ActiveObjectResolver
  -> TurnInterpreter
  -> TargetResolver
  -> OperationInterpreter
  -> SkillSufficiencyEvaluator
  -> DialoguePolicy
  -> CapabilityPolicy
  -> CapabilityInvoker
  -> CapabilityResult
  -> DialogueDecisionBuilder
  -> Decision
```

其中 `CapabilityPolicy/Invoker` 只在 directive 需要副作用能力时进入。

## 3. 新 Directive

建议新增通用 directive：

```go
const DirectiveInvokeCapability DialogueDirectiveKind = "invoke_capability"

type DialogueDirective struct {
    Kind         DialogueDirectiveKind
    Capability  *CapabilityCall
    // existing fields...
}
```

不要为每种对象操作新增大量 directive，例如：

```text
DirectiveRevisePromptReview
DirectiveCancelAwaitingTask
DirectiveSupersedeActivity
```

这些都属于 capability 内部职责。

## 4. RequestModification 链路

Reviewing 阶段：

```text
user = 修改一下
```

V2.2:

```text
ActRequestModification
```

V2.3:

```text
Target = review_artifact
Operation = revise, feedback empty
```

DialoguePolicy：

```text
DirectiveClarify
SetPending = collect_modification(Target=review_artifact)
Reply = 想怎么修改当前分镜脚本？
```

第二轮：

```text
user = 把风格改为电影风格
```

V2.2:

```text
ActAnswerQuestion 或 ActProvideModification
```

V2.3:

```text
Target = PendingInteraction.Target
Operation = revise(feedback)
DirectiveInvokeCapability(revise_review_by_fork)
```

## 5. Sufficiency 调整

当前 `SkillSufficiencyEvaluator` 面向 plan slots。V2.3 需要新增对象操作 sufficiency：

```text
review_artifact + revise requires feedback
plan + modify requires enough slots or raw feedback
task + cancel may require explicit approval
```

第一版可以先在 OperationInterpreter/CapabilityPolicy 中判断 `feedback` 是否为空，避免大改 V2.2 evaluator。

## 6. Backward Compatibility

迁移期：

- 保留 `PendingInteraction.TargetPlanID`。
- 新写入优先使用 `Target *ObjectRef`。
- 读取时如果 `Target == nil && TargetPlanID != nil`，派生成 `ObjectRef{Type:plan, PlanID}`。
- 旧 plan 修改路径继续可用。
- Review 阶段的修改优先进入 V2.3 runtime。

## 7. Shadow / Regression

V2.3 上线前建议 shadow 记录：

```text
active_objects
target_resolution
operation_intent
capability_policy_decision
would_invoke_capability
```

但 shadow 不执行 capability 副作用。只有通过回归矩阵后才启用真实 invoker。

