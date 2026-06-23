package nodes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flux/domain"
	"flux/tool"
	"flux/utils"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const (
	mapCPTotal            = "total"
	mapCPDone             = "done"
	mapCPResults          = "results"
	mapCPItemHashes       = "item_hashes"
	mapCPReusedItems      = "reused_items"
	mapCPFailurePolicy    = "failure_policy"
	mapCPMaxChildRetries  = "max_child_retries"
	mapCPFallbackSource   = "fallback_source"
	mapCPMaxFallbackRatio = "max_fallback_ratio"
)

type MapNodeStep struct {
	itemsExpr        string
	iterator         string
	workflow         string
	parallel         int
	failurePolicy    string  // "fail_fast" | "partial"
	maxChildRetries  int     // -1 = use global default; phase 1 only 0 is fully supported
	fallbackSource   string  // "item"
	maxFallbackRatio float64 // 0.0 = no limit; >0 = enforce in Phase 2
}

func NewMapStep(
	items string,
	iterator string,
	workflow string,
	parallel int,
) *MapNodeStep {
	if parallel <= 0 {
		parallel = 5
	}
	return &MapNodeStep{
		itemsExpr:       items,
		iterator:        iterator,
		workflow:        workflow,
		parallel:        parallel,
		failurePolicy:   "fail_fast",
		maxChildRetries: -1,
		fallbackSource:  "item",
	}
}

// WithFailurePolicy sets the failure policy for this map step.
func (m *MapNodeStep) WithFailurePolicy(policy string) *MapNodeStep {
	if policy == "partial" {
		m.failurePolicy = "partial"
	}
	return m
}

// WithMaxChildRetries sets the max child-level retries.
func (m *MapNodeStep) WithMaxChildRetries(retries int) *MapNodeStep {
	m.maxChildRetries = retries
	return m
}

// WithFallbackSource sets the fallback data source.
func (m *MapNodeStep) WithFallbackSource(source string) *MapNodeStep {
	if source != "" {
		m.fallbackSource = source
	}
	return m
}

// WithMaxFallbackRatio sets the max allowed fallback ratio (Phase 2 enforcement).
func (m *MapNodeStep) WithMaxFallbackRatio(ratio float64) *MapNodeStep {
	m.maxFallbackRatio = ratio
	return m
}

func (m *MapNodeStep) Name() string {
	return "map"
}

func (m *MapNodeStep) RetryPolicy() RetryPolicy {
	return RetryPolicy{}
}

func (m *MapNodeStep) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}

func (m *MapNodeStep) InputSchema() tool.DataSchema {
	return tool.DataSchema{}
}

func (m *MapNodeStep) OutputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"results": {
				Type: "array",
			},
		},
	}
}

func (m *MapNodeStep) Run(execCtx *NodeExecContext) error {
	execCtx.EmitNodeEvent(NodeEvent{
		Type:    "started",
		Message: "Map任务开始",
	})

	items, err := resolveItems(m.itemsExpr, execCtx)
	if err != nil {
		return fmt.Errorf("map resolve items failed: %w", err)
	}

	runtime := execCtx.TaskContext.Runtime[execCtx.NodeDef.Name]
	if runtime == nil {
		return fmt.Errorf("map runtime not found: %s", execCtx.NodeDef.Name)
	}

	// 1. empty fast path
	if len(items) == 0 {
		return m.completeEmpty(execCtx, runtime)
	}

	// 2. init checkpoint once
	if runtime.Checkpoint == nil {
		if err := m.initCheckpoint(execCtx, runtime, len(items)); err != nil {
			return err
		}
		m.emitFanoutProgress(execCtx, runtime)
	}

	// 3. reconcile checkpoint total with current items length
	if err := m.ensureCheckpointConsistency(execCtx, runtime, len(items)); err != nil {
		return err
	}

	// 4. fan-in existing finished children
	if err := m.processExistingChildren(execCtx, runtime, items); err != nil {
		return err
	}
	m.emitFanoutProgress(execCtx, runtime)

	// 5. fill reusable items from parent snapshot
	if err := m.fillReusableItems(execCtx, runtime, items); err != nil {
		return err
	}
	m.emitFanoutProgress(execCtx, runtime)

	// 6. if already done, finalize directly
	done, total := m.readDoneTotal(runtime.Checkpoint)
	if done >= total {
		return m.finalizeCompleted(execCtx, runtime)
	}

	// 7. fan-out missing children
	if err := m.dispatchPendingChildren(execCtx, runtime, items); err != nil {
		return err
	}
	m.emitFanoutProgress(execCtx, runtime)

	// 8. dispatch 后再看一遍，有可能全部都是复用或刚好都已聚合
	done, total = m.readDoneTotal(runtime.Checkpoint)
	if done >= total {
		return m.finalizeCompleted(execCtx, runtime)
	}

	// 9. not completed yet -> suspend
	return &domain.WorkflowSuspendedError{
		Reason: domain.SuspendSubWorkflow,
	}
}

func (m *MapNodeStep) completeEmpty(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
) error {
	empty := []any{}
	execCtx.SetOutput("results", empty)

	runtime.Output = map[string]any{
		"results": empty,
	}
	runtime.OutputHash = calculateOutputHash(runtime.Output)

	clearNodeReuseMetadata(runtime)
	runtime.ReuseKind = domain.ReuseNone

	return execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime)
}

func (m *MapNodeStep) initCheckpoint(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	total int,
) error {
	runtime.Checkpoint = map[string]any{
		cpFanoutKind:          string(FanoutKindMap),
		mapCPTotal:            total,
		mapCPDone:             0,
		mapCPResults:          map[string]any{},
		mapCPItemHashes:       map[string]any{},
		mapCPReusedItems:      map[string]any{},
		mapCPFailurePolicy:    m.failurePolicy,
		mapCPMaxChildRetries:  m.maxChildRetries,
		mapCPFallbackSource:   m.fallbackSource,
		mapCPMaxFallbackRatio: m.maxFallbackRatio,
	}
	runtime.Output = nil
	runtime.OutputHash = ""

	return execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime)
}

func (m *MapNodeStep) ensureCheckpointConsistency(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	total int,
) error {
	if runtime == nil || runtime.Checkpoint == nil {
		return fmt.Errorf("map checkpoint missing")
	}

	cp := runtime.Checkpoint
	oldTotal := utils.ToInt(cp[mapCPTotal])

	// 当前先采用保守策略：
	// 非 fork/replay 场景下 total 变化通常意味着输入漂移，不应该静默继续。
	// 这里允许 total=0 时初始化；否则要求一致。
	if oldTotal == 0 {
		cp[mapCPTotal] = total
		return execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime)
	}

	if oldTotal != total {
		return fmt.Errorf("map checkpoint total mismatch: checkpoint=%d current=%d", oldTotal, total)
	}

	return nil
}

func (m *MapNodeStep) processExistingChildren(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	items []any,
) error {
	existingTasks, err := execCtx.Executor.TaskRepo().ListByParentNode(
		execCtx.TaskContext.Ctx,
		execCtx.TaskContext.Task.ID,
		execCtx.NodeDef.Name,
	)
	if err != nil {
		return fmt.Errorf("map list existing children failed: %w", err)
	}

	if runtime.Checkpoint == nil {
		runtime.Checkpoint = map[string]any{}
	}

	var changed bool

	for _, t := range existingTasks {
		if t == nil {
			continue
		}

		index, err := getSubTaskIndex(t)
		if err != nil {
			continue
		}

		// 已经聚合过了，直接跳过
		if m.hasCheckpointResult(runtime.Checkpoint, index) {
			continue
		}

		switch t.Status {
		case domain.TaskSuccess:
			final, err := utils.ParseFinal(t.OutputJSON)
			if err != nil || final == nil {
				continue
			}

			itemHash := ""
			subInput := parseTaskInput(t.InputJSON)
			if v, ok := subInput["__map_item_hash"].(string); ok {
				itemHash = v
			}

			runtime.WriteMapItemResult(index, itemHash, final, false)
			changed = true

		case domain.TaskFailed:
			if m.failurePolicy == "partial" {
				// partial 模式：写 fallback result，不返回 error
				fallback := m.buildFallbackResult(index, t, items)
				itemHash := m.getItemHash(items, index)
				runtime.WriteMapItemResult(index, itemHash, fallback, false)
				changed = true
				continue
			}
			// fail_fast（默认）：任一子任务失败，Map 本身失败
			return fmt.Errorf(
				"map child task failed at index=%d, task_id=%d, retry_count=%d",
				index, t.ID, t.RetryCount,
			)

		case domain.TaskCanceled:
			if m.failurePolicy == "partial" {
				// 取消的子任务也走 fallback
				fallback := m.buildFallbackResult(index, t, items)
				itemHash := m.getItemHash(items, index)
				runtime.WriteMapItemResult(index, itemHash, fallback, false)
				changed = true
				continue
			}
			return fmt.Errorf(
				"map child task canceled at index=%d, task_id=%d, retry_count=%d",
				index, t.ID, t.RetryCount,
			)

		case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended:
			// 仍在执行，不处理
			continue

		default:
			continue
		}
	}

	if changed {
		runtime.Output = nil
		runtime.OutputHash = ""
		if err := execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime); err != nil {
			return fmt.Errorf("map persist aggregated checkpoint failed: %w", err)
		}
	}

	return nil
}

func (m *MapNodeStep) fillReusableItems(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	items []any,
) error {
	if runtime == nil || runtime.Checkpoint == nil {
		return fmt.Errorf("map checkpoint missing")
	}

	changed := false

	for i, item := range items {
		if m.hasCheckpointResult(runtime.Checkpoint, i) {
			continue
		}

		itemHash := CalculateMapItemHash(item)

		reused, reusedResult := tryReuseMapItem(execCtx, i, itemHash)
		if !reused {
			continue
		}

		runtime.WriteMapItemResult(i, itemHash, reusedResult, true)
		changed = true
	}

	if changed {
		runtime.Output = nil
		runtime.OutputHash = ""
		if err := execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime); err != nil {
			return fmt.Errorf("map persist reused checkpoint failed: %w", err)
		}
	}

	return nil
}

func (m *MapNodeStep) dispatchPendingChildren(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
	items []any,
) error {
	if runtime == nil || runtime.Checkpoint == nil {
		return fmt.Errorf("map checkpoint missing")
	}

	activeCount, occupiedIndexes, err := m.collectOccupiedChildIndexes(execCtx)
	if err != nil {
		return err
	}

	cp := runtime.Checkpoint

	for i, item := range items {
		if activeCount >= m.parallel {
			break
		}

		itemHash := CalculateMapItemHash(item)

		// 已经完成聚合
		if m.hasCheckpointResult(cp, i) {
			continue
		}
		if occupiedIndexes[i] {
			continue
		}

		subInput := buildMapSubWorkflowInput(execCtx, m.iterator, item, i, itemHash)

		_, err := execCtx.Executor.RunSubWorkflow(
			execCtx,
			m.workflow,
			subInput,
		)
		if err != nil {
			var suspendErr *domain.WorkflowSuspendedError
			if errors.As(err, &suspendErr) {
				// 说明子任务已存在或已成功入队，这属于正常 fanout 生命周期
				activeCount++
				occupiedIndexes[i] = true
				continue
			}
			return fmt.Errorf("map dispatch child failed at index=%d: %w", i, err)
		}

		activeCount++
		occupiedIndexes[i] = true
	}

	return nil
}

func (m *MapNodeStep) collectOccupiedChildIndexes(execCtx *NodeExecContext) (int, map[int]bool, error) {
	existingTasks, err := execCtx.Executor.TaskRepo().ListByParentNode(
		execCtx.TaskContext.Ctx,
		execCtx.TaskContext.Task.ID,
		execCtx.NodeDef.Name,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("map count active children failed: %w", err)
	}

	count := 0
	occupiedIndexes := map[int]bool{}
	for _, t := range existingTasks {
		if t == nil {
			continue
		}
		index, err := getSubTaskIndex(t)
		if err != nil {
			continue
		}
		occupiedIndexes[index] = true
		switch t.Status {
		case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended:
			count++
		}
	}
	return count, occupiedIndexes, nil
}

func (m *MapNodeStep) finalizeCompleted(
	execCtx *NodeExecContext,
	runtime *domain.NodeRuntime,
) error {
	if runtime == nil || runtime.Checkpoint == nil {
		return fmt.Errorf("map checkpoint missing")
	}

	cp := runtime.Checkpoint
	total := utils.ToInt(cp[mapCPTotal])

	final, err := m.buildFinalResultsFromCheckpoint(cp, total)
	if err != nil {
		return err
	}

	// execution output
	execCtx.SetOutput("results", final)

	// runtime public output
	runtimeOutput := map[string]any{
		"results": deepCloneAny(final),
	}

	// partial success: quality semantics + meta + stats
	if m.failurePolicy == "partial" {
		// enrich each result item with quality fields（只追加，不破坏已有结构）
		final = m.enrichResultItems(final, cp)
		execCtx.SetOutput("results", final)
		runtimeOutput["results"] = deepCloneAny(final)

		meta, stats := m.buildPartialMeta(cp, total)
		failedCount := stats["fallback_count"].(int)
		if failedCount > 0 {
			runtimeOutput["partial_success"] = true
			runtimeOutput["failed_count"] = failedCount
			runtimeOutput["warnings"] = m.buildWarnings(cp, failedCount, total)
		}
		runtimeOutput["meta"] = meta
		for k, v := range stats {
			runtimeOutput[k] = v
		}

		// 同步写入 execCtx.Output，防止 executor 用 execCtx.Output 覆盖 runtime.Output
		execCtx.SetOutput("meta", meta)
		for k, v := range stats {
			execCtx.SetOutput(k, v)
		}
		if failedCount > 0 {
			execCtx.SetOutput("partial_success", true)
			execCtx.SetOutput("failed_count", failedCount)
			execCtx.SetOutput("warnings", runtimeOutput["warnings"])
		}
	}

	runtime.Output = runtimeOutput
	runtime.OutputHash = calculateOutputHash(runtime.Output)

	clearNodeReuseMetadata(runtime)
	runtime.ReuseKind = domain.ReuseNone
	if hasAnyReusedMapItems(cp) {
		runtime.ReuseKind = domain.ReuseMapItems
	}

	if err := execCtx.Executor.NodeRepo().Update(execCtx.TaskContext.Ctx, runtime); err != nil {
		return fmt.Errorf("map persist final output failed: %w", err)
	}
	m.emitFanoutProgress(execCtx, runtime)

	execCtx.EmitNodeEvent(NodeEvent{
		Type:     "progress",
		Message:  "Map任务完成",
		Progress: 1,
	})

	return nil
}

func (m *MapNodeStep) readDoneTotal(cp map[string]any) (done int, total int) {
	if cp == nil {
		return 0, 0
	}
	done = utils.ToInt(cp[mapCPDone])
	total = utils.ToInt(cp[mapCPTotal])
	return
}

func (m *MapNodeStep) emitFanoutProgress(execCtx *NodeExecContext, runtime *domain.NodeRuntime) {
	if execCtx == nil || runtime == nil || runtime.Checkpoint == nil {
		return
	}
	done, total := m.readDoneTotal(runtime.Checkpoint)
	running, failed, currentIndex := m.childRuntimeSummary(execCtx)
	EmitFanoutProgress(execCtx, FanoutProgress{
		Kind:         FanoutKindMap,
		Total:        total,
		Done:         done,
		Running:      running,
		Failed:       failed,
		Reused:       countMapEntries(runtime.Checkpoint[mapCPReusedItems]),
		CurrentIndex: currentIndex,
	})
}

func (m *MapNodeStep) childRuntimeSummary(execCtx *NodeExecContext) (running, failed, currentIndex int) {
	currentIndex = 0
	if execCtx == nil || execCtx.Executor == nil || execCtx.TaskContext == nil || execCtx.TaskContext.Task == nil {
		return 0, 0, 0
	}
	children, err := execCtx.Executor.TaskRepo().ListByParentNode(
		execCtx.TaskContext.Ctx,
		execCtx.TaskContext.Task.ID,
		execCtx.NodeDef.Name,
	)
	if err != nil {
		return 0, 0, 0
	}
	firstActive := -1
	for _, child := range children {
		if child == nil {
			continue
		}
		idx, _ := getSubTaskIndex(child)
		switch child.Status {
		case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended:
			running++
			if firstActive < 0 || idx < firstActive {
				firstActive = idx
			}
		case domain.TaskFailed, domain.TaskCanceled:
			failed++
		}
	}
	if firstActive >= 0 {
		currentIndex = firstActive + 1
	}
	return running, failed, currentIndex
}

func countMapEntries(v any) int {
	switch m := v.(type) {
	case map[string]any:
		return len(m)
	case map[int]bool:
		return len(m)
	}
	return 0
}

func (m *MapNodeStep) hasCheckpointResult(cp map[string]any, index int) bool {
	if cp == nil {
		return false
	}
	raw, _ := cp[mapCPResults].(map[string]any)
	if raw == nil {
		return false
	}
	_, ok := raw[strconv.Itoa(index)]
	return ok
}

func (m *MapNodeStep) buildFinalResultsFromCheckpoint(cp map[string]any, total int) ([]any, error) {
	raw, _ := cp[mapCPResults].(map[string]any)
	if raw == nil {
		return nil, fmt.Errorf("map checkpoint results missing")
	}

	final := make([]any, total)
	for i := 0; i < total; i++ {
		k := strconv.Itoa(i)
		v, ok := raw[k]
		if !ok {
			return nil, fmt.Errorf("map result missing index %d", i)
		}
		final[i] = deepCloneAny(v)
	}
	return final, nil
}

// CalculateMapItemHash 计算 item hash
func CalculateMapItemHash(item any) string {
	b, _ := json.Marshal(normalizeMapItemForHash(item))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func normalizeMapItemForHash(item any) any {
	switch v := item.(type) {
	case map[string]any:
		return utils.NormalizeMap(v)
	case []any:
		out := make([]any, 0, len(v))
		for _, elem := range v {
			out = append(out, normalizeMapItemForHash(elem))
		}
		return out
	default:
		if item != nil {
			rv := reflect.ValueOf(item)
			for rv.Kind() == reflect.Pointer {
				if rv.IsNil() {
					return nil
				}
				rv = rv.Elem()
			}
			if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
				out := make([]any, 0, rv.Len())
				for i := 0; i < rv.Len(); i++ {
					out = append(out, normalizeMapItemForHash(rv.Index(i).Interface()))
				}
				return out
			}
		}
		if utils.IsObject(item) {
			asMap, err := utils.ObjectToMap(item)
			if err == nil {
				return utils.NormalizeMap(asMap)
			}
		}
		return item
	}
}

// 父 run map item 复用函数
func tryReuseMapItem(
	execCtx *NodeExecContext,
	index int,
	itemHash string,
) (bool, map[string]any) {
	parent := execCtx.TaskContext.ParentSnapshot
	if parent == nil {
		return false, nil
	}

	if execCtx.TaskContext.MapItemReuse != nil {
		if nodeReuse, ok := execCtx.TaskContext.MapItemReuse[execCtx.NodeDef.Name]; ok {
			if !nodeReuse[index] {
				return false, nil
			}
		}
	}

	parentMapNode, ok := parent.Nodes[execCtx.NodeDef.Name]
	if !ok || parentMapNode == nil || parentMapNode.Checkpoint == nil {
		return false, nil
	}

	itemHashesRaw, _ := parentMapNode.Checkpoint[mapCPItemHashes].(map[string]any)
	resultsRaw, _ := parentMapNode.Checkpoint[mapCPResults].(map[string]any)
	if itemHashesRaw == nil || resultsRaw == nil {
		return false, nil
	}

	key := strconv.Itoa(index)

	oldHash, _ := itemHashesRaw[key].(string)
	if oldHash != itemHash {
		return false, nil
	}

	result, ok := resultsRaw[key].(map[string]any)
	if !ok || result == nil {
		return false, nil
	}

	return true, result
}

func resolveItems(expr string, ctx *NodeExecContext) ([]any, error) {
	val, err := resolveValue(expr, ctx)
	if err != nil {
		return nil, err
	}

	slice, ok := utils.ToAnySlice(val)
	if !ok {
		return nil, fmt.Errorf("invalid items expression: %s", expr)
	}
	return slice, nil
}

func resolveValue(path string, ctx *NodeExecContext) (any, error) {
	candidates := splitFallbackCandidates(path)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("invalid expr path")
	}

	var lastErr error
	for _, candidate := range candidates {
		val, err := resolveSingleValue(candidate, ctx)
		if err == nil {
			return val, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("invalid expr path")
	}
	return nil, lastErr
}

func splitFallbackCandidates(path string) []string {
	rawParts := strings.Split(path, "??")
	candidates := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		candidates = append(candidates, part)
	}
	return candidates
}

func resolveSingleValue(path string, ctx *NodeExecContext) (any, error) {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid expr path")
	}

	prefix := parts[0]

	if prefix == "input" {
		return descendValue(ctx.TaskContext.Input, parts[1:])
	}

	nodes, ok := ctx.TaskContext.Output["nodes"].(map[string]any)
	if !ok || nodes == nil {
		return nil, fmt.Errorf("nodes output not found")
	}

	if prefix == "nodes" {
		if len(parts) < 4 {
			return nil, fmt.Errorf("invalid nodes expr path")
		}
		nodeName := parts[1]
		node, ok := nodes[nodeName].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("node not found")
		}
		if parts[2] != "output" {
			return nil, fmt.Errorf("unsupported nodes expr path")
		}
		output, ok := node["output"]
		if !ok || output == nil {
			return nil, fmt.Errorf("node output not found")
		}
		return descendValue(output, parts[3:])
	}

	node, ok := nodes[prefix].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("node not found")
	}

	output, ok := node["output"]
	if !ok || output == nil {
		return nil, fmt.Errorf("node output not found")
	}

	return descendValue(output, parts[1:])
}

func descendValue(current any, parts []string) (any, error) {
	if len(parts) == 0 {
		return current, nil
	}

	part := parts[0]

	switch v := current.(type) {
	case map[string]any:
		next, ok := lookupObjectKey(v, part)
		if !ok {
			return nil, fmt.Errorf("field not found")
		}
		return descendValue(next, parts[1:])
	default:
		if utils.IsObject(current) {
			asMap, err := utils.ObjectToMap(current)
			if err != nil {
				return nil, fmt.Errorf("object to map failed: %w", err)
			}
			next, ok := lookupObjectKey(asMap, part)
			if !ok {
				return nil, fmt.Errorf("field not found")
			}
			return descendValue(next, parts[1:])
		}
		if len(parts) == 1 {
			return nil, fmt.Errorf("field not found")
		}
		return nil, fmt.Errorf("cannot descend into non-object value")
	}
}

func lookupObjectKey(obj map[string]any, key string) (any, bool) {
	if val, ok := obj[key]; ok {
		return val, true
	}

	lowerKey := strings.ToLower(strings.TrimSpace(key))
	for k, v := range obj {
		if strings.ToLower(strings.TrimSpace(k)) == lowerKey {
			return v, true
		}
	}

	if len(key) > 0 {
		jsonKey := lowerCamel(key)
		if val, ok := obj[jsonKey]; ok {
			return val, true
		}
	}

	return nil, false
}

func lowerCamel(s string) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) == 0 {
		return ""
	}
	rs[0] = []rune(strings.ToLower(string(rs[0])))[0]
	return string(rs)
}

func getSubTaskIndex(task *domain.Task) (int, error) {
	if task == nil {
		return 0, fmt.Errorf("task is nil")
	}

	if task.MapIndex != nil {
		return *task.MapIndex, nil
	}

	input := parseTaskInput(task.InputJSON)
	if input == nil {
		return 0, fmt.Errorf("map sub task missing input json")
	}

	if v, ok := input["index"]; ok {
		switch n := v.(type) {
		case int:
			return n, nil
		case int32:
			return int(n), nil
		case int64:
			return int(n), nil
		case float32:
			return int(n), nil
		case float64:
			return int(n), nil
		}
	}

	return 0, fmt.Errorf("map sub task missing index metadata")
}

func parseTaskInput(data []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func deepCloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCloneAny(v)
	}
	return dst
}

func deepCloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCloneMap(x)

	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = deepCloneAny(x[i])
		}
		return out

	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out

	case []int:
		out := make([]int, len(x))
		copy(out, x)
		return out

	case []int64:
		out := make([]int64, len(x))
		copy(out, x)
		return out

	case []float64:
		out := make([]float64, len(x))
		copy(out, x)
		return out

	case []bool:
		out := make([]bool, len(x))
		copy(out, x)
		return out

	case map[string]string:
		out := make(map[string]string, len(x))
		for k, v := range x {
			out[k] = v
		}
		return out

	case map[string]int:
		out := make(map[string]int, len(x))
		for k, v := range x {
			out[k] = v
		}
		return out

	case map[string]bool:
		out := make(map[string]bool, len(x))
		for k, v := range x {
			out[k] = v
		}
		return out

	default:
		return x
	}
}

func hasAnyReusedMapItems(cp map[string]any) bool {
	if cp == nil {
		return false
	}
	raw, _ := cp[mapCPReusedItems].(map[string]any)
	if raw == nil {
		return false
	}
	for _, v := range raw {
		if b, ok := v.(bool); ok && b {
			return true
		}
	}
	return false
}

func clearNodeReuseMetadata(runtime *domain.NodeRuntime) {
	if runtime == nil {
		return
	}
	runtime.IsInjected = false
	runtime.ReusedFromTaskID = nil
	runtime.ReusedFromNode = nil

	if runtime.ReuseKind == domain.ReuseNode {
		runtime.ReuseKind = domain.ReuseNone
	}
}

func calculateOutputHash(output map[string]any) string {
	if output == nil {
		return ""
	}
	normalized := utils.NormalizeMap(output)
	b, err := json.Marshal(normalized)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildFallbackResult 构造 partial 模式下的 fallback result。
// 从原始 item 中提取下游可消费的图片 URL 字段，确保 merge 等下游节点能识别。
func (m *MapNodeStep) buildFallbackResult(index int, child *domain.Task, items []any) map[string]any {
	errorMsg := child.ErrorMessage

	result := map[string]any{
		"index":           index,
		"status":          "fallback",
		"quality":         "low",
		"source":          "original",
		"degraded":        true,
		"fallback_used":   true,
		"fallback_source": m.fallbackSource,
		"error":           errorMsg,
	}

	// 从原始 item 填充
	if index >= 0 && index < len(items) {
		item := items[index]
		result["original_item"] = deepCloneAny(item)

		if itemMap, ok := item.(map[string]any); ok {
			// 提取图片 URL — merge_augmented_images 的 extractPrimaryFileURL 会检查 primary_file_url
			// augment_product_images 生成的 spec item 使用 source_image_url
			sourceURL := utils.ToString(itemMap["source_image_url"])
			if sourceURL == "" {
				sourceURL = utils.ToString(itemMap["image_url"])
			}
			if sourceURL == "" {
				sourceURL = utils.ToString(itemMap["primary_file_url"])
			}
			if sourceURL == "" {
				sourceURL = utils.ToString(itemMap["url"])
			}
			if sourceURL != "" {
				result["primary_file_url"] = sourceURL
				result["image_url"] = sourceURL
			}

			// 透传其他可能对下游有用的字段
			for _, key := range []string{"result_type", "width", "height", "aspect_ratio"} {
				if v, ok := itemMap[key]; ok && v != nil {
					result[key] = v
				}
			}
		}
	}

	return result
}

// getItemHash 返回指定 index 的 item hash。
func (m *MapNodeStep) getItemHash(items []any, index int) string {
	if index < 0 || index >= len(items) {
		return ""
	}
	return CalculateMapItemHash(items[index])
}

// countFallbacksInCheckpoint 统计 checkpoint results 中的 fallback 数量。
func (m *MapNodeStep) countFallbacksInCheckpoint(cp map[string]any) int {
	raw, _ := cp[mapCPResults].(map[string]any)
	if raw == nil {
		return 0
	}
	count := 0
	for _, v := range raw {
		if rm, ok := v.(map[string]any); ok {
			if s, _ := rm["status"].(string); s == "fallback" {
				count++
			}
		}
	}
	return count
}

// buildWarnings 为 partial success 输出构建 warnings 列表。
func (m *MapNodeStep) buildWarnings(cp map[string]any, failedCount, total int) []string {
	raw, _ := cp[mapCPResults].(map[string]any)
	if raw == nil {
		return nil
	}
	warnings := make([]string, 0, failedCount)
	for i := 0; i < total; i++ {
		key := strconv.Itoa(i)
		v, ok := raw[key]
		if !ok {
			continue
		}
		rm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := rm["status"].(string); s == "fallback" {
			errMsg := utils.ToString(rm["error"])
			if errMsg == "" {
				errMsg = "unknown error"
			}
			warnings = append(warnings, fmt.Sprintf(
				"子任务 index=%d 失败，已使用原始数据兜底: %s", i, errMsg,
			))
		}
	}
	return warnings
}

// enrichResultItems 为 results 数组中每个 item 补齐 quality 语义字段。
// 只追加字段，不覆盖已有 primary_file_url / image_url / result_type 等关键字段。
func (m *MapNodeStep) enrichResultItems(final []any, cp map[string]any) []any {
	if final == nil {
		return final
	}
	raw, _ := cp[mapCPResults].(map[string]any)

	for i, v := range final {
		rm, ok := v.(map[string]any)
		if !ok {
			continue
		}

		// 检查 checkpoint 中的原始状态
		key := strconv.Itoa(i)
		cpItem, _ := raw[key].(map[string]any)
		isFallback := cpItem != nil && cpItem["status"] == "fallback"

		if isFallback {
			// fallback item 在 buildFallbackResult 中已设置，此处确保不缺失
			if _, exists := rm["quality"]; !exists {
				rm["quality"] = "low"
			}
			if _, exists := rm["source"]; !exists {
				rm["source"] = "original"
			}
			if _, exists := rm["degraded"]; !exists {
				rm["degraded"] = true
			}
		} else {
			// success item 补默认 quality 标记
			if _, exists := rm["status"]; !exists {
				rm["status"] = "success"
			}
			if _, exists := rm["quality"]; !exists {
				rm["quality"] = "high"
			}
			if _, exists := rm["source"]; !exists {
				rm["source"] = "ai"
			}
			if _, exists := rm["degraded"]; !exists {
				rm["degraded"] = false
			}
		}

		final[i] = rm
	}
	return final
}

// resultIndexClassification holds classified index sets for partial meta output.
type resultIndexClassification struct {
	successIndexes  []int
	fallbackIndexes []int
}

// classifyResultIndexes 扫描 checkpoint results，分类 success/fallback 索引。
func classifyResultIndexes(cp map[string]any, total int) resultIndexClassification {
	raw, _ := cp[mapCPResults].(map[string]any)
	out := resultIndexClassification{
		successIndexes:  make([]int, 0, total),
		fallbackIndexes: make([]int, 0),
	}

	if raw == nil {
		for i := 0; i < total; i++ {
			out.successIndexes = append(out.successIndexes, i)
		}
		return out
	}

	for i := 0; i < total; i++ {
		key := strconv.Itoa(i)
		v, ok := raw[key]
		if !ok {
			continue
		}
		rm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if rm["status"] == "fallback" {
			out.fallbackIndexes = append(out.fallbackIndexes, i)
		} else {
			out.successIndexes = append(out.successIndexes, i)
		}
	}
	return out
}

// buildPartialMeta 为 partial success 构建 meta 和质量统计。
func (m *MapNodeStep) buildPartialMeta(cp map[string]any, total int) (meta map[string]any, stats map[string]any) {
	cls := classifyResultIndexes(cp, total)

	fallbackCount := len(cls.fallbackIndexes)
	successCount := len(cls.successIndexes)

	stats = map[string]any{
		"success_count":      successCount,
		"fallback_count":     fallbackCount,
		"high_quality_count": successCount,
		"low_quality_count":  fallbackCount,
	}
	if total > 0 {
		stats["fallback_rate"] = float64(fallbackCount) / float64(total)
	} else {
		stats["fallback_rate"] = 0.0
	}

	meta = map[string]any{
		"success_indexes":  cls.successIndexes,
		"fallback_indexes": cls.fallbackIndexes,
		"failed_indexes":   cls.fallbackIndexes,
	}

	return meta, stats
}
