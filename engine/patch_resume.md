# 引擎 patch / resume 设计规范

1. 设计目标

将当前工作流引擎从“只支持 task input 级重跑”，升级为支持：
•	基于 checkpoint / state 的局部编辑
•	基于 fork 的非破坏性重做
•	从指定节点开始 resume
•	保持 DAG 依赖语义一致
•	保持 lineage 清晰可追踪
•	保持复用、dirty 传播、map item reuse 仍然成立

核心原则只有一句话：

> 不直接修改目标节点的输入，而是修改其上游节点的输出或 checkpoint 状态；然后从指定 resume 节点开始重新执行，使所有下游节点都消费一致的新状态。

2. 核心原则

2.1 patch 的对象不是 node input，而是 node output / checkpoint

用户表面上可能说：
•	修改 enhance_prompt 的 parsed_intent
•	修改 video_generate_submit 的 prompt
•	修改某一步 AI 理解结果

但引擎内部不应该直接改目标节点的 resolved input。

正确做法是：
•	找到这个 input 的来源节点
•	patch 来源节点的 Output 或 Checkpoint
•	从目标消费节点开始 resume

例子：
```json
{
  "mode": "node_patch",
  "resume_from": "enhance_prompt",
  "patches": [
    {
      "target": "node_output",
      "node": "reconstruct_intent",
      "path": "intent",
      "value": {
        "scene": "sunset beach",
        "motion": "slow pan"
      }
    }
  ]
}
```

这里用户看起来是在“改 enhance_prompt 的 parsed_intent”，
但引擎内部实际是：
•	patch reconstruct_intent.output.intent
•	从 enhance_prompt 开始往后重跑

2.2 不破坏 DAG 依赖语义

节点输入必须始终通过：
•	InputMapping
•	Config
•	上游节点 Output
•	task input fallback

来重新构造。

不能做这种事：
•	手动往 enhance_prompt 塞一个假的 input
•	下游节点看到的是 patched input，但别的依赖节点看到的是旧上游 output

否则会导致状态分叉。

2.3 用户看到的是“改输入”，引擎执行的是“改上游输出”

产品层可以包装得更自然：
•	“修正 AI 对画面的理解”
•	“修改提示词理解结果”
•	“调整这一步的输入参数”

但引擎层必须坚持：
•	patch state
•	rebuild input
•	resume downstream

3. 运行时模型

3.1 Task 侧新增能力

Task 需要支持一次 fork/edit run 携带编辑意图。

建议新增：
```go
type RuntimePatchTarget string

const (
	PatchTargetNodeOutput     RuntimePatchTarget = "node_output"
	PatchTargetNodeCheckpoint RuntimePatchTarget = "node_checkpoint"
)

type RuntimePatch struct {
	Target RuntimePatchTarget `json:"target"`
	Node   string             `json:"node"`
	Path   string             `json:"path"`
	Value  any                `json:"value"`
}

type ResumeSpec struct {
	ResumeFrom string         `json:"resume_from"`
	Patches    []RuntimePatch `json:"patches"`
}

type Task struct {
	...
	ResumeFrom *string `json:"resume_from"`
	PatchJSON  []byte  `json:"patch_json"`
}
```

语义：
•	PatchJSON 记录本次 fork 做了哪些 patch
•	ResumeFrom 记录从哪个节点重新开始
•	这两个字段属于任务 lineage 的一部分，不写进 node runtime

3.2 NodeRuntime 侧新增能力

必须新增：ResolvedInputSnapshot

你已经有：
•	InputHash
•	Output
•	Checkpoint

但还缺一个关键字段：

```go
ResolvedInput map[string]any `json:"resolved_input"`
```
这是节点本次真正执行时构造出来并通过校验的输入快照。

作用有三个：

第一，给 UI 展示。
前端点开节点时，可以真正看到：
•	这个节点当时拿到的 prompt 是什么
•	parsed_intent 是什么
•	style / model / duration 是什么

第二，给 patch 定位。
用户说“我要改 enhance_prompt 的 parsed_intent”，系统才能知道：
•	这个字段来自哪里
•	当前值是多少

第三，给调试与审计。
否则只有 InputHash，你知道变了，但不知道“变成什么”。

⸻

4. patch precedence 规则

引擎里状态来源优先级必须明确。

推荐规则：

4.1 planning / execution 时的状态优先级

对于某个节点 N，构造表达式环境时：
1.	当前 run 内已 patch 的 node output / checkpoint
2.	当前 run 内已执行完成节点的 output
3.	当前 run 内 injected reused output
4.	父快照 output
5.	task input

也就是：

patch > current run output > injected snapshot > parent snapshot > task input fallback

这样可以保证 patched state 永远优先于父任务快照。

5. planning 阶段规范

5.1 patched planning context

BuildDirtyPlan 不能只基于：
•	新 task input
•	父快照 output

还必须基于：
•	patch 后的虚拟状态

所以 planning context 需要分三层：
•	parent snapshot seed
•	patch application
•	downstream input rebuild

流程：
1.	建立 planCtx
2.	注入父任务可复用节点输出
3.	应用 patch 到 planCtx
4.	从拓扑顺序重新 build 各节点 input
5.	比较新 input hash 与父快照 input hash
6.	计算 dirty / reuse / resume boundary

⸻

5.2 patch 对 dirty plan 的影响

规则如下。

patch 命中的节点

被 patch 的节点本身不一定重跑，取决于 patch 的对象。

情况 A：patch 上游 output，resume_from 是下游节点
例如：
•	patch reconstruct_intent.output.intent
•	resume_from = enhance_prompt

那么：
•	reconstruct_intent 视为 patched success node
•	不重跑
•	enhance_prompt 标记 dirty
•	enhance_prompt 以下继续传播 dirty

这是你们当前最推荐的模式。

情况 B：patch 当前节点 output，resume_from 也是当前节点下游
例如：
•	patch enhance_prompt.output.optimized_prompt
•	resume_from = video_generate_submit

那么：
•	enhance_prompt 不重跑
•	video_generate_submit 及下游重跑

情况 C：patch checkpoint，且 checkpoint 影响该节点 output 聚合逻辑
例如 map fan-in 的 durable state 被 patch
那通常需要：
•	重新计算该 map 节点 output
•	再从其下游 resume

⸻

6. resume_from 语义

resume_from 必须是一个明确的执行边界。

6.1 定义

resume_from 表示：

从这个节点开始重新 build input 并执行；它之前的节点原则上尽量复用，除非它们也因为 patch 或 dirty 规则被打脏。

⸻

6.2 规则

resume_from 之前
•	默认可复用
•	但如果这些节点本身被 patch 命中，则它们的 runtime output 要改写为 patched state
•	不应被重新执行

resume_from 节点
•	必须标记 dirty
•	必须重新 build input
•	必须重新执行
•	即使 InputHash 碰巧没变，也建议在 patch run 中强制执行一次，避免 patch 意图被“误复用”

resume_from 下游
•	走 upstream dirty 传播
•	能复用则复用，不能复用则重跑
•	但通常 resume_from 后续链路都应该重算，除非你们以后要做更细粒度 cut-through reuse

⸻

7. dirty 传播规则

7.1 dirty 来源

当前可以有这几类：
•	input_changed
•	upstream_dirty
•	missing_parent_snapshot
•	input_resolve_failed
•	parent_not_success

现在建议新增：
•	patched_state
•	resume_boundary

含义

patched_state
节点自身状态被 patch 改写，但节点本身未必重跑

resume_boundary
这是用户指定从这里开始重跑的节点

⸻

7.2 传播规则

规则 1：被 patch 的节点不一定 dirty

如果 patch 的是 reconstruct_intent.output，但 resume_from 是 enhance_prompt，
则：
•	reconstruct_intent 是 patched success，不是 dirty execute node
•	enhance_prompt 是 dirty boundary

规则 2：resume_from 节点一定 dirty

无论 hash 比较是否变化，都应打上：
```go
IsDirty = true
DirtyReason = "resume_boundary"
```

规则 3：resume_from 下游默认 upstream_dirty

例如：
•	enhance_prompt = resume boundary
•	video_generate_submit = upstream_dirty
•	video_generate_wait = upstream_dirty
•	video_download = upstream_dirty
•	…

规则 4：resume_from 上游不应因下游重跑而变 dirty

除非它自己被 patch 或缺失快照

8. runtime 状态流转规范

8.1 patched 节点的状态

对于被 patch 且不需要重跑的节点：
•	State = success
•	IsInjected = false
•	ReuseKind = ReuseNone 或单独新增 patched
•	Output = patched output
•	Checkpoint = patched checkpoint
•	InputHash 保持原值或不变
•	OutputHash 重新计算
•	DirtyReason = ""

为什么不是 injected？
因为它不是“来自父快照原封不动注入”，而是“基于父快照被用户编辑后的新状态”。

所以更干净的做法是以后新增：

```go
type RuntimeOrigin string

const (
	RuntimeOriginExecuted RuntimeOrigin = "executed"
	RuntimeOriginInjected RuntimeOrigin = "injected"
	RuntimeOriginPatched  RuntimeOrigin = "patched"
)
```
但在当前阶段先不必强行做。

8.2 resume boundary 节点的状态

初始进入 fork run 时：
•	State = pending
•	IsDirty = true
•	DirtyReason = "resume_boundary"
•	ReuseKind = ReuseNone

执行后：
•	State = success
•	dirty 元数据保留
•	lineage 不清空 dirty

⸻

8.3 下游节点状态
•	执行前：pending + upstream_dirty
•	执行后：success + upstream_dirty
•	ReuseKind = ReuseNone

这样 UI 能明确看到哪些节点是这次局部重做实际重新跑出来的。

⸻

9. resolved input snapshot 规范

9.1 写入时机

在 executeNode 里，buildNodeInput + validateNodeInput 成功后，立刻写入：
```go
runtime.ResolvedInput = deepCloneMap(inputs)
```

并持久化。

9.2 用途

给前端展示

前端可以展示：
•	当前节点最终输入
•	输入字段来源
•	本次 patch 后的输入快照

给 patch UI 做回显

比如用户点开 enhance_prompt：
•	能看到 parsed_intent 当前值
•	但编辑动作实际上会被转译成对 reconstruct_intent.output.intent 的 patch

给 planning 做诊断

当 dirty plan 不符合预期时，可以直接对比：
•	parent.ResolvedInput
•	new resolved input

而不是只看 hash。

⸻

10. patch / resume 推荐执行流程

推荐新流程如下。

10.1 创建 fork run

创建新任务时带上：
•	ForkedFrom
•	PatchJSON
•	ResumeFrom

10.2 Run 开始时

在 prepareForkReuse 之前或其中：
1.	读取父任务快照
2.	注入父节点成功输出
3.	应用 patch 到当前 run 的 patched state
4.	构建 patched planning context
5.	计算 dirty plan
6.	标记 resume boundary 和 downstream dirty
7.	非 dirty 节点继续注入复用

10.3 runDAG
•	resume_from 之前节点应已 success 或 injected success
•	从 resume_from 开始真正进入 pending -> ready -> running -> success

11. 你们项目里建议的最小升级集

按投入产出比，建议先做这三件。

第一阶段：必须做

1）Task 增加 patch + resume_from
```go
ResumeFrom *string
PatchJSON  []byte
```

2）NodeRuntime 增加 ResolvedInput
```go
ResolvedInput map[string]any
```

3）prepareForkReuse 支持 patched planning context

也就是：
•	patch node output/checkpoint
•	resume_from boundary
•	dirty propagation

这三件做完，你们就已经从“task input 级局部重做”升级成真正的“node state patch + downstream resume”。

第二阶段：推荐做

4）增加 patch helper

例如：
•	ApplyRuntimePatch(runtime, patch)
•	ApplyPatchesToPlanningContext(planCtx, patches)
•	ApplyPatchesToRunContext(runCtx, patches)

5）增加新的 dirty reason
•	patched_state
•	resume_boundary

6）增加 RuntimeOrigin

区分：
•	executed
•	injected
•	patched

⸻

第三阶段：增强做

7）支持 path patch

例如：
•	intent.scene
•	intent.camera.motion
•	results.0.caption

8）支持 checkpoint patch

主要用于：
•	map 聚合结果修正
•	长流程中间 durable state 修正

9）支持 patch validation

比如 patch 前校验：
•	node 是否存在
•	path 是否存在
•	value 类型是否合理
•	resume_from 是否在 patch node 下游

⸻

12. 与主流工作流系统的关系

LangGraph

更接近你们现在想走的路线。
它的核心思想就是：
•	stateful graph
•	checkpoint
•	edit state
•	resume from node

所以你说的“改上游输出，再从下游恢复”，非常接近 LangGraph 思路。

Dify

Dify 更偏产品化编排，节点重跑和状态编辑能力相对更偏应用层，不像 LangGraph 那么原生强调“checkpoint state editing”。

所以从“引擎专业度”来说：
•	你们走 patch node output + resume_from downstream
•	确实更接近高级 runtime / stateful graph 引擎
•	这条路线是对的

⸻

13. 最终设计结论

你们引擎后续要坚持下面这套标准：

设计准则
•	不直接 patch node input
•	patch 上游 node output / checkpoint
•	从目标消费节点开始 resume
•	所有下游重新 build input
•	所有下游消费一致的新状态
•	保持 lineage / dirty / reuse 可追踪

运行时准则
•	patch 是任务级编辑指令，不是临时内存 hack
•	planning 时必须考虑 patched state
•	resume_from 是明确的重跑边界
•	ResolvedInput 必须落库，便于展示、调试、审计

产品准则
•	前端可以说“修改某节点输入”
•	引擎内部必须翻译成“patch 上游输出 + downstream resume”

⸻

14. 一句话版本

你们的引擎应该升级成：

一个支持 checkpoint patch、resume_from、resolved input snapshot 的 stateful DAG runtime；用户编辑的不是节点输入本身，而是上游节点状态，系统再从消费边界重新执行下游链路。
