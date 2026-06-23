package runtime

import (
	"context"
	"errors"
	"time"
)

var ErrDeadlock = errors.New("runtime: no runnable node and not done (dependency deadlock)")

type RunStatus uint8

const (
	StatusCompleted RunStatus = iota
	StatusSuspended           // 有节点在 NodeAwaiting，等外部唤醒后再 Resume
	StatusFailed
)

type Result struct {
	Status RunStatus
	Err    error
}

// Scheduler 是底座的执行器：消费 PlanSource，做依赖求解，跑就绪节点，
// 处理 sync 内联 / async 挂起。它取代 engine.runDAG 的拓扑循环，但不认识 definition。
type Scheduler struct {
	plan    *Plan // 累积的计划（动态前沿会往里追加）
	invoker Invoker
	await   AwaitController
	store   Store
	emit    Emitter
	tracer  *tracer // nil ⇒ 不记录 trace（sidecar 默认关闭，决策无副作用）
}

func NewScheduler(inv Invoker, aw AwaitController, st Store, em Emitter) *Scheduler {
	return &Scheduler{
		plan:    &Plan{Nodes: map[string]*PlanNode{}},
		invoker: inv,
		await:   aw,
		store:   st,
		emit:    em,
	}
}

// WithTrace 启用 sidecar trace（Phase 1）。additive：不改 NewScheduler/Run 签名。
// trace 只记录、不驱动任何决策——移除 sink 不改变执行结果。
func (s *Scheduler) WithTrace(sink TraceSink, runID string) *Scheduler {
	s.tracer = &tracer{sink: sink, runID: runID}
	return s
}

// Run 驱动一次（可恢复的）执行。返回 Suspended 时，外部事件到达后调用 Resume 再次进入。
func (s *Scheduler) Run(ctx context.Context, src PlanSource, state ExecState) (Result, error) {
	for {
		// 1) 向计划源拉新增节点（静态=整图一次；planner=下一步）
		added, done, err := src.Next(ctx, state)
		if err != nil {
			return Result{Status: StatusFailed, Err: err}, err
		}
		var newNames []string
		for _, n := range added {
			if _, exists := s.plan.Nodes[n.Name]; exists {
				continue // 已知节点，跳过（动态前沿幂等）
			}
			s.plan.Nodes[n.Name] = n
			state.Transition(n.Name, NodePending)
			newNames = append(newNames, n.Name)
		}
		if len(newNames) > 0 {
			// control 流：记录"谁产出了这批节点"（静态编译器=整图一次；planner=逐步）。
			// agent 回放靠它把 LLM 决策固化为确定性事实。
			s.tracer.control(TracePlanExtend, map[string]any{"nodes": newNames})
		}

		// 2) 求就绪集：自身 pending 且依赖按 Join 语义满足
		runnable := s.pickRunnable(state)

		// 3) 没有可跑的 —— 判断挂起 / 完成 / 等 planner / 死锁
		if len(runnable) == 0 {
			switch {
			case s.hasAwaiting(state):
				// 对应现有 WorkflowSuspendedError{SuspendAsyncNode}：交出控制权等唤醒
				return Result{Status: StatusSuspended}, nil
			case done && s.allTerminal(state):
				return Result{Status: StatusCompleted}, nil
			case !done:
				// planner 还没说结束，但当前没活可干 —— 让它再产下一批。
				// 真实现里这里应阻塞等待 planner 信号，避免忙等；草案先简单退避。
				time.Sleep(10 * time.Millisecond)
				continue
			default:
				return Result{Status: StatusFailed, Err: ErrDeadlock}, ErrDeadlock
			}
		}

		// 4) 执行就绪节点（可并行；草案串行示意）
		for _, n := range runnable {
			if err := s.execNode(ctx, state, n); err != nil {
				return Result{Status: StatusFailed, Err: err}, err
			}
		}
	}
}

// Resume 由外部完成事件触发（webhook/poll → CompleteAwaitNode 的 runtime 版）。
// 把 async 节点置为 success 并写回产出，然后重入 Run 继续推进 DAG。
func (s *Scheduler) Resume(ctx context.Context, src PlanSource, state ExecState, node string, out map[string]any) (Result, error) {
	s.tracer.exec(TraceResume, node, map[string]any{"output": out})
	state.SetOutput(node, out)
	state.Transition(node, NodeSuccess)
	_ = s.store.PersistNode(ctx, node, NodeSuccess, out)
	return s.Run(ctx, src, state)
}

func (s *Scheduler) execNode(ctx context.Context, state ExecState, n *PlanNode) error {
	s.tracer.exec(TraceNodeStart, n.Name, nil)

	input, err := n.Resolve(ctx, state) // InputResolver：expr（workflow）或具体值（planner）
	if err != nil {
		s.tracer.exec(TraceFail, n.Name, map[string]any{"stage": "resolve", "error": err.Error()})
		state.Transition(n.Name, NodeFailed)
		return err
	}
	// 回放完整：记录 resolved input（确定性入参）。
	s.tracer.exec(TraceInput, n.Name, map[string]any{"input": input})
	state.Transition(n.Name, NodeRunning)

	// ── async 分叉：对应 engine/executor.go:133 ──
	if n.Async {
		if _, err := s.await.Begin(ctx, n, input); err != nil {
			s.tracer.exec(TraceFail, n.Name, map[string]any{"stage": "await", "error": err.Error()})
			state.Transition(n.Name, NodeFailed)
			return err
		}
		s.tracer.exec(TraceAwait, n.Name, map[string]any{"input": input})
		state.Transition(n.Name, NodeAwaiting) // 挂起，等 Resume
		return s.store.PersistNode(ctx, n.Name, NodeAwaiting, nil)
	}

	// ── sync 内联执行（含简单重试，对应 ToolStepAdapter.RetryPolicy）──
	var out map[string]any
	attempts := n.Retry.MaxRetries + 1
	for i := 0; i < attempts; i++ {
		out, err = s.invoker.Invoke(ctx, n.ToolName, input, s.emit)
		if err == nil {
			break
		}
		if i < attempts-1 && n.Retry.Interval > 0 {
			time.Sleep(n.Retry.Interval)
		}
	}
	if err != nil {
		s.tracer.exec(TraceFail, n.Name, map[string]any{"stage": "invoke", "error": err.Error()})
		state.Transition(n.Name, NodeFailed)
		return err
	}
	// 回放完整：记录 tool output（回放时要注入的外部不确定性）。
	s.tracer.exec(TraceOutput, n.Name, map[string]any{"output": out})
	state.SetOutput(n.Name, out)
	state.Transition(n.Name, NodeSuccess)
	return s.store.PersistNode(ctx, n.Name, NodeSuccess, out)
}

// ── 依赖求解辅助 ──

func (s *Scheduler) pickRunnable(state ExecState) []*PlanNode {
	var out []*PlanNode
	for name, n := range s.plan.Nodes {
		if state.State(name) != NodePending {
			continue
		}
		if s.depsSatisfied(state, n) {
			out = append(out, n)
		}
	}
	return out
}

func (s *Scheduler) depsSatisfied(state ExecState, n *PlanNode) bool {
	if len(n.DependsOn) == 0 {
		return true
	}
	switch n.Join {
	case JoinAny:
		for _, d := range n.DependsOn {
			if state.State(d) == NodeSuccess {
				return true
			}
		}
		return false
	default: // JoinAll
		for _, d := range n.DependsOn {
			if st := state.State(d); st != NodeSuccess && st != NodeSkipped {
				return false
			}
		}
		return true
	}
}

func (s *Scheduler) hasAwaiting(state ExecState) bool {
	for name := range s.plan.Nodes {
		if state.State(name) == NodeAwaiting {
			return true
		}
	}
	return false
}

func (s *Scheduler) allTerminal(state ExecState) bool {
	for name := range s.plan.Nodes {
		if !state.State(name).Terminal() {
			return false
		}
	}
	return true
}
