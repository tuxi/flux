package runtime

// trace.go —— 执行真相的事件日志（event-sourcing 的 log）。
//
// 设计要点（与 telemetry Emitter 严格区分）：
//   - Emitter/Event 是给人看的遥测：可丢、瞬时、高频（token 流）。
//   - TraceEvent 是给内核用的回放级真相：完整、有序、持久，足以确定性回放。
//
// 现阶段（Phase 1 / sidecar）：trace 只记录，不驱动任何决策（不替代 ExecState、
// 不替代 Store、不驱动 replay）。但 payload 此刻就要"回放完整"——把内核边界上
// 的全部非确定性（tool output、planner 决策、resolved input）一次性围栏住，
// 否则 Phase 2 切回放时要重新埋点。

// TraceClass 区分两条语义流，但二者共享同一条 Seq 全序。
type TraceClass uint8

const (
	// ClassExecution 确定性流：节点生命周期 + tool I/O。
	ClassExecution TraceClass = iota
	// ClassControl 非确定性流：planner/编译器的计划产出（plan_extend）。
	// "agent 的自由"是内核外部性，一旦记录就被固化为确定性事实。
	ClassControl
)

type TraceType string

const (
	TraceNodeStart  TraceType = "node_start"
	TraceInput      TraceType = "input"  // resolved input（回放时的确定性入参）
	TraceOutput     TraceType = "output" // tool 产出（回放时要注入的外部不确定性）
	TraceAwait      TraceType = "await"
	TraceResume     TraceType = "resume"
	TraceFail       TraceType = "fail"
	TracePlanExtend TraceType = "plan_extend" // control 流：planner 产出了哪些节点
)

// TraceEvent 一条不可变的执行真相记录。
type TraceEvent struct {
	RunID string
	// Seq 是全序真相：execution 与 control 共用同一单调序列，
	// 跨流因果（"先有 Y.output，planner 才产出 X"）才可回放。回放靠 Seq，不靠 wall-clock。
	Seq     int64
	Class   TraceClass
	Node    string // plan_extend 时为空
	Type    TraceType
	Payload map[string]any
}

// TraceSink 双写协议。两个方法只是语义分类；Seq 由唯一写入者（Scheduler）统一盖章，
// 保证两流共享单序列。nil sink ⇒ 完全不记录（sidecar 默认关闭，决策无副作用）。
type TraceSink interface {
	EmitExecution(e TraceEvent)
	EmitControl(e TraceEvent)
}

// tracer 是 Scheduler 内部的盖章器：持有 RunID + 单调 Seq，保证单序列。
// 方法支持 nil receiver —— 未启用 trace 时所有埋点都是空操作。
type tracer struct {
	sink  TraceSink
	runID string
	seq   int64
}

func (t *tracer) exec(typ TraceType, node string, payload map[string]any) {
	if t == nil || t.sink == nil {
		return
	}
	t.seq++
	t.sink.EmitExecution(TraceEvent{
		RunID: t.runID, Seq: t.seq, Class: ClassExecution,
		Node: node, Type: typ, Payload: payload,
	})
}

func (t *tracer) control(typ TraceType, payload map[string]any) {
	if t == nil || t.sink == nil {
		return
	}
	t.seq++
	t.sink.EmitControl(TraceEvent{
		RunID: t.runID, Seq: t.seq, Class: ClassControl,
		Type: typ, Payload: payload,
	})
}
