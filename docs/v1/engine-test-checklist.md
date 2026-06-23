# AI Engine Workflow Engine Test Checklist

本文档面向 `ai-engine` 工作流引擎第一阶段测试建设，目标是先把“引擎语义正确性”测稳，再扩大到业务 workflow 集成场景。

## 1. 基础编译 / 调度

- `WorkflowDefinition` 编译成功：合法 DSL 能通过 `workflow.Builder.Build` 构造成 `workflow.Workflow`
- DSL 校验失败：不存在节点的边、环路、非法 node type、非法 output 表达式要被拦截
- 拓扑顺序稳定：同一份 DSL 构建出的 `Order()` 稳定可预期
- 首次运行 runtime 初始化：所有节点均创建 `NodeRuntime`，初始状态正确
- 节点输入解析：`InputMapping`、表达式求值、上游 output 注入符合预期
- 节点输出写回：`Context.Output["nodes"]` 与 `NodeRuntime.Output` 保持一致
- 状态推进：`pending -> running -> success/failed/skipped` 符合允许的迁移约束
- 路径激活：条件边、跳过节点、不可达节点的 `ActivatedEdges` 与最终路径一致
- 任务结束态：`TaskSuccess`、`TaskFailed`、`TaskSuspended` 的落库和 final output 正确

当前缺口：
- 基础 builder/graph 校验几乎没有独立单测
- `runDAG` 的纯引擎调度断言偏少，更多依赖业务 workflow 集成测试

## 2. 挂起恢复

- async 节点挂起：异步节点调度后任务进入 `suspended`
- async 事件恢复：`node_complete_async` 到达后只恢复一次，重复事件幂等
- subworkflow 挂起：父任务在子任务创建后挂起，不能提前完成
- subworkflow 成功恢复：子任务成功后父节点 output/public output 正确回填
- subworkflow 失败恢复：失败事件唤醒父任务后由父节点自身决定失败或继续等待
- `ResumeTask` 安全性：非 `running/suspended` 任务不可恢复
- 恢复时 output 补回：resume 使用 meta/runtime output 补齐当前节点 public output
- 锁幂等：同一 task 并发恢复只有一个分支继续推进

当前缺口：
- `ResumeTask`、事件监听、幂等恢复缺少成体系单测
- 对 stale child event / duplicate async event 的保护尚未系统验证

## 3. fork reuse

- 父快照加载正确：`ForkedFrom` 能恢复 parent nodes/output
- run plan 正确：节点被判定为 `reuse / patch / execute`
- input hash 不变时复用：同输入 replay 应复用 success 节点
- input hash 变化时重跑：节点和其下游标记 `input_changed / upstream_dirty`
- resume boundary 优先级：指定 `resume_from` 时边界节点必须强制执行
- parent not success：父任务该节点非 success 时不能复用
- missing parent snapshot：父快照缺节点时应强制执行
- map item reuse：map 节点支持 item 级部分复用，且 `ReuseKind=map_items`
- dirty 元数据保留：执行后 `IsDirty / DirtyReason / ExecutionReason` 不应被意外清空

当前覆盖：
- `ai-engine/test/workflow_image_to_video_test.go`
- `ai-engine/test/image_to_video_patch_resume_test.go`

仍需补强：
- 纯引擎级 run plan 单测
- `ExecutionReason -> force execute` 的语义断言

## 4. patch

- patch path 基础能力：`set/delete/merge` 支持 object + array mixed path
- output patch：patch `NodeRuntime.Output` 后同步 `Context.Output`
- checkpoint patch：patch `Checkpoint` 后自动 rebuild public output
- patch 元数据：patched node 需要清除 injected/reuse 元数据，并标记 `patched_state`
- patch 校验：非法 node、target、op、path、merge value 必须报错
- patch/resume 关系校验：`resume_from` 只能等于 patch node 或位于其下游
- patch 只改节点态，不应误伤无关节点 runtime

当前覆盖：
- `ai-engine/engine/patch_tdd_test.go`
- `ai-engine/test/image_to_video_patch_resume_test.go`

当前缺口：
- patch validation 的纯单测还不够
- patch 对 run plan 的影响缺少白盒断言

## 5. map / loop / subworkflow

- map empty fast path：空数组直接成功返回空结果
- map checkpoint 初始化：`total/done/results/item_hashes/reused_items` 正确创建
- map existing children fan-in：已完成子任务可重新聚合
- map child failed：任一子任务失败时 map 失败并要求父级处理
- map item reuse：父快照 item hash 未变时复用对应 item
- loop 初始化：`current_index/running_index/carry_state` 状态机正确
- loop running child reconcile：子任务 success/failed/suspended 分支正确
- loop carry 透传：本轮 output 到下一轮 input 的 carry 行为正确
- subworkflow 幂等：相同 `subKey` 不重复创建子任务
- subworkflow success direct return：普通 subworkflow 成功时可以直接返回 final
- fanout node 唤醒策略：map/loop 的 child success/failed 只唤醒父任务，不直接在 listener 完成父节点

当前缺口：
- `loop_node_step.go` 目前几乎没有独立测试
- `subworkflow.go` 的 `subKey` 幂等和状态分支缺少纯单测

## 第一批高价值测试

第一批优先补这些，因为它们对引擎正确性影响最大，且依赖少、反馈快：

- 修复 `ai-engine/engine/patch_tdd_test.go`，恢复为可编译可运行的基线测试
- 增加 patch validation 单测：覆盖非法 patch target/op/path、非法 resume relation
- 增加 resume boundary / execution reason 单测：保证 boundary 和 downstream dirty 不会被 hash shortcut 掉
- 增加 map checkpoint rebuild 单测：确保 checkpoint patch 后 public output 始终同步

## 第二批建议

- 为 `BuildRunPlan` / `MaterializeRunPlan` 补白盒测试
- 为 `ResumeTask` 和 async/subworkflow listener 补幂等恢复测试
- 为 `LoopNodeStep` 补状态机级测试
- 为 `map/loop/subworkflow` 建立一套轻量 fake repo/fake executor 测试基座
