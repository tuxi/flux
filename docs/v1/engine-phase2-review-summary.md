# AI Engine 第二阶段评审汇报版摘要

日期：2026-04-22

关联文档：

- [AI Engine 第二阶段产品需求文档（PRD）](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/docs/engine-phase2-prd.md)
- [AI Engine 第二阶段迭代需求文档](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/docs/engine-phase2-requirements.md)

## 一页结论

第一阶段测试已经证明：`ai-engine` 工作流引擎的主链路是可工作的，但当前系统已经从“普通 DAG 执行器”演化成“持久化状态机 + 增量复用 + fanout 状态机 + 事件驱动恢复”的复杂运行时。

第二阶段不建议继续优先堆业务 workflow，而应优先夯实底层运行时。否则后续每新增一个 workflow 能力，都会同步放大恢复、幂等、复用和排障成本。

本期建议立项，目标是让引擎从“复杂但能跑”升级到“复杂但可解释、可恢复、可维护”。

## 为什么现在做

第一阶段测试已覆盖：

- `fork / reuse / patch / resume`
- `map / loop / subworkflow`
- async / child event listener 幂等恢复
- `runDAG / skip / edge closure / pending_edges`

覆盖之后暴露出的核心问题不是“某个功能没实现”，而是底层设计风险已经清晰：

- 运行时决策分散，优先级靠代码分支隐式表达
- reason/key/path 等字符串协议不统一
- `map / loop` checkpoint 仍是弱类型结构
- listener 契约与恢复契约缺少统一定义
- `ResumeTask` 作为核心入口，状态机仍偏脆弱
- 缺少统一调试与解释视图

## 如果不做会怎样

- fork / patch / replay / resume 场景的线上风险持续累积
- fanout 节点扩展成本越来越高
- 业务开发继续接 workflow 时，排障成本持续上升
- 新能力上线会越来越依赖“补丁式防御”，而不是体系化设计
- 引擎维护门槛越来越高，新人接手成本变大

## 本期做什么

本期聚焦 5 件事：

1. 统一运行时决策模型
2. 强化恢复与 listener 契约
3. 将 fanout checkpoint 类型化
4. 收紧 reason/path/checkpoint 等关键协议
5. 增强可观测性与测试基座

## 本期不做什么

- 不以新增业务 workflow 功能为主目标
- 不优先改 UI 或配置平台
- 不做纯性能导向的大规模重写

## 预期收益

业务收益：

- fork / patch / replay / resume 的行为更稳定、更可预测
- fanout 节点在复杂场景下的回归风险下降

研发收益：

- 能解释“为什么这个节点被复用/重跑/跳过”
- 能解释“为什么这个 child event 被接纳/拒绝”
- 出问题时排查路径从“读代码”降到“看结构化信息”

工程收益：

- 关键 reason/key/path 不再到处裸字符串扩散
- 引擎测试基座更统一，后续迭代成本下降

## 建议优先级

### P0

- 统一运行时决策模型
- 强化 `ResumeTask` 恢复模型
- 收紧关键协议

### P1

- fanout checkpoint 类型化
- listener 契约重构
- testkit 抽离

### P2

- inspector / 调试视图增强
- 规范和文档完善

## 建议实施顺序

1. 先统一 reason / decision / key / path 等关键协议
2. 再收敛 planning -> materialize -> execute -> resume 的运行时模型
3. 然后处理 fanout checkpoint 与 listener 契约
4. 最后增强观测与测试基座

## 建议评审结论

建议通过立项，并按 P0 -> P1 顺序推进。

原因：

- 问题已经被测试明确暴露，不再是抽象担忧
- 本期收益集中在“稳定性、维护性、可扩展性”，属于底层核心投入
- 现在做成本最低，继续叠业务后再做，迁移成本会明显更高
