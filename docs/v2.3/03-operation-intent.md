# 03 · OperationIntent

## 1. 职责

OperationInterpreter 负责把用户对目标对象的自然语言反馈解释为操作意图。

建议模型：

```go
type OperationKind string

const (
    OperationInspect     OperationKind = "inspect"
    OperationConfirm     OperationKind = "confirm"
    OperationRevise      OperationKind = "revise"
    OperationUpdateField OperationKind = "update_field"
    OperationRegenerate  OperationKind = "regenerate"
    OperationCancel      OperationKind = "cancel"
)

type OperationIntent struct {
    Kind       OperationKind `json:"kind"`
    Target     ObjectRef     `json:"target"`
    Path       string        `json:"path,omitempty"`
    Value      any           `json:"value,omitempty"`
    Feedback   string        `json:"feedback,omitempty"`
    Confidence float64       `json:"confidence"`
    Reason     string        `json:"reason,omitempty"`
}
```

## 2. 第一版解析原则

第一版不追求完美字段拆解。

能可靠结构化时输出 `path/value`：

```json
{
  "kind": "update_field",
  "path": "visual_style",
  "value": "电影风格",
  "feedback": "把风格改为电影风格"
}
```

无法可靠结构化时输出 `revise + feedback`：

```json
{
  "kind": "revise",
  "feedback": "第二幕改成雨夜重逢，整体更电影感"
}
```

Capability adapter 可以把 `feedback` 合并进新版 Plan 的 slots 或生成上下文。通用 capability 层不硬编码短剧字段。

对 `review_artifact`，第一版优先输出：

```text
Kind = revise
Feedback = 原始用户修改意见
```

不要过早把“电影风格”拆成 `visual_style`。短剧当前 `style` 同时承载 genre 与视觉风格语义，过早结构化会把错误边界固化。

## 3. 示例

| 用户输入 | 目标 | OperationIntent |
| --- | --- | --- |
| 修改一下 | review_artifact | `revise`，但缺 feedback，进入追问 |
| 把风格改为电影风格 | review_artifact | `revise/update_field`，feedback 保留原文 |
| 确认 | review_artifact | `confirm` |
| 取消这个任务 | task | `cancel` |
| 再来一版 | result/plan | `regenerate` |

## 4. 与 V2.2 DialogueAct 的关系

V2.2:

```text
ActRequestModification
ActProvideModification
```

V2.3:

```text
Target = review_artifact
Operation = revise
Capability = revise_review_by_fork
```

也就是说，`DialogueAct` 是回合语义，`OperationIntent` 是对象操作语义。它们不互相替代。

## 5. 信息不足

当用户只说：

```text
修改一下
```

OperationInterpreter 可输出：

```json
{
  "kind": "revise",
  "feedback": ""
}
```

CapabilityPolicy 必须判定参数不足，不调用副作用能力。DialoguePolicy 返回针对目标对象的追问：

```text
想怎么修改当前分镜脚本？
```

追问 pending 必须保存 `Target=review_artifact`，保证下一轮反馈不会丢失对象。

## 6. V2.3-2 边界

V2.3-2 只产出 `OperationIntent`，不调用 capability，不创建 Plan，不取消 Task，不清理 await binding，不更新 Activity，也不让旧 ReviewCard stale。

示例：

```text
target = review_artifact
user = 把风格改为电影风格

=> operation.kind = revise
=> operation.feedback = 把风格改为电影风格
```

等 OperationIntent 稳定后，V2.3-3 才进入 `CapabilityPolicy` dry-run，判断当前 `target + operation` 是否可映射到 `revise_review_by_fork`。
