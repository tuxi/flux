# Engine DAG Runtime V2 Plan

## 背景

当前 Engine 已经具备 DAG 工作流的核心能力：节点拓扑、动态激活边、条件分支、fan-out/fan-in、节点状态持久化、checkpoint、resume、fork、redo、replay、Map/Loop/SubWorkflow/Async 等执行形态。

本次问题来自 `goods_video_timeline_v1` 中 `prepare_shot_assets` 的 `video_script` 为 `nil`。实际任务 `2053069726698979328` 中，`validate_shot_plan` 被手动状态修改或恢复流程影响后处于 `skipped`，其出边全部被关闭，但 `prepare_shot_assets` 仍因其他两个激活父节点成功而运行，最终从 `validate_shot_plan.video_script` 解析到 `nil`。

这个现象暴露了两类问题：

- 当前 Engine 的 join 语义是“所有激活入边成功”，不是“所有父节点成功”。
- DSL 的数据依赖和控制依赖没有显式区分，节点可能引用一个被 skipped 的上游输出。

## 当前 Engine 语义

### Fan-out

一个节点可以连接多个子节点。

```go
{From: "validate_shot_plan", To: "build_subtitle_timeline", Type: definition.EdgeNormal}
{From: "validate_shot_plan", To: "tts_router", Type: definition.EdgeNormal}
{From: "validate_shot_plan", To: "prepare_shot_assets", Type: definition.EdgeNormal}
```

普通边表示上游成功后所有出边都会被激活。它不是条件分支，也不需要额外条件。

条件边适合表达互斥或选择分支：

```go
{From: "cache_lookup", To: "cache_hit_return", Type: definition.EdgeCondition, Condition: "cache_lookup.hit == true"}
{From: "cache_lookup", To: "generate", Type: definition.EdgeCondition, Condition: "cache_lookup.hit == false"}
```

### Fan-in / Join

当前 `depsMet` 只检查激活入边：

```go
for _, p := range dag.Parents[node] {
    key := p + "->" + node
    activated, ok := ctx.ActivatedEdges[key]
    if !ok {
        panic(...)
    }
    if !activated {
        continue
    }
    if ctx.Runtime[p].State != domain.NodeSuccess {
        return false
    }
}
return true
```

因此 join 语义是：

> 子节点等待所有激活入边上的父节点成功；未激活入边不参与依赖判断。

这是支持条件分支汇聚的必要语义。否则未被选择的分支会永久阻塞下游 join。

### Skip 传播

节点被 skipped 时，`finalizeSkippedNode` 会关闭该节点所有出边：

```go
for _, edge := range dag.Edges[nodeName] {
    key := nodeName + "->" + edge.To
    ctx.ActivatedEdges[key] = false
}
```

如果下游还有其他激活且成功的父节点，下游可能继续执行。这对控制流是合理的，但对数据依赖存在风险。

### 同步节点执行模型

当前主执行器支持 DAG fan-out/fan-in，但同步节点不是严格并发执行。

`runDAG` 按拓扑顺序扫描节点：

1. 找到 `pending` 节点。
2. 检查 `shouldSkipNode`。
3. 检查 `depsMet`。
4. 转为 `ready`。
5. 直接调用 `executeNode`。
6. 执行完成后 `finalizeNode`，计算出边。

所以多个兄弟同步节点在拓扑上可以同时 ready，但主循环通常仍按顺序执行。当前真正具备并发语义的是：

- `NodeMap`：按 items fan-out 子任务，并通过 `parallel` 控制并发。
- `NodeLoop`：带循环语义和 checkpoint。
- `NodeSubWorkflow`：封装子工作流。
- `AsyncExecution`：发布异步 job 后挂起，等待 worker/callback/poll 恢复。

## 当前问题总结

### 控制依赖和数据依赖混用

例如：

```go
"video_script": "validate_shot_plan.video_script"
```

如果 `validate_shot_plan` 被 skipped，这个表达式会得到 `nil`。但下游是否可执行只由激活边决定，不会检查 input mapping 是否引用了 skipped 节点。

### 手动修改 DB 风险

手动修改节点状态容易造成以下不一致：

- 节点 state 与 `activated_edges_json` 不一致。
- 子节点 skipped 早于父节点 success。
- output/checkpoint/input_hash 没有按状态同步清理。
- 下游节点读取到旧 output 或 nil output。

因此手动改状态不能只改 `state`，需要同步修复边闭包、output、checkpoint、下游 dirty 状态。

## V1 稳定性补强

在推进 V2 并发调度前，建议先完成以下补强。

### 1. DSL fallback 规范

当下游引用一个可能被 skipped 的中间节点时，必须提供 fallback。

```go
"video_script": "validate_shot_plan.video_script ?? generate_goods_script_pro.video_script"
```

同理适用于：

- `voiceover_text`
- `voiceover_plan`
- `subtitle_plan`
- 其他由可选质检、缓存、条件分支节点产生的字段

### 2. Required input fail-fast

工具输入 schema 应该准确表达必填字段。对于 `prepare_goods_shot_assets_v2` 这类强依赖脚本结构的工具，`video_script` 应该 required，或 parse 阶段明确返回错误。

目标是避免 nil input 静默进入业务逻辑。

### 3. 状态闭合校验

增加 task runtime validator，用于 resume / redo / fork / replay 前检查：

- terminal 节点是否都有完整 `activated_edges_json`。
- pending 节点的所有父边是否已经被决策。
- skipped 节点是否仍被下游 input mapping 直接引用且没有 fallback。
- success 节点是否有 output hash。
- running/ready 节点是否为合法恢复状态。

### 4. Repair 工具

提供内部 repair 能力，替代手动 DB 修改：

- 从某个节点开始 reset 子图。
- 清理下游 output/checkpoint/input_hash。
- 重置下游 activated edges。
- 保留或清理 cost record 的策略明确化。
- 重新 materialize dirty plan。

### 5. Fork / Redo / Resume 入口闭合校验

当前运行时 DAG 执行阶段有较好的闭包保证：

- `ensureEdgeClosure`：节点成功执行后检查所有出边是否已决策。
- `failClosure`：失败时关闭失败路径并传播 skip。
- `globalClosure`：DAG 停滞时扫描 pending 节点，尝试做死锁恢复。

但这些保证只作用于当前内存执行过程。fork / redo / resume 入口目前没有对父任务状态做系统一致性校验：

- `rebuildActivatedEdges` 直接加载历史边状态，不检查 terminal 节点的出边是否完整。
- `ForkTask` 不拒绝 running、pending 或半闭合任务。
- `PreviewRunPlan` / `BuildRunPlan` 依赖 parent snapshot，但没有先证明 snapshot 闭合。
- `validateResumeSpec` 只检查 patch 语法和节点是否存在。
- `PrepareTaskRetry` 主要检查 task 级别状态是否可恢复。

建议新增 `validateParentStateClosure`，在 `PreviewRunPlan` / `BuildRunPlan` 之前调用，并覆盖 fork / redo / resume 三类路径。

输入：

- parent task
- parent snapshot
- workflow definition
- workflow graph

检查项：

1. activated edges 完整性

   遍历所有 terminal 节点：`success`、`skipped`、`failed`。对照 workflow graph，检查该节点定义中的每条出边是否都在 `ActivatedEdges` 中有记录。

2. 非 terminal 节点扫描

   如果父任务中存在 `pending`、`ready`、`running`、`retrying`、`success_pending_edges`、`failed_pending_edges` 等非闭合状态，应拒绝 fork / redo，或要求先 normalize。

3. skipped 上游引用检查

   遍历所有节点的 `input_mapping` 表达式。如果表达式引用了 skipped 节点的输出，且该表达式没有 fallback，或 fallback 解析后仍为空，应返回明确错误。典型问题：

   ```go
   "video_script": "validate_shot_plan.video_script"
   ```

   应改为：

   ```go
   "video_script": "validate_shot_plan.video_script ?? generate_goods_script_pro.video_script"
   ```

4. pending 节点 parent edge decisions

   如果允许从 suspended / partial task resume，则 pending 节点的所有 parent edges 必须已经有边决策。缺失边决策说明 parent snapshot 不闭合，继续 plan 会产生不可解释的路径。

5. 不闭合拒绝

   以上任一 block 级问题失败时，返回明确错误，告知客户端该任务状态不闭合，不能 fork / redo / resume，需要先 repair / normalize。

问题分级：

- Warn：可自动修复。例如 terminal 节点缺失某条出边记录，且能根据当前节点状态安全推断为 `false`。
- Block：必须拒绝。例如存在 running/pending 父任务节点、skipped 上游被无 fallback input mapping 引用、pending 节点缺少 parent edge decisions。

建议错误结构：

```go
type ClosureValidationIssue struct {
    Level     string // warn | block
    NodeName  string
    EdgeKey   string
    FieldName string
    Message   string
}
```

建议先只做 block，不做自动 repair。repair 应作为显式管理动作，避免 fork / redo / resume 入口悄悄改历史任务状态。

## V2 目标

V2 的目标不是替代 Map/Loop/SubWorkflow/Async，而是让 DAG 层面的独立兄弟节点可以受控并发执行。

示例：

```text
        A
      / | \
     B  C  D
      \ | /
        E
```

在 V1 中，`B/C/D` 通常按拓扑顺序串行执行。在 V2 中，如果它们满足并发安全条件，可以同时执行，`E` 等待所有激活入边成功后继续。

## V2 非目标

- 不用兄弟节点并发替代 `NodeMap`。
- 不改变条件分支的激活边 join 语义。
- 不默认让所有同步节点并发。
- 不允许未声明并发安全的副作用节点盲目并发。
- 不牺牲 resume/fork/redo/replay 的确定性和可解释性。

## 为什么 Map 仍不可替代

Map 的价值不只是并发。它还提供：

- item 级 fan-out/fan-in。
- item 顺序和 `results[]` 聚合语义。
- `map_index` / `sub_key` / parent node 关联。
- item 级 checkpoint。
- partial reuse。
- item 级失败恢复。
- 独立子任务生命周期。

兄弟节点并发解决的是 DAG 层面的独立步骤并行；Map 解决的是集合数据处理并行。两者应该共存。

## V2 设计方向

### 1. Ready 队列

将当前“扫描到 ready 就立即执行”的模型，改为：

1. 扫描 DAG。
2. 收集本轮所有 ready nodes。
3. 根据并发策略放入 ready queue。
4. worker pool 执行节点。
5. 调度线程串行处理完成事件。

### 2. 显式 opt-in

初期默认保持 V1 串行行为。节点必须显式声明并发安全：

```go
Config: map[string]any{
    "parallel_safe": true,
}
```

也可以在 workflow 级别增加开关：

```go
Config: map[string]any{
    "enable_parallel_sync_nodes": true,
    "max_parallel_sync_nodes": 3,
}
```

### 3. 并发上限

至少需要以下层级的限制：

- workflow run 级并发上限。
- 全局同步节点并发上限。
- 按 node type / tool type 的并发上限。
- 按 provider/resource 的限流，例如 LLM、TTS、视频生成、上传。

### 4. 状态原子抢占

多 worker 场景下，节点从 `pending` 到 `ready/running` 需要 CAS：

```sql
update task_nodes
set state = 'ready'
where task_id = ? and node_name = ? and state = 'pending'
```

只有更新成功的 worker 才拥有执行权。

### 5. 执行并发，finalize 串行

建议第一版只并发执行 `executeNode` 的业务主体，但以下操作仍由调度线程串行处理：

- `finalizeNode`
- `computeEdges`
- `finalizeSkippedNode`
- `skipSubtree`
- `globalClosure`
- progress 更新
- cost record
- task terminal 判断

这样可以显著降低 `ActivatedEdges`、skip 传播、join 判定的 race 风险。

### 6. Completion event

worker 执行完成后，不直接修改 DAG 边闭包，而是返回完成事件：

```go
type NodeCompletion struct {
    NodeName string
    Output   map[string]any
    Err      error
    Suspended bool
}
```

调度线程消费 completion，串行推进状态机。

### 7. Suspend 语义

并发执行后，某个节点进入 await/async suspend 时，需要定义清楚：

- 是否立即停止调度新的 ready 节点。
- 已经 running 的兄弟节点是否允许完成。
- task 状态何时转为 suspended。
- resume 时如何识别未完成 running 节点。

建议策略：

- 一旦出现 suspend，不再调度新节点。
- 已经 running 的同步节点允许收尾。
- 所有 completion 串行 finalize 后，task 进入 suspended。

### 8. Failure 语义

某个并发节点失败时：

- 停止调度新的 ready 节点。
- 已 running 节点允许完成或按可取消能力取消。
- 失败节点 finalize 后，关闭失败路径。
- 下游是否 skip 由现有 closure 规则处理。

需要额外记录 failure barrier，避免失败后继续扩散调度。

### 9. Context 并发审计

需要重点审计：

- `Context.Output`
- `Context.Runtime`
- `Context.ActivatedEdges`
- `buildNodeInput`
- `SetNodeOutput`
- `UpdateNodeStatus`
- `EvalAny` / `EvalBool`
- cost record
- event publish 顺序

原则：并发 worker 可以读快照和写自身 runtime input/output，但 DAG 边状态和下游状态只能由调度线程修改。

## 对 Retry / Resume / Redo / Fork / Replay 的影响

### Retry

并发后，一个节点失败时，其他兄弟节点可能已经成功或仍在 running。retry 必须明确：

- 只重试失败节点。
- 还是重试失败节点影响的 dirty 子图。
- 是否复用已成功兄弟节点 output。
- 失败节点的外部副作用是否幂等。

### Resume

恢复时会更常见多个节点处于 `running`、`ready`、`awaiting`。需要区分：

- running 且 heartbeat 失联：重置为 pending/retrying。
- awaiting：保留 await binding。
- async scheduled：根据 async job/callback 状态恢复。
- ready 但未执行：可回到 pending 或继续 ready。

### Redo / Replay

并发会让完成顺序不稳定。Replay 不能依赖事件顺序，而应该依赖：

- input hash
- output hash
- activated edges
- node terminal state
- checkpoint

有外部副作用的节点需要 idempotency key。

### Fork

Fork 应尽量只允许基于状态闭合的 task：

- success
- failed 且失败边闭合
- suspended 且 awaiting 状态清晰

对于 running 或手动修改过的半闭合任务，应先 repair/normalize，再 fork。

### Redo

Redo 需要从 redo 起点开始：

- 标记 dirty 子图。
- 清理下游 output/checkpoint/input_hash。
- 清理或重算 activated edges。
- 保留可复用兄弟分支。

并发执行后，这部分逻辑需要更加严格，否则容易混合旧输出和新输出。

## V2 分阶段计划

### Phase 0：V1 稳定性补强

- 增加 DSL fallback 规范。
- 工具 input schema required 化。
- 增加 task runtime validator。
- 增加 reset/repair 工具，替代手动 DB 修改。
- 增加 engine test cases 覆盖 skipped upstream + fallback。

### Phase 1：Ready 收集但仍串行执行

- 重构 `runDAG`，先收集 ready nodes。
- 保持串行执行，验证行为不变。
- 增加日志：每轮 ready set、activated edge decisions、skip decisions。
- 为后续 worker pool 打基础。

### Phase 2：Opt-in 并发执行

- 增加 workflow 级开关。
- 增加 node 级 `parallel_safe`。
- worker pool 执行业务节点。
- completion event 回到调度线程串行 finalize。
- 默认并发上限较小，例如 2 或 3。

### Phase 3：恢复与失败语义完善

- 并发场景下的 retry/resume 测试。
- suspend barrier。
- failure barrier。
- heartbeat scanner 适配多个 running 节点。
- fork/redo/replay 的闭合校验。

### Phase 4：扩大节点类型覆盖

- 从纯计算/路由/本地构造类节点开始。
- 再扩展到 LLM 分析类节点。
- 最后谨慎评估上传、缓存写入、外部生成提交、扣费类节点。

## 测试清单

### DAG fan-out/fan-in

- 一个普通节点 fan-out 到多个普通子节点。
- 多个普通父节点 fan-in 到一个子节点。
- 条件分支 fan-in，未激活边不阻塞 join。
- skipped 上游被下游 input mapping 引用时 fail-fast 或 fallback。

### 并发调度

- 多个 ready 节点同时执行。
- 并发上限生效。
- 未声明 `parallel_safe` 的节点保持串行。
- 一个节点完成后激活新边，新 ready 节点进入下一轮调度。

### 状态恢复

- 并发运行中进程退出。
- 一个 running 节点 heartbeat 失联。
- 一个 async 节点 suspended，兄弟同步节点完成。
- failed 节点与 success 兄弟节点混合恢复。

### Fork / Redo / Replay

- 从 terminal task fork。
- 从 suspended task fork。
- 从不闭合 task fork 被拒绝。
- redo 起点下游 dirty 子图重算。
- replay 不依赖节点完成顺序。

## 结论

当前 Engine 已经具备 DAG fan-out/fan-in 的控制流基础，但主执行器同步节点仍是顺序推进。V2 可以在现有基础上演进到受控兄弟节点并发，但必须先补齐状态闭合校验、repair 工具、input fallback 规范和并发安全边界。

建议优先完成 V1 稳定性补强，再以 opt-in、低并发、finalize 串行的方式推进 V2。这样可以获得 DAG 并发收益，同时尽量不破坏 retry、resume、redo、fork、replay 这些已有核心能力。
