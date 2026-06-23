# V2.3 Object Semantics & Capability Runtime

V2.3 不继续扩张 V2.2 的会话语义判断，而是在其后新增对象语义与能力运行时。

V2.2 回答：

```text
用户这一轮在做什么？
```

V2.3 回答：

```text
动作作用于哪个对象？
用户具体想执行什么操作？
当前对象允许调用哪些能力？
能力如何执行并回写对话状态？
```

目标链路：

```text
ConversationContext
  -> ActiveObjectResolver
  -> TurnInterpreter
  -> TargetResolver
  -> OperationInterpreter
  -> DialoguePolicy
  -> CapabilityPolicy
  -> CapabilityInvoker
  -> CapabilityResult
  -> Decision
```

并非每个回合都进入 Capability Runtime。闲聊、信息不足追问、普通 pending 取消仍由 `DialoguePolicy` 直接处理。只有对象明确、操作明确、策略允许、能力注册存在的副作用动作才调用 capability。

## Planner Foundation

V2.3 是 Planner 的地基，而不只是修 ReviewCard 的 bug。

```text
Agent = LLM / Reasoning + Planning + Memory + Tools
```

在 DreamAI 当前架构里：

| Agent 组成 | DreamAI 对应 |
| --- | --- |
| LLM / Reasoning | `TurnInterpreter`、未来 LLM Interpreter、`OperationInterpreter`、Creative Planner |
| Planning | `DialoguePolicy`、`TargetResolver`、`OperationInterpreter`、`CapabilityPolicy`、未来 `ActionPlan` |
| Memory | `AgentState`、`PendingInteraction`、`CurrentPlan`、`TaskLinks`、conversation history |
| Tools | Skill、Workflow、Capability、Engine Task、Review Gate |

V2.1 定义 Skill Contract，约束工具和技能注册边界；V2.2 定义 Conversation Semantics，让 Agent 听懂当前回合；V2.3 定义 Object Semantics 和 Operation Semantics，让 Agent 知道当前世界里有什么对象、对象是什么状态、用户动作默认指向谁、以及动作空间是什么。

V2.3 还不是完整通用 Planner。它先建立 Planner 所需的可感知世界模型和可执行动作空间，后续 Capability Runtime 才能安全调用工具。

## 文档目录

| 文档 | 内容 |
| --- | --- |
| [00-object-semantics-overview.md](00-object-semantics-overview.md) | 总览、真实代码调查结论、边界 |
| [01-object-ref-and-active-objects.md](01-object-ref-and-active-objects.md) | `ObjectRef` / `ActiveObject` / `PendingInteraction` 目标感知升级 |
| [02-target-resolver.md](02-target-resolver.md) | 目标解析优先级和 ReviewCard 归属 |
| [03-operation-intent.md](03-operation-intent.md) | 用户反馈到 `OperationIntent` 的结构化 |
| [04-capability-registry-and-policy.md](04-capability-registry-and-policy.md) | Capability 注册、暴露、授权与安全策略 |
| [05-capability-call-result-and-invoker.md](05-capability-call-result-and-invoker.md) | 调用、幂等、结果与事务边界 |
| [06-review-revision-by-fork.md](06-review-revision-by-fork.md) | Review 阶段修改采用 fork 重规划的完整产品语义 |
| [07-task-cancel-and-await-cleanup.md](07-task-cancel-and-await-cleanup.md) | Task/Worker/Await/Observer 的真实机制与取消红线 |
| [08-v22-integration.md](08-v22-integration.md) | 与 V2.2 主链路衔接方式 |
| [09-function-calling-and-mcp-roadmap.md](09-function-calling-and-mcp-roadmap.md) | Function Calling 与 MCP 的未来边界 |
| [10-implementation-and-migration-plan.md](10-implementation-and-migration-plan.md) | 分阶段实施计划 |
| [11-regression-matrix.md](11-regression-matrix.md) | 必须覆盖的回归矩阵 |

## 第一版裁决

Review 阶段用户要求修改时，旧运行在产品语义上是 `superseded`，但 Engine 操作状态必须进入 `canceled`，并清理 `await_bindings`。仅标记业务层 `superseded` 不足以阻止 Worker、AwaitPollWorker、signal resume 或 recovery 路径再次推进旧运行。

第一版采用方案 B：

```text
ReviewCard 等待确认
  -> 用户提出修改
  -> Agent 收集具体反馈
  -> 内部 capability 取消当前 awaiting/suspended task
  -> 清理 await binding
  -> 使旧 ReviewCard 失去操作能力
  -> Activity 表达“已被新版替代”
  -> 基于反馈创建新版 PlanCard
  -> 用户确认新版 Plan
  -> 通过现有 outbox/fork 机制启动新 Task
```

## 非目标

本阶段只交付设计文档，不实现 V2.3 业务代码。第一版暂不做 Workflow 内原地修改审核产物、单镜头 patch、复杂多对象修改、LLM Function Calling、MCP Server 或外部工具发现。
