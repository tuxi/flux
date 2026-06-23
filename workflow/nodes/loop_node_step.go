package nodes

import (
	"crypto/sha1"
	"encoding/hex"
	"flux/domain"
	"flux/runtimekeys"
	"flux/tool"
	"flux/utils"
	"fmt"
	"strconv"
	"strings"
)

const (
	loopCPTotal        = "total"
	loopCPCurrentIndex = "current_index"
	loopCPDone         = "done"
	loopCPResults      = "results"
	loopCPCarryState   = "carry_state"

	// 当前正在执行的 iteration
	loopCPRunningIndex  = "running_index"
	loopCPRunningSubKey = "running_sub_key"

	// 当前 attempt token（用于构造 subKey）
	loopCPRunningAttempt = "running_attempt_token"

	// 每个 index 的 attempt 序号
	loopCPAttemptSeqByIndex = "attempt_seq_by_index"

	// 内部透传字段，参与 subKey 计算，但不作为业务字段使用
	loopHiddenAttemptToken = "__loop_attempt_token"
)

type LoopNodeStep struct {
	itemsExpr string
	iterator  string
	workflow  string

	// 语义：下一轮 inputKey <- 本轮 outputKey
	carry map[string]string

	// 支持 literal + expr 混合
	initial map[string]any
}

func NewLoopStep(
	items string,
	iterator string,
	workflow string,
	carry map[string]string,
	initial map[string]any,
) *LoopNodeStep {
	return &LoopNodeStep{
		itemsExpr: items,
		iterator:  iterator,
		workflow:  workflow,
		carry:     carry,
		initial:   initial,
	}
}

func (l *LoopNodeStep) Name() string {
	return "loop"
}

func (l *LoopNodeStep) RetryPolicy() RetryPolicy {
	return RetryPolicy{}
}

func (l *LoopNodeStep) InputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (l *LoopNodeStep) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}

func (l *LoopNodeStep) OutputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"results": {Type: "array"},
		},
	}
}

type loopIterationDecision int

const (
	loopDecisionContinue loopIterationDecision = iota
	loopDecisionSuspend
	loopDecisionFail
)

func (l *LoopNodeStep) Run(execCtx *NodeExecContext) error {
	execCtx.EmitNodeEvent(NodeEvent{
		Type:    "started",
		Message: "Loop开始执行",
	})

	// 1. resolve items
	items, err := resolveItems(l.itemsExpr, execCtx)
	if err != nil {
		return fmt.Errorf("loop resolve items failed: %w", err)
	}

	if len(items) == 0 {
		empty := []any{}
		execCtx.SetOutput("results", empty)

		runtime := execCtx.TaskContext.Runtime[execCtx.NodeDef.Name]
		if runtime != nil {
			runtime.Output = map[string]any{
				"results": empty,
			}
			runtime.OutputHash = calculateOutputHash(runtime.Output)
			if err := execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime); err != nil {
				return err
			}
		}
		return nil
	}

	// 2. load runtime
	runtime := execCtx.TaskContext.Runtime[execCtx.NodeDef.Name]
	if runtime == nil {
		return fmt.Errorf("loop runtime not found: %s", execCtx.NodeDef.Name)
	}

	// 3. init checkpoint once
	if runtime.Checkpoint == nil {
		if err := l.initCheckpoint(execCtx, runtime, len(items)); err != nil {
			return err
		}
		l.emitFanoutProgress(execCtx, runtime)
	}

	// 4. reconcile current running iteration, if any
	decision, err := l.processRunningIteration(execCtx, runtime)
	if err != nil {
		return err
	}
	if decision == loopDecisionSuspend {
		return l.suspend()
	}
	if decision == loopDecisionFail {
		return fmt.Errorf("loop process running iteration failed")
	}

	// 5. re-read current checkpoint state after reconciliation
	cp := runtime.Checkpoint
	total := utils.ToInt(cp[loopCPTotal])
	currentIndex := utils.ToInt(cp[loopCPCurrentIndex])
	runningIndex := utils.ToInt(cp[loopCPRunningIndex])

	// 6. if all iterations done, finalize output
	if currentIndex >= total {
		finalResults, err := l.buildFinalResults(cp)
		if err != nil {
			return err
		}

		execCtx.SetOutput("results", finalResults)

		runtime.Output = map[string]any{
			"results": deepCloneAny(finalResults),
		}
		runtime.OutputHash = calculateOutputHash(runtime.Output)

		clearNodeReuseMetadata(runtime)
		runtime.ReuseKind = domain.ReuseNone

		if err := execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime); err != nil {
			return err
		}
		l.emitFanoutProgress(execCtx, runtime)

		return nil
	}

	// 7. if still has active binding, keep suspended
	if runningIndex != -1 {
		l.emitFanoutProgress(execCtx, runtime)
		return l.suspend()
	}

	// 8. no active binding and not finished -> dispatch next iteration
	if currentIndex < 0 || currentIndex >= len(items) {
		return fmt.Errorf("loop current_index out of range: index=%d len=%d", currentIndex, len(items))
	}

	if err := l.dispatchNextIteration(execCtx, runtime, items[currentIndex], currentIndex); err != nil {
		return err
	}
	l.emitFanoutProgress(execCtx, runtime)

	return l.suspend()
}

func (l *LoopNodeStep) processRunningIteration(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
) (loopIterationDecision, error) {
	if runtime == nil || runtime.Checkpoint == nil {
		return loopDecisionContinue, nil
	}

	cp := runtime.Checkpoint
	runningIndex := utils.ToInt(cp[loopCPRunningIndex])
	if runningIndex == -1 {
		return loopDecisionContinue, nil
	}

	runningSubKey := strings.TrimSpace(utils.ToString(cp[loopCPRunningSubKey]))
	if runningSubKey == "" {
		return loopDecisionFail, fmt.Errorf(
			"loop checkpoint invalid: running_index=%d but running_sub_key empty",
			runningIndex,
		)
	}

	child, err := execCtx.Executor.TaskRepo().FindBySubKey(
		execCtx.TaskContext.Ctx,
		runningSubKey,
	)
	if err != nil {
		return loopDecisionFail, fmt.Errorf("loop find child by subKey failed: %w", err)
	}

	// child 短暂不可见时不要激进失败
	if child == nil {
		execCtx.EmitNodeEvent(NodeEvent{
			Type:    "debug",
			Message: fmt.Sprintf("loop iteration %d child not visible yet, keep suspended", runningIndex),
		})
		return loopDecisionSuspend, nil
	}

	switch child.Status {
	case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended:
		return loopDecisionSuspend, nil

	case domain.TaskSuccess:
		if err := l.aggregateSuccessfulIteration(execCtx, runtime, child, runningIndex); err != nil {
			return loopDecisionFail, err
		}
		return loopDecisionContinue, nil

	case domain.TaskFailed:
		return loopDecisionFail, fmt.Errorf(
			"loop iteration %d child task failed (retry_count=%d), binding preserved, parent retry required",
			runningIndex,
			child.RetryCount,
		)

	case domain.TaskCanceled:
		return loopDecisionFail, fmt.Errorf(
			"loop iteration %d child task canceled (retry_count=%d), binding preserved, parent retry or manual intervention required",
			runningIndex,
			child.RetryCount,
		)

	default:
		return loopDecisionFail, fmt.Errorf(
			"loop iteration %d child task in unexpected status=%s",
			runningIndex,
			child.Status,
		)
	}
}

func (l *LoopNodeStep) aggregateSuccessfulIteration(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	child *domain.Task,
	runningIndex int,
) error {
	if runtime == nil || runtime.Checkpoint == nil {
		return fmt.Errorf("loop runtime/checkpoint is nil")
	}
	if child == nil {
		return fmt.Errorf("loop child task is nil")
	}

	final, err := domain.ParseFinal(child.OutputJSON)
	if err != nil {
		return fmt.Errorf("loop parse child final failed: %w", err)
	}
	if final == nil {
		return fmt.Errorf("loop iteration %d final output is nil", runningIndex)
	}

	finalMap := taskOutputToMap(final)
	cp := runtime.Checkpoint

	// 1. append results
	results, _ := cp[loopCPResults].([]any)
	if results == nil {
		results = []any{}
	}
	results = append(results, finalMap)
	cp[loopCPResults] = results

	// 2. update carry state
	carryState, _ := cp[loopCPCarryState].(map[string]any)
	if carryState == nil {
		carryState = map[string]any{}
	}
	for inputKey, outputKey := range l.carry {
		if strings.TrimSpace(inputKey) == "" || strings.TrimSpace(outputKey) == "" {
			continue
		}
		if v, ok := finalMap[outputKey]; ok {
			carryState[inputKey] = v
		}
	}
	cp[loopCPCarryState] = carryState

	// 3. advance state machine
	cp[loopCPDone] = utils.ToInt(cp[loopCPDone]) + 1
	cp[loopCPCurrentIndex] = runningIndex + 1
	l.resetRunningBinding(cp)

	// 4. persist checkpoint
	runtime.Output = nil
	runtime.OutputHash = ""

	if err := execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime); err != nil {
		return fmt.Errorf("loop persist checkpoint failed: %w", err)
	}

	total := utils.ToInt(cp[loopCPTotal])
	done := utils.ToInt(cp[loopCPDone])

	progress := 0.0
	if total > 0 {
		progress = float64(done) / float64(total)
	}

	execCtx.EmitNodeEvent(NodeEvent{
		Type:     "progress",
		Message:  fmt.Sprintf("Loop第 %d 轮完成", runningIndex),
		Progress: progress,
	})
	l.emitFanoutProgress(execCtx, runtime)

	return nil
}

func (l *LoopNodeStep) dispatchNextIteration(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	item any,
	currentIndex int,
) error {
	if runtime == nil || runtime.Checkpoint == nil {
		return fmt.Errorf("loop runtime/checkpoint is nil")
	}

	cp := runtime.Checkpoint
	attemptToken := l.resolveAttemptToken(runtime, cp, currentIndex)

	subInput := buildLoopSubWorkflowInput(
		execCtx,
		cp,
		l.iterator,
		item,
		currentIndex,
		attemptToken,
	)

	subKey := runtimekeys.BuildSubWorkflowKey(
		execCtx.TaskContext.Task.ID,
		execCtx.NodeDef.Name,
		l.workflow,
		subInput,
	)

	cp[loopCPRunningIndex] = currentIndex
	cp[loopCPRunningSubKey] = subKey
	cp[loopCPRunningAttempt] = attemptToken

	if err := execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime); err != nil {
		return fmt.Errorf("loop persist running binding failed: %w", err)
	}

	_, err := execCtx.Executor.RunSubWorkflow(
		execCtx,
		l.workflow,
		subInput,
	)
	if err != nil {
		var suspendErr *domain.WorkflowSuspendedError
		if errorsAsSuspend(err, &suspendErr) {
			return nil
		}
		return fmt.Errorf("loop dispatch subworkflow failed: %w", err)
	}

	return nil
}

func (l *LoopNodeStep) buildFinalResults(cp map[string]any) ([]any, error) {
	if cp == nil {
		return nil, fmt.Errorf("loop checkpoint is nil")
	}

	raw, _ := cp[loopCPResults].([]any)
	if raw == nil {
		return []any{}, nil
	}

	out := make([]any, len(raw))
	for i := range raw {
		out[i] = deepCloneAny(raw[i])
	}
	return out, nil
}

func (l *LoopNodeStep) suspend() error {
	return &domain.WorkflowSuspendedError{
		Reason: domain.SuspendSubWorkflow,
	}
}

func (l *LoopNodeStep) initCheckpoint(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	total int,
) error {
	carryState := map[string]any{}

	for k, raw := range l.initial {
		val, err := l.resolveInitialValue(execCtx, raw)
		if err != nil {
			return fmt.Errorf("loop initial[%s] resolve failed: %w", k, err)
		}
		carryState[k] = val
	}

	runtime.Checkpoint = map[string]any{
		cpFanoutKind:            string(FanoutKindLoop),
		loopCPTotal:             total,
		loopCPCurrentIndex:      0,
		loopCPDone:              0,
		loopCPResults:           []any{},
		loopCPCarryState:        carryState,
		loopCPRunningIndex:      -1,
		loopCPRunningSubKey:     "",
		loopCPRunningAttempt:    "",
		loopCPAttemptSeqByIndex: map[string]any{},
	}

	return execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime)
}

func (l *LoopNodeStep) emitFanoutProgress(execCtx *NodeExecContext, runtime *domain.NodeRuntime) {
	if execCtx == nil || runtime == nil || runtime.Checkpoint == nil {
		return
	}
	cp := runtime.Checkpoint
	total := utils.ToInt(cp[loopCPTotal])
	done := utils.ToInt(cp[loopCPDone])
	runningIndex := utils.ToInt(cp[loopCPRunningIndex])
	running := 0
	currentIndex := utils.ToInt(cp[loopCPCurrentIndex])
	if runningIndex >= 0 {
		running = 1
		currentIndex = runningIndex + 1
	}
	EmitFanoutProgress(execCtx, FanoutProgress{
		Kind:         FanoutKindLoop,
		Total:        total,
		Done:         done,
		Running:      running,
		Failed:       0,
		CurrentIndex: currentIndex,
	})
}

func (l *LoopNodeStep) resolveInitialValue(execCtx *NodeExecContext, raw any) (any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return "", nil
		}
		if shouldEvalLoopExpr(s) {
			return execCtx.TaskContext.EvalAny(s)
		}
		return v, nil
	case bool, int, int32, int64, float32, float64:
		return v, nil
	case []any, map[string]any:
		return v, nil
	default:
		return v, nil
	}
}

func shouldEvalLoopExpr(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	exprHints := []string{
		"input.",
		"nodes.",
		"shot.",
		"index",
		"??",
		"==",
		"!=",
		"[",
		"]",
		"(",
		")",
		".output",
	}

	for _, hint := range exprHints {
		if strings.Contains(s, hint) {
			return true
		}
	}

	if strings.Contains(s, ".") &&
		!strings.HasPrefix(s, "http://") &&
		!strings.HasPrefix(s, "https://") &&
		!strings.Contains(s, " ") {
		return true
	}

	return false
}

func (l *LoopNodeStep) resetRunningBinding(cp map[string]any) {
	cp[loopCPRunningIndex] = -1
	cp[loopCPRunningSubKey] = ""
	cp[loopCPRunningAttempt] = ""
}

func (l *LoopNodeStep) resolveAttemptToken(
	runtime *domain.NodeRuntime,
	cp map[string]any,
	currentIndex int,
) string {
	if !shouldRecreateLoopRunningTask(runtime.ExecutionReason) {
		if v := strings.TrimSpace(utils.ToString(cp[loopCPRunningAttempt])); v != "" {
			return v
		}
	}

	seq := l.getAttemptSeq(cp, currentIndex)
	if seq <= 0 {
		seq = 1
		l.setAttemptSeq(cp, currentIndex, seq)
	}

	raw := fmt.Sprintf("%s|%d|%d", runtime.Name, currentIndex, seq)
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (l *LoopNodeStep) getAttemptSeq(cp map[string]any, index int) int {
	m, _ := cp[loopCPAttemptSeqByIndex].(map[string]any)
	if m == nil {
		return 0
	}
	return utils.ToInt(m[strconv.Itoa(index)])
}

func (l *LoopNodeStep) setAttemptSeq(cp map[string]any, index int, seq int) {
	m, _ := cp[loopCPAttemptSeqByIndex].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	m[strconv.Itoa(index)] = seq
	cp[loopCPAttemptSeqByIndex] = m
}

func (l *LoopNodeStep) bumpAttemptSeq(cp map[string]any, index int) int {
	seq := l.getAttemptSeq(cp, index)
	seq++
	if seq <= 0 {
		seq = 1
	}
	l.setAttemptSeq(cp, index, seq)
	return seq
}

func shouldRecreateLoopRunningTask(reason string) bool {
	switch reason {
	case "resume_boundary",
		"upstream_dirty",
		"input_changed",
		"missing_parent",
		"parent_not_ready",
		"input_resolve_fail":
		return true
	default:
		return false
	}
}

func taskOutputToMap(out *domain.TaskOutput) map[string]any {
	if out == nil {
		return map[string]any{}
	}

	result := map[string]any{
		"result_type":      out.ResultType,
		"primary_file_url": out.PrimaryFileUrl,
	}

	if out.CoverUrl != nil {
		result["cover_url"] = *out.CoverUrl
	}
	if out.PreviewUrl != nil {
		result["preview_url"] = *out.PreviewUrl
	}
	if out.Width != nil {
		result["width"] = *out.Width
	}
	if out.Height != nil {
		result["height"] = *out.Height
	}
	if out.Duration != nil {
		result["duration"] = *out.Duration
	}

	for k, v := range out.Extras {
		result[k] = v
	}

	return result
}

// 为了避免直接 import errors 与你项目里已有别名冲突，包一层
func errorsAsSuspend(err error, target **domain.WorkflowSuspendedError) bool {
	if err == nil {
		return false
	}
	se, ok := err.(*domain.WorkflowSuspendedError)
	if ok {
		*target = se
		return true
	}
	return false
}
