# AI Engine 第二阶段迭代需求文档

日期：2026-04-22

本文档基于第一阶段引擎测试建设过程中的实际观察整理，目标不是罗列“未来可能想做什么”，而是沉淀当前工作流引擎已经暴露出的结构性风险，并将其转化为第二阶段可执行的迭代需求。

## 1. 背景

第一阶段我们已经围绕以下能力补了一轮高价值测试：

- `fork / reuse / patch / resume` 的白盒测试
- `map / loop / subworkflow` 状态机测试
- `ResumeTask` 与 listener 的幂等恢复测试
- `runDAG / finalizeNode / edge closure` 的调度级测试

这些测试已经能覆盖引擎主干路径，但也暴露出一个更明确的事实：

- 当前引擎不是简单的 DAG runner，而是“持久化状态机 + 增量复用 + 事件驱动恢复”的混合运行时
- 正确性依赖分散在多个层次：`BuildRunPlan`、`MaterializeRunPlan`、`executeNode`、`ResumeTask`、listener、`map/loop/subworkflow` checkpoint
- 代码已经能工作，但语义边界、状态约束、字符串协议、checkpoint 结构和恢复契约还不够收敛

因此第二阶段的重点，不是继续补业务 workflow，而是把引擎的运行时语义收紧、显式化、可观测化。

## 2. 第二阶段目标

第二阶段目标有四个：

1. 统一运行时语义，降低分散在不同模块中的隐式规则
2. 提升恢复与幂等的确定性，避免重复事件、陈旧 child、部分恢复造成脏状态
3. 收紧 fanout 节点的 checkpoint 契约，减少 `map[string]any` 魔法字段带来的隐性耦合
4. 建立引擎级可演进的测试与调试基座，让后续迭代不再高成本回归

非目标：

- 本阶段不优先扩展新的业务 workflow 能力
- 本阶段不优先做 UI 层面的改造
- 本阶段不优先做大规模性能优化，除非某项改动直接影响幂等或恢复正确性

## 3. 第一阶段测试暴露出的核心风险

### 3.1 运行时语义分散，优先级规则不够显式

当前节点是否 `reuse / patch / execute` 的判定，散落在：

- `BuildRunPlan`
- `MaterializeRunPlan`
- `prepareDirtyRuntime`
- `executeNode`
- `isExecutionRequired`

虽然测试已经能覆盖主要分支，但“优先级为什么是这样”仍主要依赖读代码理解。例如：

- `resume_boundary`、`patched_node`、`parent_not_success`、`missing_parent_snapshot` 的优先级并不集中定义
- `MaterializeRunPlan` 和 `executeNode` 都会影响最终执行行为
- `ExecutionReason`、`DirtyReason`、`PlanAction` 三套语义有映射，但没有统一的中心模型

风险：

- 后续继续扩展 fork/edit/replay 能力时，容易在某一层修了一半，另一层仍保留旧语义
- 新人维护时容易“看起来改对了，实际只改对一半”

### 3.2 字符串协议不统一，已经出现潜在失配

在测试和代码梳理中，已经观察到多处字符串协议的隐式耦合：

- `ExecutionReason` 与 `DirtyReason` 分别维护
- `loop` 中 `shouldRecreateLoopRunningTask` 使用的 reason 字符串，与引擎主流程里的 reason 命名并不完全一致
- patch path 当前以 bracket index 为主，但旧测试和旧思维模型中仍混用了 dot-number 语法

风险：

- 表面上都是字符串枚举，实际有一处拼写差异就会导致恢复逻辑悄悄失效
- 这类问题不容易通过普通业务回归发现，只能靠白盒测试或线上事故暴露

### 3.3 fanout 节点 checkpoint 过于“弱类型”

`map` 和 `loop` 当前都依赖 `Checkpoint map[string]any` 维护内部状态，典型字段包括：

- `results`
- `item_hashes`
- `reused_items`
- `done`
- `total`
- `current_index`
- `running_index`
- `running_sub_key`
- `carry_state`

这些字段目前依赖：

- 多处硬编码 key
- 不同模块自行读写
- 字段类型靠运行时断言保证

风险：

- 一旦 key 漏写、字段类型漂移、补丁路径写错，错误通常会在恢复阶段才暴露
- checkpoint 语义难以文档化，也很难做跨版本兼容

### 3.4 listener 正确性高度依赖隐式契约

当前 listener 设计是合理的，但契约是隐式的：

- async 节点通过 `AttemptCompletePendingEdges` 去重
- map/loop 子任务事件通过 `canAcceptChildResult` 和 checkpoint binding 过滤 stale child
- fanout 节点 listener 只唤醒父任务，不直接完成父节点
- 非 fanout 节点 listener 会先 `completeAsyncNode` 再 `ResumeTask`

这些行为现在已经有测试覆盖，但缺少统一说明。

风险：

- 后续若新增新的 listener 类型、补偿逻辑或主动重试逻辑，容易破坏幂等假设
- 业务开发者不清楚“应该在 listener 里做聚合，还是在 Resume 后做聚合”

### 3.5 ResumeTask 是引擎核心入口，但恢复模型仍偏脆弱

`ResumeTask` 当前要同时处理：

- async 节点恢复
- subworkflow 节点恢复
- `success_pending_edges`
- `failed_pending_edges`
- runtime output/meta 补回
- activated edges rebuild

测试表明主链路可用，但恢复模型仍偏脆弱，主要体现在：

- 恢复前提依赖 task/node runtime 的组合状态，而这些状态约束没有统一数据模型
- 恢复时 output 来源可能来自 `meta`、runtime persisted output、child final，不同来源的优先级靠分支代码保证
- 重复 resume、late event、terminal parent 拦截虽然已测通，但属于“后置防御”而不是“前置设计”

### 3.6 控制流主干缺少统一的调试视图

当前我们可以通过：

- task
- node runtime
- event
- checkpoint
- output

去回放执行过程，但没有一个统一的“调度解释层”告诉我们：

- 当前节点为什么被判定为 execute
- 当前节点为什么被 skip
- 当前边为什么被关闭
- 当前 child event 为什么被拒绝

风险：

- 出问题时排查成本高
- 很多逻辑即使测试通过，也仍然“不好理解、不好解释”

## 4. 第二阶段需求

## 4.1 统一运行时决策模型

需求：

- 引入统一的运行时决策模型，收敛 `PlanAction / ExecutionReason / DirtyReason / ResumeReason`
- 明确每个节点在 planning、materialize、execute 三个阶段允许的状态变化
- 将“优先级规则”从散落的 if-else 中抽象成中心化决策表或规则函数

建议产出：

- `RuntimeDecision` 或同等语义结构
- 一份运行时决策矩阵文档
- 决策级 debug 输出，至少能解释节点为何 `reuse / patch / execute`

验收标准：

- 关键 reason 不再靠裸字符串散落多处维护
- `BuildRunPlan`、`MaterializeRunPlan`、`executeNode` 的核心分支能映射到统一模型

## 4.2 收紧 reason/path/checkpoint 等字符串协议

需求：

- 将所有 `ExecutionReason`、`DirtyReason`、fanout checkpoint key、patch target/op/path grammar 统一收敛
- 对外提供一份明确的 patch path grammar 规范
- 所有关键 reason/key 都应集中定义，禁止新增裸字符串

建议产出：

- `const` / typed enum 集中定义文件
- patch path 规范文档
- 对关键协议增加 compile-time 或启动期校验

验收标准：

- 新增 reason/key 时必须修改中心定义，而不是任意字符串拼接
- `loop`、`map`、`resume`、`patch` 之间不再存在 reason 命名漂移

## 4.3 将 fanout checkpoint 从弱类型结构升级为显式模型

需求：

- 为 `map` 和 `loop` 定义显式 checkpoint schema
- 将 checkpoint 的读写封装到类型化 accessor / mutator 中
- 将 checkpoint rebuild 与 public output rebuild 的关系文档化

建议产出：

- `MapCheckpoint`
- `LoopCheckpoint`
- 序列化/反序列化辅助层
- fanout 节点 checkpoint schema 文档

验收标准：

- 新增/修改 checkpoint 字段时不需要全项目 grep 魔法 key
- patch checkpoint 时能够明确校验路径是否合法

## 4.4 重构 listener 与恢复契约

需求：

- 明确 listener 只负责哪一层职责：去重、验权、唤醒、聚合、完成
- 将 async/subworkflow/fanout listener 的契约写成统一文档
- 对 duplicate event、late event、stale child event 定义统一处理策略

建议产出：

- listener contract 文档
- 事件去重与接纳决策日志
- 可选：为 child event 增加标准化 rejection reason

验收标准：

- 对每类 child event，能够明确回答“什么时候接受、什么时候拒绝、拒绝后做什么”
- 线上排查时可以从日志直接看出事件被拒绝的原因

## 4.5 强化 ResumeTask 恢复模型

需求：

- 显式定义 resume 的前置条件、输入来源、状态变换和失败回退策略
- 将 `success_pending_edges`、`failed_pending_edges` 等恢复态纳入统一状态机文档
- 为重复恢复、锁竞争失败、parent terminal、missing runtime 等路径定义明确返回语义

建议产出：

- `ResumeTask` 状态图
- 恢复态错误码/错误原因枚举
- resume inspector 或 debug DTO 扩展

验收标准：

- `ResumeTask` 不再是“读代码才能懂的黑盒入口”
- 对恢复失败的原因可以直接分类型观测，而不是只看 error string

## 4.6 建立统一的引擎测试基座

需求：

- 将第一阶段补出来的 fake repo、fake executor、workflow helper 抽成统一 testkit
- 支持快速构造：
  - fork/reuse 场景
  - async resume 场景
  - fanout child event 场景
  - runDAG branch/failure closure 场景

建议产出：

- `engine/testkit`
- `nodes/testkit`
- 标准 workflow fixture

验收标准：

- 新增引擎级测试时，不再重复复制 fake repo
- 测试关注点回到语义，而不是测试脚手架本身

## 4.7 增强可观测性与调试能力

需求：

- 为 planning、materialize、resume、listener rejection、edge closure 增加结构化 debug 信息
- 提供 task 级“运行解释视图”

建议产出：

- `RunPlanPreview` 能展示更多解释信息
- task/node inspector 中增加 reason/decision/binding/rejection 字段
- listener rejection reason 事件或日志

验收标准：

- 遇到线上恢复问题时，能够通过 inspector 或日志快速定位是 planning 问题、listener 问题还是 checkpoint 问题

## 5. 优先级建议

### P0

- 统一运行时决策模型
- 收紧字符串协议
- 强化 `ResumeTask` 恢复模型

### P1

- fanout checkpoint 类型化
- listener 契约重构
- 统一测试基座

### P2

- inspector / 调试视图增强
- patch grammar 与 checkpoint schema 文档化完善

## 6. 建议实施顺序

建议按下面顺序推进：

1. 先统一枚举/字符串协议，消除最危险的隐式耦合
2. 再收敛 `BuildRunPlan -> Materialize -> Execute` 的决策模型
3. 然后类型化 `map/loop` checkpoint，并同步梳理 listener 契约
4. 在此基础上增强 `ResumeTask` 和 inspector
5. 最后抽统一 testkit，降低后续迭代成本

## 7. 需要特别关注的已知风险

- system node 或 zero-output node 的复用语义仍需专门梳理，否则容易出现 runtime state 与 public output 不一致
- `BuildDirtyPlan` 与 `BuildRunPlan` 当前存在语义重叠，长期并存会提升维护成本，建议评估是否收敛为单一规划入口
- 当前很多正确性由“防御性判断 + 测试兜底”保证，第二阶段应尽量升级为“数据模型先约束，再由代码实现”

## 8. 结论

第一阶段测试已经证明：

- 当前引擎主链路是可工作的
- 但运行时语义仍然偏分散、偏隐式、偏字符串驱动

第二阶段的核心价值，不是“再多做几个 workflow”，而是把这套引擎真正从“复杂但能跑”推进到“复杂但可解释、可维护、可扩展”。
