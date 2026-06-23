# AI Engine 第二阶段 P0/P1 开发任务清单

日期：2026-04-22

关联文档：

- [AI Engine 第二阶段产品需求文档（PRD）](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/docs/engine-phase2-prd.md)
- [AI Engine 第二阶段评审汇报版摘要](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/docs/engine-phase2-review-summary.md)

## 说明

本清单按 `P0 / P1` 拆分，目标是直接作为开发排期与任务拆解基础使用。

每个任务包含：

- 任务目标
- 核心改动点
- 验收标准

## P0 任务

## P0-1 统一运行时决策模型

任务目标：

- 收敛节点在 planning、materialize、execute、resume 中的决策语义

核心改动点：

- 梳理并统一 `PlanAction / ExecutionReason / DirtyReason`
- 引入统一决策模型，例如 `RuntimeDecision`
- 将优先级规则从散落分支中提取为中心化逻辑
- 为关键节点输出决策解释信息

验收标准：

- `BuildRunPlan`、`MaterializeRunPlan`、`executeNode` 能映射到统一语义模型
- 关键 reason 不再多处裸字符串维护
- 新增引擎级测试覆盖决策矩阵

## P0-2 收紧关键协议定义

任务目标：

- 消除 reason/path/checkpoint key 的隐式耦合

核心改动点：

- 收敛所有关键 reason 常量
- 收敛 fanout checkpoint key 常量
- 明确 patch path grammar
- 禁止继续新增裸字符串协议

验收标准：

- `loop / map / resume / patch` 之间 reason 命名一致
- patch path 规则有文档且测试覆盖
- 关键 key 与 reason 有统一定义文件

## P0-3 强化 ResumeTask 恢复模型

任务目标：

- 将 `ResumeTask` 从黑盒入口变成有明确状态机的恢复入口

核心改动点：

- 定义 resume 前置条件与合法状态
- 明确 `success_pending_edges / failed_pending_edges` 的恢复行为
- 明确 output 来源优先级：`meta / runtime output / child final`
- 为重复恢复、missing runtime、terminal task、锁失败等情况定义标准返回语义

验收标准：

- `ResumeTask` 有完整状态图
- 重复 resume、late event、terminal parent 行为有明确测试
- 对恢复失败原因可结构化观测

## P0-4 建立 listener 契约说明与拒绝策略

任务目标：

- 明确 listener 在恢复体系中的职责边界

核心改动点：

- 统一 async/subworkflow/fanout listener 行为说明
- 明确 duplicate event、late event、stale child event 的处理策略
- 增加 rejection reason 或等价结构化日志

验收标准：

- 能直接回答某类 event 为何被接受/拒绝
- listener 相关日志可用于线上排障
- listener 契约与测试保持一致

## P0-5 增强运行解释信息

任务目标：

- 让 planning / materialize / resume / listener rejection 更可解释

核心改动点：

- 为节点增加 decision/reason/execution source 等调试字段
- 为 child event rejection 增加结构化记录
- 为 edge closure/skip 增加解释信息

验收标准：

- 能从 inspector 或日志解释节点为何被 `reuse / patch / execute / skip`
- 能解释 child event 被拒绝的具体原因

## P1 任务

## P1-1 fanout checkpoint 类型化

任务目标：

- 将 `map / loop` checkpoint 从弱类型结构升级为显式模型

核心改动点：

- 定义 `MapCheckpoint`
- 定义 `LoopCheckpoint`
- 封装 checkpoint accessor / mutator
- 统一 checkpoint rebuild 与 public output rebuild

验收标准：

- 新增或修改 checkpoint 字段不再需要全局搜索魔法 key
- patch checkpoint 时可校验合法路径
- `map / loop` 状态机测试稳定通过

## P1-2 patch 与 checkpoint 规则收敛

任务目标：

- 让 patch 能力在 runtime output 和 checkpoint 上都具备稳定契约

核心改动点：

- 明确 patch target/op/path 的合法范围
- 明确 checkpoint patch 后的 rebuild 行为
- 为 patch/resume 关系定义统一限制

验收标准：

- patch 规则有统一文档与测试
- checkpoint patch 不再依赖隐式 rebuild 假设

## P1-3 抽离统一 testkit

任务目标：

- 降低新增引擎级测试的成本

核心改动点：

- 抽离 fake repo
- 抽离 fake executor
- 抽离常用 workflow fixture
- 抽离常用 helper

验收标准：

- 引擎级测试不再重复复制脚手架
- 新增状态机/恢复测试能快速搭建

## P1-4 增强 inspector / 调试视图

任务目标：

- 为运行时问题提供统一可视化/可查询解释视图

核心改动点：

- 扩展 `RunPlanPreview`
- 扩展 run inspector DTO
- 提供节点 decision / binding / rejection 视图

验收标准：

- 出问题时可以通过 inspector 快速定位是 planning、listener、resume 还是 checkpoint 问题

## P1-5 BuildDirtyPlan 与 BuildRunPlan 语义评估

任务目标：

- 降低双规划入口长期共存带来的维护成本

核心改动点：

- 评估 `BuildDirtyPlan` 与 `BuildRunPlan` 的职责重叠
- 明确是否保留双入口或收敛为单入口
- 如收敛，提供迁移方案

验收标准：

- 规划入口职责明确
- 不再存在语义重叠但行为不一致的长期风险

## 任务依赖建议

建议依赖顺序：

1. `P0-2` 收紧关键协议定义
2. `P0-1` 统一运行时决策模型
3. `P0-3` 强化 `ResumeTask`
4. `P0-4` listener 契约说明与拒绝策略
5. `P0-5` 运行解释信息增强
6. `P1-1` fanout checkpoint 类型化
7. `P1-2` patch 与 checkpoint 规则收敛
8. `P1-3` 抽离 testkit
9. `P1-4` inspector 增强
10. `P1-5` 规划入口收敛评估

## 建议排期方式

如果按两期推进：

- 第一小期：完成全部 P0
- 第二小期：完成 P1-1 / P1-2 / P1-3

如果按三期推进：

- 第一期：P0-2 / P0-1 / P0-3
- 第二期：P0-4 / P0-5 / P1-1
- 第三期：P1-2 / P1-3 / P1-4 / P1-5

## 建议会议输出

评审会后建议形成三项输出：

- 是否通过立项
- P0 是否整体承诺进入开发排期
- P1 中哪些项作为本阶段承诺，哪些项作为候选储备
