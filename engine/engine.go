package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"flux/cost"
	"flux/definition"
	"flux/domain"
	"flux/engine/graph"
	"flux/eventbus"
	"flux/pkg/lock"
	"flux/pkg/uuid"
	repository2 "flux/repository"
	"flux/workflow"
	"flux/workflow/nodes"
)

// Engine 工作流执行器
type Engine struct {
	mu                  sync.RWMutex
	taskRepo            repository2.TaskRepository
	nodeRepo            repository2.NodeRuntimeRepository
	awaitBindingRepo    repository2.AwaitBindingRepository
	WorkflowVersionRepo repository2.WorkflowVersionRepository
	WorkflowRepo        repository2.WorkflowRepository

	builder *workflow.Builder

	eventBus  *eventbus.EventBus
	eventRepo repository2.EventRepository

	jobQueue AsyncJobQueue
	iSrv     uuid.SnowNode
	dLocker  lock.DistributedLock // 分布式锁

	checkpointRebuilders *checkpointRebuildRegistry
	costRecorder         cost.Recorder

	// enableSubWorkflowBinding 控制 P1 的 subworkflow-as-await-binding 写入路径。
	// 关闭时（默认）行为与改造前完全一致：父节点停在 NodeRunning，由 recovery_scanner 兜底。
	// 开启时：父节点挂起落 NodeAwaiting + 建 await binding，完成走 CompleteAwaitNode。
	// ⚠️ 在 P2 的 poll 对账兜底上线前，开启意味着丢事件/崩溃场景缺少兜底，仅供灰度验证。
	enableSubWorkflowBinding bool
}

func NewEngine(
	taskRepo repository2.TaskRepository,
	nodeRepo repository2.NodeRuntimeRepository,
	awaitBindingRepo repository2.AwaitBindingRepository,
	workflowVersionRepo repository2.WorkflowVersionRepository,
	workflowRepo repository2.WorkflowRepository,
	builder *workflow.Builder,
	eventBus *eventbus.EventBus,
	jobQueue AsyncJobQueue,
	dLocker lock.DistributedLock,
	eventRepo repository2.EventRepository,
) *Engine {
	rebuilders := newCheckpointRebuildRegistry()
	rebuilders.Register(definition.NodeMap, func(nodeDef nodes.Node, runtime *domain.NodeRuntime) (map[string]any, error) {
		return rebuildMapNodeOutput(runtime)
	})
	rebuilders.Register(definition.NodeLoop, func(nodeDef nodes.Node, runtime *domain.NodeRuntime) (map[string]any, error) {
		return rebuildLoopNodeOutput(runtime)
	})
	e := &Engine{
		taskRepo:             taskRepo,
		nodeRepo:             nodeRepo,
		awaitBindingRepo:     awaitBindingRepo,
		WorkflowVersionRepo:  workflowVersionRepo,
		WorkflowRepo:         workflowRepo,
		builder:              builder,
		eventBus:             eventBus,
		eventRepo:            eventRepo,
		jobQueue:             jobQueue,
		iSrv:                 *uuid.NewNode(3),
		dLocker:              dLocker,
		checkpointRebuilders: rebuilders,
		costRecorder:         cost.NewDefaultRecorder(),
	}

	e.startAsyncNodeEventListener()
	e.startSubWorkflowSuccessListener()
	e.startSubWorkflowFailedListener()
	return e
}

func (e *Engine) SetCostRecorder(recorder cost.Recorder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.costRecorder = recorder
}

// SetSubWorkflowBinding 开启/关闭 subworkflow-as-await-binding 写入路径（P1，默认关闭）。
func (e *Engine) SetSubWorkflowBinding(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enableSubWorkflowBinding = enabled
}

// RunWithResult 执行 DAG，并返回明确的运行结果状态。
func (e *Engine) RunWithResult(
	ctx context.Context,
	task *domain.Task,
	def *definition.WorkflowDefinition,
) RunResult {
	var (
		wf  workflow.Workflow
		err error
	)

	// 优先使用传入 def，兼容现有 worker 调用
	if def != nil {
		wf, err = e.builder.Build(def)
		if err != nil {
			return RunResult{Status: RunFailed, Err: err}
		}
	} else {
		var loadedDef *definition.WorkflowDefinition
		wf, loadedDef, err = e.loadWorkflowForTask(ctx, task)
		if err != nil {
			return RunResult{Status: RunFailed, Err: err}
		}
		_ = loadedDef
	}

	runCtx := e.newRunContext(ctx, task, wf)

	// 1. 加载/初始化 runtime
	if err := e.loadOrInitRuntime(runCtx, wf); err != nil {
		return RunResult{Status: RunFailed, Err: err}
	}

	// 2. 只有 fork 初始执行才做 planning/materialization
	if task.ForkedFrom != nil {
		if err := e.LoadForkParentSnapshot(runCtx); err != nil {
			return RunResult{Status: RunFailed, Err: err}
		}

		plan, err := e.BuildRunPlan(runCtx, wf, runCtx.Input)
		if err != nil {
			return RunResult{Status: RunFailed, Err: err}
		}

		if err := e.MaterializeRunPlan(runCtx, wf, plan); err != nil {
			return RunResult{Status: RunFailed, Err: err}
		}
	}

	// 3. 恢复 activated edges
	e.rebuildActivatedEdges(runCtx)

	// 4. 执行
	return e.executeTask(runCtx, wf, true)
}

// Run 执行 DAG，保留旧接口语义：
// 只有失败时返回 error，success/suspended/noop 都返回 nil。
func (e *Engine) Run(
	ctx context.Context,
	task *domain.Task,
	def *definition.WorkflowDefinition,
) error {
	result := e.RunWithResult(ctx, task, def)
	if result.Status == RunFailed {
		return result.Err
	}
	return nil
}

// loadOrInitRuntime 加载并初始化节点、恢复节点状态
func (e *Engine) loadOrInitRuntime(ctx *nodes.Context, wf workflow.Workflow) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	nodeMap := wf.Nodes()
	order := wf.Order()

	// 1️⃣ 加载数据库节点状态
	savedNodes, err := e.nodeRepo.FindByTaskID(ctx.Ctx, ctx.Task.ID)
	if err != nil {
		return err
	}

	// 初始化全局 Output 结构
	if ctx.Output == nil {
		ctx.Output = make(map[string]any)
	}
	ctx.EnsureOutputInitialized()

	if len(savedNodes) > 0 {
		// 恢复模式
		for _, n := range savedNodes {
			if n == nil {
				continue
			}

			ctx.Runtime[n.Name] = n

			// 恢复成功类输出，供表达式/下游使用
			if (n.State == domain.NodeSuccess || n.State == domain.NodeSuccessPendingEdges) && len(n.Output) > 0 {
				def, ok := nodeMap[n.Name]
				if !ok {
					return fmt.Errorf("workflow definition missing node %s", n.Name)
				}
				if err := ctx.SetNodeOutput(n.Name, deepCloneMap(n.Output), def.Step.OutputSchema()); err != nil {
					return fmt.Errorf("restore node %s output failed: %w", n.Name, err)
				}
				ctx.UpdateNodeStatus(n.Name, string(n.State))
			}

			// persist_output=false 的终端聚合节点，output 未落盘，恢复时需重跑
			if n.State == domain.NodeSuccess && len(n.Output) == 0 {
				if def, ok := nodeMap[n.Name]; ok && !shouldPersistOutput(def) {
					n.State = domain.NodePending
					n.StartedAt = nil
					n.FinishedAt = nil
					n.Error = ""
					if err := e.nodeRepo.Update(ctx.Ctx, n); err != nil {
						return err
					}
					continue
				}
			}

			switch n.State {
			case domain.NodeRunning:
				// NodeRunning 且已有 output 落盘：说明 executeNode 已完成（tool 跑完并写了 output），
				// 但 finalizeNode 在写 NodeSuccess 之前进程崩溃了。
				// 直接恢复为 NodeSuccess，避免在 ResumeTask 时重新执行已完成的前置节点。
				if len(n.Output) > 0 {
					n.State = domain.NodeSuccess
					if n.FinishedAt == nil {
						now := time.Now()
						n.FinishedAt = &now
					}
					if err := e.nodeRepo.Update(ctx.Ctx, n); err != nil {
						return err
					}
					if def, ok := nodeMap[n.Name]; ok {
						if restoreErr := ctx.SetNodeOutput(n.Name, deepCloneMap(n.Output), def.Step.OutputSchema()); restoreErr == nil {
							ctx.UpdateNodeStatus(n.Name, string(domain.NodeSuccess))
						}
					}
					continue
				}
				// output 未落盘，说明执行真的被中断，fallthrough 到通用的 Pending 重置
				fallthrough

			case domain.NodeRetrying, domain.NodeReady:
				// 这些都说明上次执行中断了，恢复成 Pending
				n.State = domain.NodePending
				n.StartedAt = nil
				n.FinishedAt = nil
				n.Error = ""

				if err := e.nodeRepo.Update(ctx.Ctx, n); err != nil {
					return err
				}

			case domain.NodeSuccessPendingEdges, domain.NodeFailedPendingEdges:
				// 保持原样，runDAG 进入后会 finalize
				continue

			case domain.NodeSuccess, domain.NodeFailed, domain.NodeSkipped, domain.NodeCanceled, domain.NodePending:
				// 保持原样
				continue
			}
		}
	} else {
		// 首次执行，初始化 runtime
		weight := 1.0 / float64(len(nodeMap))
		index := 0

		for i, name := range order {
			node := nodeMap[name]
			if node.Weight > 0 {
				weight = node.Weight
			}
			if !nodes.IsSystemNode(node.Name) {
				index++
			}

			nr := &domain.NodeRuntime{
				TaskID:    ctx.Task.ID,
				Name:      name,
				State:     domain.NodePending,
				Index:     i,     // 拓扑顺序
				BizIndex:  index, // UI 顺序
				Weight:    weight,
				ReuseKind: domain.ReuseNone,
			}

			ctx.Runtime[name] = nr

			if err := e.nodeRepo.Create(ctx.Ctx, nr); err != nil {
				return err
			}
		}
	}

	// 兜底：确保定义里的每个节点都有 runtime。
	// 恢复模式只加载了已落库的节点；若任务在工作流结构变更后被恢复（定义新增了节点，
	// 而这些节点在该任务首次执行时还不存在、未落库），runDAG 遍历 order 时
	// ctx.Runtime[node.Name] 会取到 nil 指针并在 runtime.State 处崩溃。
	// 这里为缺失的定义节点补建 Pending runtime 并落库，让 runDAG 正常调度，
	// 避免空指针 panic。首次执行分支已创建全部节点，这里是 no-op。
	bizIndex := 0
	for i, name := range order {
		node := nodeMap[name]
		if !nodes.IsSystemNode(name) {
			bizIndex++
		}
		if _, ok := ctx.Runtime[name]; ok {
			continue
		}
		w := 1.0 / float64(len(nodeMap))
		if node.Weight > 0 {
			w = node.Weight
		}
		nr := &domain.NodeRuntime{
			TaskID:    ctx.Task.ID,
			Name:      name,
			State:     domain.NodePending,
			Index:     i,
			BizIndex:  bizIndex,
			Weight:    w,
			ReuseKind: domain.ReuseNone,
		}
		ctx.Runtime[name] = nr
		if err := e.nodeRepo.Create(ctx.Ctx, nr); err != nil {
			return err
		}
	}

	return nil
}

// rebuildActivatedEdges 任务恢复阶段把每个 NodeRuntime 持久化的 ActivatedEdges 灌回 ctx。
//
// 兜底规则：丢弃过期的 false 边。
//
// 背景：finalizeNode 失败分支、finalizeSkippedNode 会把所属节点的所有 outgoing edge
// 标为 false 并落盘。如果该节点后续被 fork / dirty 流程 reset 回 pending，
// 它在 ctx 内的 ActivatedEdges 会被 clearOutgoingActivatedEdges 清掉；但若 reset 早于
// 持久化落库，或重启后再次 rebuild，DB 里的过期 false 边仍可能被加载，
// 让下游 depsMet 错误跳过这条入边。
//
// 规则：如果记录值是 false 且 from 节点当前不是 terminal 状态，认为这是过期数据，
// 跳过加载，让下游 join 老老实实等待节点这次执行重新决定 edge。
func (e *Engine) rebuildActivatedEdges(ctx *nodes.Context) {

	for nodeName, runtime := range ctx.Runtime {

		if runtime == nil || len(runtime.ActivatedEdges) == 0 {
			continue
		}

		fromTerminal := isTerminal(runtime.State)

		for k, v := range runtime.ActivatedEdges {
			// 仅对 false 边做"幽灵记录"过滤。true 边表示节点曾真实成功通过这条边，
			// 即使节点后来又被 reset，我们也不在 rebuild 阶段擅自删除（让 executeNode
			// 的 clearOutgoingActivatedEdges 在重执行时统一清理）。
			if !v && !fromTerminal {
				_ = nodeName // 留作后续 trace
				continue
			}
			ctx.ActivatedEdges[k] = v
		}
	}
}

// runNodeWithHeartbeat 运行节点和心跳
// 每 5 秒更新一次心跳
// 即使 Worker 崩溃或进程卡死，DB RecoveryScanner 能及时发现节点失联
func (e *Engine) runNodeWithHeartbeat(execCtx *nodes.NodeExecContext, node nodes.Node) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	done := make(chan struct{})
	defer close(done)
	var err error

	go func(node nodes.Node) {
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				e.mu.Lock()
				r := execCtx.TaskContext.Runtime[node.Name]
				r.LastHeartbeat = &now
				_ = e.nodeRepo.Update(execCtx.TaskContext.Ctx, r)
				e.mu.Unlock()
			case <-done:
				return
			}
		}
	}(node)

	// 执行节点
	err = e.runNodeWithRetry(execCtx, node)
	return err
}

func (e *Engine) runNodeWithRetry(execCtx *nodes.NodeExecContext, node nodes.Node) error {

	policy := node.Step.RetryPolicy()
	var lastErr error
	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		if attempt == 1 { // 仅第一次重试修改状态，因为状态机不允许出现retrying → retrying
			e.mu.Lock()
			e.transitionLocked(execCtx.TaskContext, execCtx.TaskContext.Runtime[node.Name], domain.NodeRetrying, lastErr, nil)
			e.mu.Unlock()
			execCtx.EmitNodeEvent(nodes.NodeEvent{
				Type:    "retrying",
				Message: fmt.Sprintf("开始第 %d 次重试", attempt),
			})
		}

		err := node.Step.Run(execCtx)
		if err == nil {
			return nil
		}

		var suspendErr *domain.WorkflowSuspendedError
		if errors.As(err, &suspendErr) {
			return err // ❗挂起时直接向上传递，不进入 retry
		}

		lastErr = err
		execCtx.EmitNodeEvent(nodes.NodeEvent{
			Type:    "failed",
			Message: fmt.Sprintf("第 %d 次执行失败: %v", attempt+1, err),
		})

		// 如果还有重试机会，sleep 然后继续
		if attempt < policy.MaxRetries {
			time.Sleep(policy.Interval)
		}
	}

	return lastErr
}

// transition 实现状态机，状态修改的唯一入口
func (e *Engine) transitionLocked(
	ctx *nodes.Context,
	node *domain.NodeRuntime,
	newState domain.NodeState,
	err error,
	output map[string]any,
) {

	if !isAllowed(node.State, newState) {
		panic(fmt.Sprintf("非法状态迁移: %s → %s",
			node.State, newState))
	}

	//old := node.State
	node.State = newState

	now := time.Now()

	if newState == domain.NodeSuccess {
		node.Progress = 1
	}

	if newState == domain.NodeRunning {
		node.StartedAt = &now
	}

	if isTerminal(newState) {
		node.FinishedAt = &now
	}
	if output != nil {
		node.Output = output
	}
	if err != nil {
		node.Error = err.Error()
	}

	// 持久化节点
	err = e.nodeRepo.Update(ctx.Ctx, node)
	if err != nil {
		node.Error = err.Error()
	}
	// 状态同步写入Output
	ctx.UpdateNodeStatus(node.Name, string(newState))
	// 日志在锁内只修改状态
	//ctx.AddEvent(node.Name,
	//	fmt.Sprintf("状态变更: %s → %s", old, newState), fmt.Sprintf("node_%s", newState))
	//ctx.EmitNodeEvent(node.Name, nodes.NodeEvent{
	//	Type:    "debug",
	//	Message: fmt.Sprintf("状态变更: %s → %s", old, newState),
	//})

	progress := ctx.CalculateTaskProgress()
	ctx.Task.Progress = progress
	_ = e.taskRepo.Update(ctx.Ctx, ctx.Task)

	if ctx.Task.ID == ctx.Task.RootID {

		// 只有主工作流才发进度
		ctx.EventBus.Publish(ctx.Task.RootID, &domain.TaskEvent{
			TaskID:     ctx.Task.ID,
			RootTaskID: ctx.Task.RootID,
			Step:       "task",
			Type:       "task_progress",
			Message:    "任务进度更新",
			Meta: map[string]any{
				"progress": progress,
			},
			CreatedAt: time.Now(),
		})
	}
}

// depsMet 检查依赖，严格 fan-in join 语义（P0 修复：三态推导，不再把 blocked 当 inactive）。
//
// 每条入边通过 resolveEdgeState 从 edge.Type + parent.State + ActivatedEdges[key] 推导三态：
//   - unknown：边状态未定（父未 terminal 或 edge 未计算）→ 继续等待
//   - blocked：父节点 terminal 但非 Success（failed/skipped/canceled）→ join 阻塞，不可执行
//   - inactive：仅限 EdgeCondition/CaseKey 且父 Success 但条件不满足 → 从 join 剔除
//   - active：边已激活且父 Success → 参与 join
//
// EdgeNormal 强制 AND-join：所有 EdgeNormal 父必须 Success，任何一条 blocked 即阻断。
// EdgeCondition 可 OR-剔除：inactive 边只表示分支未走，不影响其他父继续满足 join。
//
// 历史缺陷：旧实现把 blocked（父 failed 写入 false）和 inactive（条件未命中写入 false）
// 混为一谈，对两者都 continue，导致 fan-in join 中只要有一个成功父就通过——错误放行了
// 「必选父失败 + 旁路父成功」的下游节点。
func (e *Engine) depsMet(ctx *nodes.Context, node string, dag *graph.Graph) bool {
	parents := dag.Parents[node]
	if len(parents) == 0 {
		return true
	}

	hasActiveParent := false
	for _, p := range parents {
		es := e.resolveEdgeState(ctx, p, node, dag)
		switch es {
		case EdgeStateUnknown:
			return false
		case EdgeStateBlocked:
			// 必选父或条件父死 → join 阻塞，下游不可执行。
			return false
		case EdgeStateInactive:
			// 仅条件边未命中 → 从 join 剔除，不影响其他父。
			continue
		case EdgeStateActive:
			// 再保一次：resolveEdgeState 已保证父 Success，这里防御。
			if ctx.Runtime[p].State != domain.NodeSuccess {
				return false
			}
			hasActiveParent = true
		}
	}
	return hasActiveParent
}

// clearOutgoingActivatedEdges 把某个节点所有 outgoing edge 从全局 ctx.ActivatedEdges 中移除。
//
// 调用时机：节点被重置回 pending / 重新执行前。
//
// 为什么需要：ActivatedEdges 是 join 判定的唯一信号源。一旦某条边曾被写为 false（来自 finalizeNode 失败、
// finalizeSkippedNode、failClosure、skipSubtree），即使源节点后来被 reset 回 pending，
// 那条 false 也会一直留在 ctx 里，导致下游 depsMet 把它当成"已决定的非激活边"跳过，
// 错误地让 fan-in join 提前通过。重置节点状态时必须同步把它的 outgoing 标志清掉，让 join
// 退回到"等上游真正决定"的状态。
func (e *Engine) clearOutgoingActivatedEdges(ctx *nodes.Context, nodeName string) {
	if ctx == nil || ctx.Workflow == nil {
		return
	}
	prefix := nodeName + "->"
	for _, edge := range ctx.Workflow.Edges {
		if edge.From != nodeName {
			continue
		}
		delete(ctx.ActivatedEdges, prefix+edge.To)
	}
}

// ensureEdgeClosure Closure 校验
func (e *Engine) ensureEdgeClosure(node string, dag *graph.Graph, ctx *nodes.Context) {
	for _, edge := range dag.Edges[node] {
		key := node + "->" + edge.To

		if _, ok := ctx.ActivatedEdges[key]; !ok {
			panic(fmt.Sprintf("edge not closed: %s", key))
		}
	}
}

func isTerminal(s domain.NodeState) bool {
	return s == domain.NodeSuccess ||
		s == domain.NodeFailed ||
		s == domain.NodeSkipped ||
		s == domain.NodeCanceled
}

// buildNodeInput 构造节点 Step 的 input（输入参数）
func (e *Engine) buildNodeInput(
	ctx *nodes.Context,
	node nodes.Node,
	g *graph.Graph,
) (map[string]any, error) {

	schema := node.Step.InputSchema()
	resolved := make(map[string]any)

	// 1️⃣ Config 优先
	for k, v := range node.Config {
		// 有些字段在config中是表达式，比如map 节点的 items
		if e.shouldEvalConfigField(node, k) {
			exprStr, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("config field %s must be expr string", k)
			}
			val, err := ctx.EvalAny(exprStr)
			if err != nil {
				return nil, fmt.Errorf("config expr %s eval failed: %w", k, err)
			}
			resolved[k] = val
		} else {
			resolved[k] = v
		}
	}
	nodesMap := ctx.Output["nodes"].(map[string]any)

	// 2️⃣ InputMapping 显式映射
	for targetField, source := range node.InputMapping {
		val, err := ctx.EvalAny(source)
		if err != nil {
			return nil, fmt.Errorf("inputMapping %s -> %s error: %w",
				targetField, source, err)
		}
		resolved[targetField] = val
	}
	log.Println("node:", node.Name)
	log.Println("resolved input:", resolved)

	// 3️⃣ 自动 fallback（可选）
	for field := range schema.Fields {

		if _, exists := resolved[field]; exists {
			continue
		}

		// 查找依赖关系
		parents := g.Parents[node.Name]
		for _, dep := range parents {

			depNode, ok := nodesMap[dep].(map[string]any)
			if !ok {
				continue
			}

			depOut, ok := depNode["output"].(map[string]any)
			if !ok {
				continue
			}

			if val, ok := depOut[field]; ok {
				resolved[field] = val
				break
			}
		}

		// Task input fallback
		if _, exists := resolved[field]; !exists {
			if val, ok := ctx.Input[field]; ok {
				resolved[field] = val
			}
		}
	}
	return resolved, nil
}

// validateNodeInput 验证节点输入
func (e *Engine) validateNodeInput(
	node nodes.Node,
	input map[string]any,
) error {

	schema := node.Step.InputSchema()

	for name, field := range schema.Fields {

		val, exists := input[name]

		if field.Required && !exists {
			return fmt.Errorf("缺少必填字段: %s", name)
		}

		if exists && val != nil {
			if err := nodes.ValidateFieldTypeStrict(field, val); err != nil {
				return fmt.Errorf("输入字段 %s 类型错误: %w", name, err)
			}
		}
	}

	return nil
}

func isAllowed(from, to domain.NodeState) bool {
	for _, s := range domain.AllowedTransitionsNodes[from] {
		if s == to {
			return true
		}
	}
	return false
}

// finishTask 完成任务，纯收尾数据函数，不做状态的更改
func (e *Engine) finishTask(ctx *nodes.Context) error {
	e.mu.RLock()
	var firstFailedNode *domain.NodeRuntime
	for _, r := range ctx.Runtime {
		if r.State == domain.NodeFailed {
			firstFailedNode = r
			break
		}
	}
	e.mu.RUnlock()
	if firstFailedNode != nil {
		return fmt.Errorf("%s node status is faile, %s", firstFailedNode.Name, firstFailedNode.Error)
	}

	// 输出客户端需要 output
	final, err := ctx.GetFinal()
	if err != nil {
		return err
	}
	ctx.Output["final"] = final

	// 只持久化 final，nodes 数据已在 task_nodes 表中，input 在 InputJSON 中
	outputJSON, err := json.Marshal(map[string]any{"final": final})
	if err != nil {
		return err
	}
	ctx.Task.OutputJSON = outputJSON
	return nil
}

// allTerminal 检查是否全部完成
func (e *Engine) allTerminal(ctx *nodes.Context) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, r := range ctx.Runtime {
		if !isTerminal(r.State) {
			return false
		}
	}
	return true
}

func (e *Engine) TaskRepo() repository2.TaskRepository {
	return e.taskRepo
}

func (e *Engine) NodeRepo() repository2.NodeRuntimeRepository {
	return e.nodeRepo
}

func (e *Engine) shouldEvalConfigField(node nodes.Node, key string) bool {
	regi := e.builder.GetRegister()
	if regi == nil {
		panic("register not set")
	}
	return regi.IsExprConfigField(node.Type, key)
}
