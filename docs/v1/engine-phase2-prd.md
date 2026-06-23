# AI Engine 第二阶段产品需求文档（PRD）

日期：2026-04-22

状态：Draft

负责人：AI Engine / Workflow Engine

关联文档：

- [AI Engine 第二阶段迭代需求文档](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/docs/engine-phase2-requirements.md)
- [AI Engine Workflow Engine Test Checklist](/Users/xiaoyuan/Documents/work/git/dream-ai/ai-engine/docs/engine-test-checklist.md)

## 1. 项目概述

本项目目标是推进 `ai-engine` 工作流引擎进入第二阶段迭代，从“功能可运行”升级到“语义清晰、恢复可靠、可观测、可维护”。

第一阶段我们已经通过补测试覆盖了引擎主干路径，包括：

- `fork / reuse / patch / resume`
- `map / loop / subworkflow`
- async / child event listener 幂等恢复
- `runDAG / edge closure / skip / pending_edges`

测试结果表明，当前引擎的核心能力已经具备可用基础，但也暴露出多个结构性问题：运行时语义分散、字符串协议松散、checkpoint 弱类型、恢复契约隐式、排障视图不足。

因此第二阶段的重点，不是新增更多业务工作流，而是夯实引擎底层运行时。

## 2. 立项原因

当前引擎承担的已不再只是“串行执行节点”，而是：

- 持久化状态机
- 增量复用与局部重跑
- fanout 节点状态管理
- 事件驱动恢复
- 子任务幂等与绑定校验

这意味着底层运行时一旦语义不清晰，问题会表现为：

- 某些任务偶发重复恢复
- 同一个 child event 在不同状态下表现不一致
- 节点被错误复用或错误重跑
- checkpoint 被 patch 后行为不可预期
- 出现问题时难以定位根因

如果不在当前阶段收敛这些问题，后续继续叠加 workflow 能力，只会放大维护成本和线上风险。

## 3. 目标

本期目标：

1. 统一引擎运行时决策语义
2. 强化恢复与幂等确定性
3. 规范 fanout 节点 checkpoint 模型
4. 提升引擎级可观测性与可解释性
5. 降低后续测试与迭代成本

本期非目标：

- 不以新增业务 workflow 功能为主目标
- 不优先改造上层页面或交互
- 不优先做吞吐量导向的性能优化

## 4. 核心问题

### 4.1 运行时决策缺少统一中心

当前节点在执行时，是否应该 `reuse / patch / execute`，判断逻辑分散在多个模块中。虽然现有代码能工作，但优先级规则不够集中，后续扩展成本高。

用户影响：

- 行为解释困难
- 线上问题排查困难
- 需求迭代时改动容易不完整

### 4.2 恢复链路过于依赖隐式契约

当前 async/subworkflow/map/loop 的恢复依赖多条隐式规则协作，包括节点状态、child binding、listener 去重、pending_edges 过渡态等。这些契约没有统一说明，维护门槛高。

用户影响：

- 任务恢复行为不够可预测
- 事件重复、晚到、过期时的系统行为难解释

### 4.3 fanout checkpoint 结构弱类型

`map` 与 `loop` 当前依赖 `map[string]any` 维护内部 checkpoint。结构灵活，但约束不够，极易引入拼写错误、字段漂移和恢复兼容问题。

用户影响：

- patch/fork/replay 等能力在 fanout 节点上风险更高
- 业务需求扩展时容易引发隐性回归

### 4.4 调试与解释视图不足

当前引擎虽然有 task、node runtime、event、output 等信息，但缺少统一的“执行解释层”。

用户影响：

- 研发排查问题效率低
- 无法快速回答“为什么这个节点被跳过/重跑/拒绝恢复”

## 5. 目标用户

直接用户：

- `ai-engine` 核心开发工程师
- 负责 workflow 接入和维护的业务开发工程师
- 测试与稳定性负责人

间接受益用户：

- 上层业务团队
- 运维与排障同学
- 依赖任务恢复、fork 编辑、fanout 执行能力的产品模块

## 6. 需求范围

本期范围包含五个模块。

### 6.1 统一运行时决策模型

需求：

- 统一 `PlanAction / ExecutionReason / DirtyReason` 的定义与映射
- 明确 planning、materialize、execute、resume 各阶段职责
- 将关键优先级规则收敛为统一模型

产出：

- 运行时决策模型
- 决策矩阵文档
- 决策解释字段

### 6.2 重构恢复与 listener 契约

需求：

- 统一 async/subworkflow/fanout listener 的职责边界
- 明确 duplicate event、late event、stale child 的处理方式
- 强化 `ResumeTask` 的状态机定义与返回语义

产出：

- listener contract
- `ResumeTask` 状态图
- 标准化 rejection reason / resume reason

### 6.3 fanout checkpoint 类型化

需求：

- 为 `map` 和 `loop` 定义显式 checkpoint schema
- 封装 checkpoint 的读写与 rebuild 行为
- 约束 patch checkpoint 的合法路径

产出：

- `MapCheckpoint`
- `LoopCheckpoint`
- checkpoint schema 文档

### 6.4 统一协议与约束

需求：

- 收敛 reason 字符串、patch path 规范、checkpoint key 常量
- 禁止关键协议继续裸字符串扩散

产出：

- 统一常量定义
- patch grammar 文档
- 关键协议校验机制

### 6.5 可观测性与测试基建增强

需求：

- 为 planning、resume、listener rejection、edge closure 增加结构化解释信息
- 将现有 fake repo/fake executor/helper 抽成 testkit

产出：

- 运行解释视图
- testkit
- inspector 字段增强

## 7. 不做什么

本期不做：

- 新的视频/图片/商品业务 workflow 能力扩展
- 页面级配置平台重构
- 仅为性能而做的大规模重写
- 不带明确运行时价值的架构抽象

## 8. 成功标准

业务层成功标准：

- fork / patch / replay / resume 相关能力在核心场景下行为更可预测
- fanout 节点在 patch/reuse/recovery 场景下的回归风险显著下降

研发层成功标准：

- 能通过统一模型解释节点为何 `reuse / patch / execute`
- 能明确解释 child event 为何被接纳或拒绝
- 出现恢复问题时，排查路径可从“读代码”降低为“看结构化信息”

工程层成功标准：

- 关键 reason/key/path 不再多处裸字符串维护
- `ResumeTask` 与 listener 相关回归有更稳定的测试覆盖
- 新增引擎级测试成本下降

## 9. 验收标准

### 9.1 功能验收

- 引擎关键决策语义有统一定义
- `ResumeTask` 恢复态有清晰状态机说明
- `map / loop` checkpoint 有显式 schema
- listener 对 duplicate/late/stale 事件有一致处理策略

### 9.2 测试验收

- 现有核心测试全部通过
- 新增决策模型与恢复模型测试
- 新增 checkpoint schema 与 listener 契约测试

### 9.3 可观测性验收

- inspector 或日志中可看到关键 decision / reason / rejection 信息
- 能通过结构化数据解释主要恢复行为

## 10. 风险与依赖

主要风险：

- 运行时决策模型收敛过程中，容易影响现有 fork/replay 行为
- checkpoint 类型化可能触及较多历史路径
- listener 契约重构如果边界处理不清，可能影响现有幂等逻辑

主要依赖：

- 第一阶段测试基线已经补齐，可作为变更回归基础
- 需要 `ai-engine` 核心开发与业务 workflow 接入方共同评审

## 11. 优先级建议

### P0

- 统一运行时决策模型
- 强化 `ResumeTask` 恢复模型
- 收紧字符串协议

### P1

- fanout checkpoint 类型化
- listener 契约重构
- testkit 抽离

### P2

- inspector 与调试视图增强
- 文档与规范完善

## 12. 里程碑建议

### M1：运行时语义收敛

- 完成 reason/decision 模型设计
- 完成 planning/materialize/execute 语义对齐

### M2：恢复模型与 listener 收敛

- 完成 `ResumeTask` 状态图
- 完成 listener contract
- 完成 duplicate/late/stale 统一策略

### M3：fanout 与测试基座

- 完成 `map / loop` checkpoint 类型化
- 完成 testkit 抽离
- 完成 inspector/调试字段增强

## 13. 结论

第二阶段的核心价值，不是“新增几个节点类型”或“再接几个 workflow”，而是让 `ai-engine` 工作流引擎从当前的“复杂但可运行”，升级到“复杂但可评审、可解释、可恢复、可持续迭代”。

这是后续所有业务 workflow 能力扩展的基础工程。
