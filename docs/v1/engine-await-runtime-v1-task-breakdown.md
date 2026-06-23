# AI Engine Await/Receive Runtime V1 开发任务清单

日期：2026-04-23

关联文档：

- [AI Engine Await/Receive Runtime V1 产品需求文档（PRD）](/Users/xiaoyuan/Documents/work/git/dream-ai-webhook-workflow/ai-engine/docs/engine-await-runtime-v1-prd.md)
- [AI Engine 第二阶段产品需求文档（PRD）](/Users/xiaoyuan/Documents/work/git/dream-ai-webhook-workflow/ai-engine/docs/engine-phase2-prd.md)

## 说明

本清单按 `P0 / P1` 拆分，目标是直接作为 `await / receive` 能力开发排期与任务拆解基础使用。

每个任务包含：

- 任务目标
- 核心改动点
- 验收标准

本清单中的 P0 任务，构成 Await Runtime V1 的最小可交付能力。

## P0 任务

## P0-1 引入 Await 节点类型与 DSL Schema

任务目标：

- 在工作流 DSL 中正式引入 `await` 节点，表达“等待外部事件/输入”的一等语义

核心改动点：

- 在 `definition.NodeType` 中新增 `NodeAwait`
- 在 node registry 中注册 `await` 节点工厂
- 增加 `await` 节点对应的 `NodeTypeSchema`
- 定义 V1 支持的 config 字段：
  - `await_type`
  - `source`
  - `provider`
  - `signal_name`
  - `correlation`
  - `completion`
  - `fallback_poll`
  - `timeout_seconds`
- 为 `await` 的 DSL 做 builder 级校验
- 保持 edge condition 语义不变，不在 `await` 节点内部引入分支规则

验收标准：

- `workflow.Builder.Build` 可以正确编译 `await` 节点
- 非法 `await` config 能在编译阶段被拒绝
- 至少有一个测试 workflow 可包含 `await` 节点并通过构建

## P0-2 增加 AwaitBinding 数据模型与 Repository

任务目标：

- 为等待订阅、外部事件路由、fallback poll 调度建立独立持久化模型

核心改动点：

- 新增 `domain.AwaitBinding`
- 新增 `domain/entity/await_binding.go`
- 新增 `repository.AwaitBindingRepository`
- 新增 `repository/query/await_binding.go`
- 落地 `await_bindings` 表结构
- 实现核心查询接口：
  - `GetByTaskAndNode`
  - `FindWaitingByProviderTaskID`
  - `FindWaitingByAPITaskID`
  - `FindWaitingBySignal`
  - `ClaimCompleting`
  - `FindPollDue`
  - `FindTimeoutDue`
- 建立 PRD 中定义的关键索引

验收标准：

- 数据模型与 PRD 一致
- webhook / signal / poll 所需的查找路径都能通过 repository 支撑
- `ClaimCompleting` 具备基本幂等抢占能力

## P0-3 收口 Await 相关状态机与统一迁移入口

任务目标：

- 确保 await 引入后，task / node / binding 的状态更新全部走显式状态机，避免与现有流转冲突

核心改动点：

- 为 `NodeRuntime` 增加 `awaiting` 的合法状态迁移
- 为 `AwaitBinding` 建立显式状态机与 transition / claim 接口
- 明确 `Task` 在 await 场景中的合法状态迁移
- 收口以下入口中的直接状态修改：
  - await 节点初次挂起
  - webhook handler
  - signal handler
  - poll scanner
  - timeout scanner
- 禁止在上述入口中直接赋值 `status/state`

验收标准：

- await 相关新增状态流转全部通过统一 transition 入口完成
- 非法状态迁移会被拒绝并留下结构化错误
- 不会因为 await 引入而破坏现有 `pending / ready / running / success_pending_edges / failed_pending_edges` 语义

## P0-4 新增 NodeAwaiting 状态与节点挂起语义

任务目标：

- 让工作流运行时正式支持“节点进入等待态，任务进入挂起态”

核心改动点：

- 在 `domain.NodeState` 中新增 `NodeAwaiting`
- 定义 `NodeAwaiting` 的合法状态迁移
- 为 `await` 节点执行引入以下语义：
  - 解析 correlation
  - 创建 AwaitBinding
  - 节点状态进入 `awaiting`
  - 任务状态进入 `suspended`
- 调整引擎调度逻辑，使 `awaiting` 被视为等待中的合法中间态，而不是异常态

验收标准：

- `await` 节点执行后不会继续向下游推进
- task 会进入 `suspended`
- node runtime 会进入 `awaiting`
- 重启或恢复时不会把 `awaiting` 当成普通失败或 running 残留态误处理

## P0-5 实现 Await 节点执行器与 Binding 创建流程

任务目标：

- 为 `await` 节点建立最小可运行执行器

核心改动点：

- 新增 `workflow/nodes/await_step.go`
- 新增 `workflow/nodes/await_node_factory.go`
- `await` 节点执行时：
  - 从 `InputMapping` 和 `config.correlation` 解析相关性键
  - 构造 `AwaitBinding`
  - 将必要快照写入 binding
  - 返回 `WorkflowSuspendedError`
- 为 `await` 节点定义输出 schema 约束
- 明确 `completion.output_mapping` 的表达式上下文

验收标准：

- `await` 节点可独立执行并创建 binding
- 同一个 task/node 重复执行不会生成多个活跃 binding
- `await` 节点最小执行链具备单元测试

## P0-6 建立统一的 Await 完成入口

任务目标：

- 将 webhook / signal / poll 的完成动作统一收敛到一个引擎恢复入口

核心改动点：

- 新增引擎侧 `CompleteAwaitNode` 能力
- 统一以下步骤：
  - 查 binding
  - 抢占 `completing`
  - 归一化事件 payload
  - 将 node runtime 转为 `success_pending_edges` 或 `failed_pending_edges`
  - 调用 `ResumeTask`
  - 回写 binding 最终状态
- 与现有 `AttemptCompletePendingEdges` 对齐，形成双层幂等保护
- 任务、节点、binding 的状态更新必须复用 `P0-3` 中定义的统一状态迁移入口

验收标准：

- 不同来源的 await 完成能复用同一条核心逻辑
- 同一 binding 被重复完成时，不会重复推进 DAG
- 引擎恢复后行为与现有 `ResumeTask` 一致

## P0-7 新增 Webhook Handler 主链路

任务目标：

- 建立“webhook 到来先查 AwaitBinding，再进入 ResumeTask”的标准链路

核心改动点：

- 新增统一 webhook handler 框架
- 支持 provider payload 解析和验签扩展点
- handler 主路径：
  - 验签
  - 解析 correlation key
  - 查 `AwaitBinding`
  - `ClaimCompleting`
  - 归一化 payload
  - 调 `CompleteAwaitNode`
- 为未命中 binding、重复事件、过期 binding 建立标准返回行为

验收标准：

- webhook 主链路不直接扫 `tasks` 或 `task_nodes`
- webhook 重复投递不会重复恢复同一节点
- 至少一个 provider 的 webhook 路由可跑通完整链路

## P0-8 新增 Signal Handler 主链路

任务目标：

- 支撑“等待用户输入/外部业务输入”的最小唤醒能力

核心改动点：

- 新增 signal handler
- 支持按 `signal_name + callback_token` 查找 binding
- signal 命中后复用 `CompleteAwaitNode`
- 定义最小 signal payload 协议

验收标准：

- 可以通过 signal 唤醒一个处于 `awaiting` 的节点
- 用户选择类场景的最小链路可以通过后端接口模拟完成

## P0-9 迁移一条试点 Workflow 到 Await 模型

任务目标：

- 用一条真实 workflow 证明 `await` 模型可替代旧的 `wait tool`

核心改动点：

- 选定一条试点 workflow，例如：
  - `motion_control/kling`
  - 或 `goods_shot_i2v`
- 将原有：
  - `submit(tool sync) -> wait(tool async)`
  - 迁移为
  - `submit(tool sync) -> await(node await)`
- submit 节点仅负责创建外部任务和输出 task id
- await 节点负责等待 webhook / poll

验收标准：

- 至少一条生产候选 workflow 完成试点迁移
- 迁移后业务输出与旧流程一致
- webhook 成为主路径，poll 不再是高频主路径

## P0-10 建立 Await Runtime 基础测试集

任务目标：

- 为 Await Runtime V1 建立最小可信测试基线

核心改动点：

- 新增以下测试场景：
  - `await` 节点创建 binding 并挂起
  - await 相关非法状态迁移被拒绝
  - webhook 命中 binding 并恢复
  - signal 命中 binding 并恢复
  - 重复 webhook 幂等
  - webhook 与 poll 并发命中幂等
  - timeout 场景
  - binding 丢失或命中不到的场景
- 为 `ClaimCompleting` 和 `AttemptCompletePendingEdges` 增加联动测试

验收标准：

- Await 主链路具备端到端测试
- 重复事件、过期事件、missing binding 有稳定测试覆盖

## P1 任务

## P1-1 增加 Fallback Poll 调度器

任务目标：

- 将 poll 从 wait tool 内阻塞轮询，升级为 AwaitBinding 维度的低频补偿调度

核心改动点：

- 新增 Await poll scanner / worker
- 定时扫描 `status=waiting AND next_poll_at <= now`
- 调用 poll tool 查询外部状态
- 命中后复用 `CompleteAwaitNode`
- 未命中时更新：
  - `poll_attempts`
  - `last_polled_at`
  - `next_poll_at`

验收标准：

- poll 成为 webhook 的补偿手段，而不是主执行路径
- 扫描与完成链路可独立观测

## P1-2 增加 Await Timeout 扫描器

任务目标：

- 让等待点具备超时收敛能力

核心改动点：

- 扫描 `timeout_at <= now` 的 waiting binding
- 将 binding 置为 `timed_out`
- 将 node runtime 置为 `failed_pending_edges`
- 通过 `ResumeTask` 让流程进入失败或补偿分支

验收标准：

- 超时等待不会永久卡住任务
- timeout 行为在 task/node/binding 三层状态上都可解释

## P1-3 扩展 Inspector / 调试视图

任务目标：

- 提升 Await Runtime 的可观测性与排障效率

核心改动点：

- inspector 增加 await binding 视图
- 展示：
  - await_type
  - source
  - correlation key
  - binding status
  - last event
  - next poll
  - timeout at
- 为 webhook/signal reject 增加结构化日志或事件

验收标准：

- 出现唤醒问题时，能快速判断是：
  - 没创建 binding
  - webhook 没命中
  - 幂等被拒绝
  - timeout
  - poll 未到期

## P1-4 收缩外部等待型 AsyncExecution 使用范围

任务目标：

- 明确 `tool.AsyncExecution` 与 `await` 的职责边界

核心改动点：

- 梳理现有 async tool
- 区分：
  - 内部异步执行型
  - 外部等待型
- 为外部等待型 async tool 标记迁移路径
- 更新开发规范，禁止新增以“高频轮询 provider”为主路径的 wait tool

验收标准：

- 新能力接入时，等待外部世界的场景优先使用 `await`
- `tool.AsyncExecution` 的职责边界更清晰

当前进展：

- 已完成仓库扫描与分类，详见 [await-async-migration-inventory.md](./await-async-migration-inventory.md)
- 当前结论是：
  - `merge_video` 属于应保留的内部异步执行型
  - `video_generate_wait`、`goods_shot_i2v_wait` 已完成主路径迁移，当前以 `await` 为主模型，legacy wait tool 仅保留为 fallback poll
  - `kling_motion_wait`、`volc_motion_wait` 已降级为 fallback poll tool
  - `aliyun_image_generate_wait`、`volcengine_image_generate_wait`、`aliyun_image_to_image_wait` 已完成主路径迁移，当前以 `await` 为主模型，legacy wait tool 仅保留为 fallback poll
  - `submit` 与 `await` 的拆分约束已明确，后续迁移不得将第三方任务创建与等待恢复揉成同一个可重试节点
  - 阿里百炼已补 `EventBridge -> AwaitBinding -> 单次补查 -> CompleteAwaitNode` 接入骨架，provider 回调模型边界已明确
  - `poll_once` 规范设计已完成，详见 [await-poll-once-tool-design.md](./await-poll-once-tool-design.md)
  - 第一批 `poll_once` 已落地：
    - `aliyun_image_generate_poll_once`
    - `aliyun_image_to_image_poll_once`
    - `volcengine_image_generate_poll_once`
    - `video_generate_poll_once`
    - `goods_shot_i2v_poll_once`
  - `text_to_image`、`image_to_image`、`style_transfer`、`text_to_video`、`image_to_video`、`goods_shot_i2v_generate` 的 fallback poll 已切换到对应 `poll_once`
  - 运行时已增加 `poll_once` 优先解析层：scanner / replay / eventbridge 在读取旧 `wait` 名称时，也会优先自动命中对应的 `poll_once`

下一步：

- 继续将剩余 legacy wait tool 从“兼容型 fallback poll”收口为单次查询型 tool
- 继续扩大 `poll_once` 优先策略覆盖面，并逐步清理对 legacy wait tool 名称的兼容依赖
- 补充 `eventbridge / replay / poll` 统一观测与调试视图的最后一段细化

## P1-5 增加用户选择 / 审批场景 Demo Workflow

任务目标：

- 证明 `await` 模型不仅适用于 webhook，也适用于人机交互型等待

核心改动点：

- 新增一个 demo workflow，例如：
  - `await_user_choice_demo`
- 节点执行到 `await` 后挂起
- 通过 signal 提交选择
- 利用 edge condition 走不同分支

验收标准：

- 至少有一条 demo workflow 能证明：
  - `await` 不是 provider 专用能力
  - 它是统一的等待-唤醒抽象

## P1-6 增加 Dev Replay / Local Webhook Simulation

任务目标：

- 在本地没有公网 webhook 的情况下，仍然能够按统一 webhook 模型联调 `await` 主路径

核心改动点：

- 增加内部 replay / emulate service
- 可选增加开发态 HTTP 调试入口
- replay 通过 poll 或 payload 回放构造 provider webhook 语义
- 复用统一 normalize / `AwaitBinding` 命中 / `CompleteAwaitNode`
- 为 replay 增加结构化事件与必要的环境隔离

验收标准：

- 本地联调不强依赖真实公网 webhook
- replay 不直接修改 task/node/binding 状态
- 本地开发路径尽量复用生产 webhook 主链路

设计说明：

- 详细设计见 [await-webhook-dev-replay-design.md](./await-webhook-dev-replay-design.md)

## 任务依赖建议

建议依赖顺序：

1. `P0-1` 引入 Await 节点类型与 DSL Schema
2. `P0-2` 增加 AwaitBinding 数据模型与 Repository
3. `P0-3` 收口 Await 相关状态机与统一迁移入口
4. `P0-4` 新增 NodeAwaiting 状态与节点挂起语义
5. `P0-5` 实现 Await 节点执行器与 Binding 创建流程
6. `P0-6` 建立统一的 Await 完成入口
7. `P0-7` 新增 Webhook Handler 主链路
8. `P0-8` 新增 Signal Handler 主链路
9. `P0-9` 迁移一条试点 Workflow 到 Await 模型
10. `P0-10` 建立 Await Runtime 基础测试集
11. `P1-1` 增加 Fallback Poll 调度器
12. `P1-2` 增加 Await Timeout 扫描器
13. `P1-3` 扩展 Inspector / 调试视图
14. `P1-4` 收缩外部等待型 AsyncExecution 使用范围
15. `P1-5` 增加用户选择 / 审批场景 Demo Workflow

## 建议排期方式

如果按两期推进：

- 第一期：完成全部 P0
- 第二期：完成 P1-1 / P1-2 / P1-3 / P1-5

如果按三期推进：

- 第一期：P0-1 / P0-2 / P0-3 / P0-4 / P0-5
- 第二期：P0-6 / P0-7 / P0-8 / P0-10
- 第三期：P0-9 / P1-1 / P1-2 / P1-3 / P1-4 / P1-5

## 里程碑建议

### M1 Await Runtime Skeleton

完成标志：

- `await` 节点可编译
- `AwaitBinding` 可创建
- task/node 可进入挂起等待态

对应任务：

- `P0-1`
- `P0-2`
- `P0-3`
- `P0-4`
- `P0-5`

注：

- `P0-3` 是 Await Runtime V1 的硬性前置约束，必须先把状态迁移入口收口，再继续推进 handler 和 completion 链路

### M2 External Wakeup Path

完成标志：

- webhook / signal 能通过 binding 路由并恢复任务

对应任务：

- `P0-6`
- `P0-7`
- `P0-8`
- `P0-10`

### M3 Business Pilot

完成标志：

- 至少一条业务 workflow 完成试点迁移

对应任务：

- `P0-9`

### M4 Await Runtime Hardening

完成标志：

- timeout / poll / inspector / demo workflow 补齐

对应任务：

- `P1-1`
- `P1-2`
- `P1-3`
- `P1-4`
- `P1-5`

## 建议会议输出

评审会后建议形成三项输出：

- Await Runtime V1 是否通过立项
- P0 是否整体承诺进入开发排期
- 试点 workflow 选择哪一条作为 V1 业务验证路径
