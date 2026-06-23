package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"flux/definition"
	"flux/domain"
	engine2 "flux/engine"
	repository2 "flux/repository"
	"flux/utils"
	"flux/workflow"
)

const (
	TaskNoRetryFound = "no retry root found task"
)

type RetryTrigger string

const (
	RetryTriggerManual   RetryTrigger = "manual"
	RetryTriggerRecovery RetryTrigger = "recovery_scanner"
)

type TaskRetryService interface {
	PrepareTaskRetry(ctx context.Context, taskID int64, trigger RetryTrigger, resumeFrom string, patches []domain.RuntimePatch) error
	ClearMapChildCheckpointResult(ctx context.Context, parentTaskID int64, nodeName string, childTaskID int64) error
}

type taskRetryService struct {
	workflowVersionRepo repository2.WorkflowVersionRepository
	taskRepo            repository2.TaskRepository
	nodeRuntimeRepo     repository2.NodeRuntimeRepository
	awaitBindingRepo    repository2.AwaitBindingRepository
	builder             *workflow.Builder
}

func NewTaskRetryService(
	workflowVersionRepo repository2.WorkflowVersionRepository,
	taskRepo repository2.TaskRepository,
	nodeRuntimeRepo repository2.NodeRuntimeRepository,
	awaitBindingRepo repository2.AwaitBindingRepository,
	builder *workflow.Builder,
) TaskRetryService {
	return &taskRetryService{
		workflowVersionRepo: workflowVersionRepo,
		taskRepo:            taskRepo,
		nodeRuntimeRepo:     nodeRuntimeRepo,
		awaitBindingRepo:    awaitBindingRepo,
		builder:             builder,
	}
}

func (s *taskRetryService) PrepareTaskRetry(
	ctx context.Context,
	taskID int64,
	trigger RetryTrigger,
	resumeFrom string,
	patches []domain.RuntimePatch,
) error {
	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task not found")
	}

	if task.Status != domain.TaskFailed &&
		task.Status != domain.TaskCanceled &&
		task.Status != domain.TaskSuspended &&
		task.Status != domain.TaskRunning {
		return fmt.Errorf("task is not retryable, current status=%s", task.Status)
	}

	// 自动恢复融断：超过最大重试次数不再自动恢复，防止死循环消耗 API 费用。
	// 手动重试不受此限制。
	if trigger == RetryTriggerRecovery && task.RetryCount >= domain.MaxAutoRetryCount {
		return fmt.Errorf("task auto retry exhausted: retry_count=%d, max=%d", task.RetryCount, domain.MaxAutoRetryCount)
	}

	dbWorkflowVersion, err := s.workflowVersionRepo.Get(ctx, task.WorkflowVersionID)
	if err != nil {
		return err
	}

	var def definition.WorkflowDefinition
	if err := json.Unmarshal(dbWorkflowVersion.DefinitionJSON, &def); err != nil {
		return err
	}

	wf, err := s.builder.Build(&def)
	if err != nil {
		return err
	}

	runtimes, err := s.nodeRuntimeRepo.FindByTaskID(ctx, taskID)
	if err != nil {
		return err
	}

	runtimeMap := make(map[string]*domain.NodeRuntime, len(runtimes))
	for _, r := range runtimes {
		if r == nil {
			continue
		}
		runtimeMap[r.Name] = r
	}

	protectedSubKeys, err := s.repairCompositeNodeCheckpointForRetry(ctx, taskID, wf, runtimeMap)
	if err != nil {
		return err
	}

	if err := s.cancelChildTasksForRetry(ctx, taskID, protectedSubKeys, trigger); err != nil {
		return err
	}

	if err := s.reviveProtectedChildrenForRetry(ctx, taskID, protectedSubKeys, trigger); err != nil {
		return err
	}

	// 如果指定了 resumeFrom，应用 patches 并使用 resumeFrom 作为重试根。
	isTargetedResume := strings.TrimSpace(resumeFrom) != ""
	if isTargetedResume {
		if _, ok := wf.Nodes()[resumeFrom]; !ok {
			return fmt.Errorf("resume_from node not found in workflow: %s", resumeFrom)
		}
		if _, ok := runtimeMap[resumeFrom]; !ok {
			return fmt.Errorf("resume_from node has no runtime: %s", resumeFrom)
		}
		// 状态闭合校验：从指定节点 resume 时，父状态必须可恢复
		if vr := engine2.ValidateParentStateClosure(runtimeMap, wf.Graph(), engine2.ClosureModeResume); !vr.Valid {
			for _, issue := range vr.Issues {
				if issue.Level == engine2.ClosureLevelBlock {
					return fmt.Errorf("parent state not closed: %s", issue.Message)
				}
			}
		}
		if len(patches) > 0 {
			if err := s.applyPatchesToNodeRuntimes(ctx, runtimeMap, patches); err != nil {
				return err
			}
		}
		retryRoots := []string{resumeFrom}
		resetSet := s.collectRetrySubtree(wf, retryRoots)
		if err := s.resetNodeRuntimeForTargetedResume(ctx, wf, runtimeMap, resetSet); err != nil {
			return err
		}
		if err := s.resetAwaitBindingsForRetry(ctx, taskID, resetSet); err != nil {
			return err
		}
	} else {
		failedRoots := s.collectRetryRoots(runtimes)
		if len(failedRoots) == 0 {
			failedRoots = s.collectNonTerminalRetryRoots(runtimes)
		}
		if len(failedRoots) == 0 {
			return fmt.Errorf(TaskNoRetryFound)
		}

		resetSet := s.collectRetrySubtree(wf, failedRoots)

		if err := s.resetNodeRuntimeForRetry(ctx, wf, runtimeMap, resetSet); err != nil {
			return err
		}
		if err := s.resetAwaitBindingsForRetry(ctx, taskID, resetSet); err != nil {
			return err
		}
	}

	task.Status = domain.TaskPending
	task.ErrorMessage = ""
	task.OutputJSON = nil
	task.Progress = 0
	if trigger == RetryTriggerManual {
		task.RetryCount = 0 // 手动重试：重置计数器
	} else {
		task.RetryCount++ // 自动恢复：递增计数器，用于融断
	}
	task.WorkerID = ""
	task.StartedAt = time.Time{}

	return s.taskRepo.Update(ctx, task)
}

func (s *taskRetryService) resetAwaitBindingsForRetry(
	ctx context.Context,
	taskID int64,
	resetSet map[string]struct{},
) error {
	if s.awaitBindingRepo == nil || len(resetSet) == 0 || taskID == 0 {
		return nil
	}

	bindings, err := s.awaitBindingRepo.ListByTaskID(ctx, taskID)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		if _, shouldReset := resetSet[binding.NodeName]; !shouldReset {
			continue
		}
		if isAwaitBindingTerminal(binding.Status) {
			continue
		}

		binding.Status = domain.AwaitBindingCanceled
		binding.ErrorMessage = "canceled by task retry"
		binding.CanceledAt = &now
		binding.NextPollAt = nil
		binding.LastEventID = nil
		binding.LastEventSource = nil
		binding.LastEventPayload = nil

		if err := s.awaitBindingRepo.Update(ctx, binding); err != nil {
			return err
		}
	}

	return nil
}

func isAwaitBindingTerminal(status domain.AwaitBindingStatus) bool {
	switch status {
	case domain.AwaitBindingCompleted,
		domain.AwaitBindingFailed,
		domain.AwaitBindingTimedOut,
		domain.AwaitBindingCanceled:
		return true
	default:
		return false
	}
}

func (s *taskRetryService) reviveProtectedChildrenForRetry(
	ctx context.Context,
	parentTaskID int64,
	protectedSubKeys map[string]struct{},
	trigger RetryTrigger,
) error {
	if len(protectedSubKeys) == 0 {
		return nil
	}

	children, err := s.taskRepo.ListChildrenByParentID(ctx, parentTaskID)
	if err != nil {
		return err
	}

	for _, child := range children {
		if child == nil || child.SubKey == nil {
			continue
		}

		subKey := strings.TrimSpace(*child.SubKey)
		if subKey == "" {
			continue
		}

		if _, ok := protectedSubKeys[subKey]; !ok {
			continue
		}

		switch child.Status {
		case domain.TaskFailed, domain.TaskCanceled:
			if err := s.PrepareTaskRetry(ctx, child.ID, trigger, "", nil); err != nil {
				return err
			}
			if err := s.taskRepo.Enqueue(ctx, child.ID); err != nil {
				return err
			}
		case domain.TaskPending:
			if err := s.taskRepo.Enqueue(ctx, child.ID); err != nil {
				return err
			}
		case domain.TaskRunning, domain.TaskSuspended, domain.TaskSuccess:
			continue
		}
	}

	return nil
}

func (s *taskRetryService) cancelChildTasksForRetry(
	ctx context.Context,
	parentTaskID int64,
	protectedSubKeys map[string]struct{},
	trigger RetryTrigger,
) error {
	// 自动恢复(recovery)的语义是“原地续跑”,不是“丢弃重来”:此时绝不能取消任何非终态子任务。
	// 否则会误伤还在跑(甚至已完成)的 subworkflow 子任务,既丢失算力/费用,又会在父任务重跑
	// 该节点时用相同 sub_key 触发 INSERT 冲突。父任务重跑各 subworkflow/map/loop 节点时,
	// RunSubWorkflow 会按 sub_key 对账复用每个子任务(success 复用结果、pending/running/suspended
	// 继续等、failed/canceled 复活),不需要也不应该在这里清理。
	// 只有显式重来(manual 重试 / fork)才需要清理旧子任务,避免脏结果污染新一轮执行。
	if trigger == RetryTriggerRecovery {
		return nil
	}

	children, err := s.taskRepo.ListChildrenByParentID(ctx, parentTaskID)
	if err != nil {
		return err
	}

	for _, child := range children {
		if child == nil {
			continue
		}

		if child.SubKey != nil {
			subKey := strings.TrimSpace(*child.SubKey)
			if subKey != "" {
				if _, ok := protectedSubKeys[subKey]; ok {
					continue
				}
			}
		}

		switch child.Status {
		case domain.TaskPending, domain.TaskRunning, domain.TaskSuspended:
			child.Status = domain.TaskCanceled
			child.ErrorMessage = "canceled by parent retry"
			child.OutputJSON = nil
			child.Progress = 0

			if err := s.taskRepo.Update(ctx, child); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *taskRetryService) repairCompositeNodeCheckpointForRetry(
	ctx context.Context,
	parentTaskID int64,
	wf workflow.Workflow,
	runtimeMap map[string]*domain.NodeRuntime,
) (map[string]struct{}, error) {
	protectedSubKeys := map[string]struct{}{}
	nodeDefs := wf.Nodes()

	for nodeName, runtime := range runtimeMap {
		if runtime == nil || runtime.Checkpoint == nil {
			continue
		}

		nodeDef, ok := nodeDefs[nodeName]
		if !ok {
			continue
		}

		switch nodeDef.Type {
		case definition.NodeLoop:
			changed, keepSubKey, err := s.repairLoopCheckpointForRetry(ctx, parentTaskID, runtime)
			if err != nil {
				return nil, err
			}
			if keepSubKey != "" {
				protectedSubKeys[keepSubKey] = struct{}{}
			}
			if changed {
				if err := s.nodeRuntimeRepo.Update(ctx, runtime); err != nil {
					return nil, err
				}
			}

		case definition.NodeMap:
			changed, keepSubKeys, err := s.repairMapCheckpointForRetry(ctx, parentTaskID, runtime)
			if err != nil {
				return nil, err
			}
			for _, subKey := range keepSubKeys {
				if subKey == "" {
					continue
				}
				protectedSubKeys[subKey] = struct{}{}
			}
			if changed {
				if err := s.nodeRuntimeRepo.Update(ctx, runtime); err != nil {
					return nil, err
				}
			}
		}
	}

	return protectedSubKeys, nil
}

func (s *taskRetryService) repairLoopCheckpointForRetry(
	ctx context.Context,
	parentTaskID int64,
	runtime *domain.NodeRuntime,
) (changed bool, keepSubKey string, err error) {
	if runtime == nil || runtime.Checkpoint == nil ||
		runtime.State == domain.NodeSuccess || runtime.State == domain.NodeSuccessPendingEdges {
		return false, "", nil
	}

	cp := runtime.Checkpoint
	runningIndex := utils.ToInt(cp["running_index"])
	runningSubKey := strings.TrimSpace(utils.ToString(cp["running_sub_key"]))

	if runningIndex == -1 || runningSubKey == "" {
		return false, "", nil
	}

	child, err := s.taskRepo.FindBySubKey(ctx, runningSubKey)
	if err != nil {
		return false, runningSubKey, nil
	}

	if child == nil {
		bumpLoopAttemptAndClearBinding(cp, runningIndex)
		runtime.Output = nil
		runtime.OutputHash = ""
		return true, "", nil
	}

	if child.ParentID == nil || *child.ParentID != parentTaskID {
		bumpLoopAttemptAndClearBinding(cp, runningIndex)
		runtime.Output = nil
		runtime.OutputHash = ""
		return true, "", nil
	}

	if child.ParentNode == nil || *child.ParentNode != runtime.Name {
		bumpLoopAttemptAndClearBinding(cp, runningIndex)
		runtime.Output = nil
		runtime.OutputHash = ""
		return true, "", nil
	}

	return false, runningSubKey, nil
}

func (s *taskRetryService) collectRetryRoots(runtimes []*domain.NodeRuntime) []string {
	out := make([]string, 0)
	for _, r := range runtimes {
		if r == nil {
			continue
		}
		switch r.State {
		case domain.NodeFailed,
			domain.NodeFailedPendingEdges,
			domain.NodeRunning,
			domain.NodeRetrying,
			domain.NodeCanceled,
			domain.NodeReady:
			out = append(out, r.Name)
		}
	}
	return out
}

func (s *taskRetryService) collectNonTerminalRetryRoots(runtimes []*domain.NodeRuntime) []string {
	out := make([]string, 0)
	for _, r := range runtimes {
		if r == nil {
			continue
		}
		switch r.State {
		case domain.NodePending,
			domain.NodeRunning,
			domain.NodeRetrying,
			domain.NodeReady:
			out = append(out, r.Name)
		}
	}
	return out
}

func (s *taskRetryService) collectRetrySubtree(
	wf workflow.Workflow,
	failedRoots []string,
) map[string]struct{} {
	resetSet := make(map[string]struct{})
	queue := append([]string{}, failedRoots...)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if _, ok := resetSet[cur]; ok {
			continue
		}
		resetSet[cur] = struct{}{}

		for _, child := range wf.Graph().Children[cur] {
			queue = append(queue, child)
		}
	}

	return resetSet
}

func (s *taskRetryService) resetNodeRuntimeForRetry(
	ctx context.Context,
	wf workflow.Workflow,
	runtimeMap map[string]*domain.NodeRuntime,
	resetSet map[string]struct{},
) error {
	nodeDefs := wf.Nodes()

	for name := range resetSet {
		r := runtimeMap[name]
		if r == nil {
			continue
		}

		if r.State == domain.NodeSuccess || r.State == domain.NodeSuccessPendingEdges {
			continue
		}

		nodeDef, _ := nodeDefs[name]

		r.State = domain.NodePending
		r.Error = ""
		r.Progress = 0
		r.StartedAt = nil
		r.FinishedAt = nil
		r.LastHeartbeat = nil
		r.ActivatedEdges = nil
		r.ResolvedInput = nil
		r.InputHash = ""
		r.OutputHash = ""
		r.Output = nil

		if nodeDef.Type != definition.NodeLoop && nodeDef.Type != definition.NodeMap {
			r.Checkpoint = nil
		}

		if err := s.nodeRuntimeRepo.Update(ctx, r); err != nil {
			return err
		}
	}

	return nil
}

func bumpLoopAttemptAndClearBinding(cp map[string]any, runningIndex int) {
	if cp == nil {
		return
	}

	seqMap, _ := cp["attempt_seq_by_index"].(map[string]any)
	if seqMap == nil {
		seqMap = map[string]any{}
	}

	key := strconv.Itoa(runningIndex)
	seq := utils.ToInt(seqMap[key])
	seq++
	if seq <= 0 {
		seq = 1
	}
	seqMap[key] = seq
	cp["attempt_seq_by_index"] = seqMap

	cp["running_index"] = -1
	cp["running_sub_key"] = ""
	cp["running_attempt_token"] = ""
}

func (s *taskRetryService) repairMapCheckpointForRetry(
	ctx context.Context,
	parentTaskID int64,
	runtime *domain.NodeRuntime,
) (changed bool, keepSubKeys []string, err error) {
	if runtime == nil ||
		runtime.Checkpoint == nil ||
		runtime.State == domain.NodeSuccess ||
		runtime.State == domain.NodeSuccessPendingEdges {
		return false, nil, nil
	}

	children, err := s.taskRepo.ListByParentNode(ctx, parentTaskID, runtime.Name)
	if err != nil {
		return false, nil, err
	}

	runtime.Output = nil
	runtime.OutputHash = ""
	keepSubKeys = collectMapRetrySubKeys(runtime.Checkpoint, children)
	return true, keepSubKeys, nil
}

func collectMapRetrySubKeys(cp map[string]any, children []*domain.Task) []string {
	if cp == nil || len(children) == 0 {
		return nil
	}

	protected := make([]string, 0)
	seen := map[string]struct{}{}

	for _, child := range children {
		if child == nil || child.SubKey == nil {
			continue
		}

		subKey := strings.TrimSpace(*child.SubKey)
		if subKey == "" {
			continue
		}

		index, err := getMapChildIndexForRetry(child)
		if err != nil {
			continue
		}

		if mapCheckpointHasResult(cp, index) {
			continue
		}

		if _, ok := seen[subKey]; ok {
			continue
		}
		seen[subKey] = struct{}{}
		protected = append(protected, subKey)
	}

	return protected
}

func mapCheckpointHasResult(cp map[string]any, index int) bool {
	if cp == nil {
		return false
	}

	results, _ := cp["results"].(map[string]any)
	if results == nil {
		return false
	}

	_, ok := results[strconv.Itoa(index)]
	return ok
}

func getMapChildIndexForRetry(task *domain.Task) (int, error) {
	if task == nil {
		return 0, fmt.Errorf("task is nil")
	}

	if task.MapIndex != nil {
		return *task.MapIndex, nil
	}

	if len(task.InputJSON) == 0 {
		return 0, fmt.Errorf("map child missing input json")
	}

	var input map[string]any
	if err := json.Unmarshal(task.InputJSON, &input); err != nil {
		return 0, err
	}

	switch v := input["index"].(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float32:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("map child missing index metadata")
	}
}

// ClearMapChildCheckpointResult 幂等清理父任务 map 节点 checkpoint 中指定子任务的结果。
// 只在 cp["results"][index] 实际存在时才 delete + decrement done，确保重复调用安全。
func (s *taskRetryService) ClearMapChildCheckpointResult(
	ctx context.Context,
	parentTaskID int64,
	nodeName string,
	childTaskID int64,
) error {
	child, err := s.taskRepo.GetByID(ctx, childTaskID)
	if err != nil {
		return fmt.Errorf("load child task %d: %w", childTaskID, err)
	}
	if child == nil {
		return fmt.Errorf("child task %d not found", childTaskID)
	}

	childIndex, err := getMapChildIndexForRetry(child)
	if err != nil {
		return fmt.Errorf("resolve child %d map index: %w", childTaskID, err)
	}
	indexKey := strconv.Itoa(childIndex)

	runtimes, err := s.nodeRuntimeRepo.FindByTaskID(ctx, parentTaskID)
	if err != nil {
		return fmt.Errorf("load parent %d node runtimes: %w", parentTaskID, err)
	}

	var mapRuntime *domain.NodeRuntime
	for _, rt := range runtimes {
		if rt != nil && rt.Name == nodeName {
			mapRuntime = rt
			break
		}
	}
	if mapRuntime == nil {
		return fmt.Errorf("parent node %s runtime not found in task %d", nodeName, parentTaskID)
	}

	cp := mapRuntime.Checkpoint
	if cp == nil {
		return nil // 没有 checkpoint，无需清理
	}

	results, _ := cp["results"].(map[string]any)
	if results == nil {
		results = map[string]any{}
		cp["results"] = results
	}

	// 幂等：只在 result 实际存在时才删除并 decrement done
	if _, ok := results[indexKey]; !ok {
		return nil // 已经不存在，无需清理
	}

	delete(results, indexKey)

	if itemHashes, ok := cp["item_hashes"].(map[string]any); ok {
		delete(itemHashes, indexKey)
	}
	if reusedItems, ok := cp["reused_items"].(map[string]any); ok {
		delete(reusedItems, indexKey)
	}

	done := utils.ToInt(cp["done"])
	done = max(done-1, 0)
	cp["done"] = done

	return s.nodeRuntimeRepo.Update(ctx, mapRuntime)
}

// applyPatchesToNodeRuntimes 将 patches 直接应用到对应节点的 Output 字段。
// 仅处理 target == "node_output"，因为 resume 场景下不需要修改 checkpoint。
func (s *taskRetryService) applyPatchesToNodeRuntimes(
	ctx context.Context,
	runtimeMap map[string]*domain.NodeRuntime,
	patches []domain.RuntimePatch,
) error {
	for _, p := range patches {
		if p.Target != domain.PatchTargetNodeOutput {
			continue
		}
		rt, ok := runtimeMap[p.Node]
		if !ok || rt == nil {
			return fmt.Errorf("patch target node not found in runtime: %s", p.Node)
		}
		if rt.Output == nil {
			rt.Output = map[string]any{}
		}
		switch p.Op {
		case domain.PatchOpSet:
			if err := engine2.SetByPath(rt.Output, p.Path, p.Value); err != nil {
				return fmt.Errorf("apply patch set node=%s path=%s: %w", p.Node, p.Path, err)
			}
		case domain.PatchOpDelete:
			if err := engine2.DeleteByPath(rt.Output, p.Path); err != nil {
				return fmt.Errorf("apply patch delete node=%s path=%s: %w", p.Node, p.Path, err)
			}
		case domain.PatchOpMerge:
			if err := engine2.MergeByPath(rt.Output, p.Path, p.Value); err != nil {
				return fmt.Errorf("apply patch merge node=%s path=%s: %w", p.Node, p.Path, err)
			}
		default:
			return fmt.Errorf("unsupported patch op: %s", p.Op)
		}
		if err := s.nodeRuntimeRepo.Update(ctx, rt); err != nil {
			return fmt.Errorf("update patched node runtime %s: %w", p.Node, err)
		}
	}
	return nil
}

// resetNodeRuntimeForTargetedResume 和 resetNodeRuntimeForRetry 类似，
// 但会重置子树内的 success 节点（因为 resumeFrom 重新执行后下游结果不再有效）。
func (s *taskRetryService) resetNodeRuntimeForTargetedResume(
	ctx context.Context,
	wf workflow.Workflow,
	runtimeMap map[string]*domain.NodeRuntime,
	resetSet map[string]struct{},
) error {
	nodeDefs := wf.Nodes()

	for name := range resetSet {
		r := runtimeMap[name]
		if r == nil {
			continue
		}

		// 与 resetNodeRuntimeForRetry 不同：targeted resume 也重置 success 节点。
		r.State = domain.NodePending
		r.Error = ""
		r.Progress = 0
		r.StartedAt = nil
		r.FinishedAt = nil
		r.LastHeartbeat = nil
		r.ActivatedEdges = nil
		r.ResolvedInput = nil
		r.InputHash = ""
		r.OutputHash = ""
		r.Output = nil

		nodeDef, _ := nodeDefs[name]
		if nodeDef.Type != definition.NodeLoop && nodeDef.Type != definition.NodeMap {
			r.Checkpoint = nil
		}

		if err := s.nodeRuntimeRepo.Update(ctx, r); err != nil {
			return err
		}
	}

	return nil
}
