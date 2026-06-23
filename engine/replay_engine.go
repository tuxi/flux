package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/websocket"
	"flux/workflow/nodes"
)

// ReplayTrace 一次任务执行的完整回放结果
type ReplayTrace struct {
	TaskID            int64             `json:"task_id"`
	WorkflowVersionID int64             `json:"workflow_version_id"`
	Status            domain.TaskStatus `json:"status"`
	StartedAt         time.Time         `json:"started_at"`
	FinishedAt        *time.Time        `json:"finished_at,omitempty"`
	Input             map[string]any    `json:"input"`
	Nodes             []NodeTraceFrame  `json:"nodes"`
}

// NodeTraceFrame 单个节点的回放帧
type NodeTraceFrame struct {
	Name             string           `json:"name"`
	Index            int              `json:"index"`
	BizIndex         int              `json:"biz_index"`
	State            domain.NodeState `json:"state"`
	StartedAt        *time.Time       `json:"started_at,omitempty"`
	FinishedAt       *time.Time       `json:"finished_at,omitempty"`
	DurationMs       *int64           `json:"duration_ms,omitempty"`
	ResolvedInput    map[string]any   `json:"resolved_input"`
	Output           map[string]any   `json:"output"`
	OutputPersisted  bool             `json:"output_persisted"`
	ActivatedEdges   map[string]bool  `json:"activated_edges"`
	ReuseKind        domain.ReuseKind `json:"reuse_kind,omitempty"`
	ReusedFromTaskID *int64           `json:"reused_from_task_id,omitempty"`
	Error            string           `json:"error,omitempty"`
	// Await 节点附加信息
	AwaitInfo *AwaitTraceInfo `json:"await_info,omitempty"`
	// Map/Loop 节点逐 item 结果
	MapItems []MapItemFrame `json:"map_items,omitempty"`
}

// AwaitTraceInfo await 节点的挂起/回调详情
type AwaitTraceInfo struct {
	AwaitType        domain.AwaitType          `json:"await_type"`
	Source           domain.AwaitSource        `json:"source"`
	Status           domain.AwaitBindingStatus `json:"status"`
	Provider         *string                   `json:"provider,omitempty"`
	ProviderTaskID   *string                   `json:"provider_task_id,omitempty"`
	ResultPayload    map[string]any            `json:"result_payload,omitempty"`
	ErrorMessage     string                    `json:"error_message,omitempty"`
	PollAttempts     int                       `json:"poll_attempts,omitempty"`
	WaitingStartedAt *time.Time                `json:"waiting_started_at,omitempty"`
	CompletedAt      *time.Time                `json:"completed_at,omitempty"`
}

// MapItemFrame map 节点逐 item 结果
type MapItemFrame struct {
	Index  int            `json:"index"`
	Output map[string]any `json:"output"`
	Reused bool           `json:"reused"`
}

// Replay 基于已持久化的执行状态，对指定任务做纯读回放，不调用任何工具或写入任何数据。
// 返回结构化 trace，包含每个节点的解析后入参、输出、分支决策。
func (e *Engine) Replay(ctx context.Context, taskID int64) (*ReplayTrace, error) {
	// 1. 加载任务
	task, err := e.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("replay: load task %d: %w", taskID, err)
	}

	// 只对终态任务回放
	switch task.Status {
	case domain.TaskSuccess, domain.TaskFailed, domain.TaskCanceled:
	default:
		return nil, fmt.Errorf("replay: task %d is in status %q, only terminal tasks can be replayed", taskID, task.Status)
	}

	// 2. 加载 workflow（使用原始版本，SHA-256 锁定）
	wf, _, err := e.loadWorkflowForTask(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("replay: load workflow for task %d: %w", taskID, err)
	}

	// 3. 加载所有节点运行时，按 index 排序
	savedNodes, err := e.nodeRepo.FindByTaskID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("replay: load node runtimes for task %d: %w", taskID, err)
	}
	sort.Slice(savedNodes, func(i, j int) bool {
		return savedNodes[i].Index < savedNodes[j].Index
	})

	// 加载 await bindings，按节点名建索引
	awaitBindings, err := e.awaitBindingRepo.ListByTaskID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("replay: load await bindings for task %d: %w", taskID, err)
	}
	awaitByNode := make(map[string]*domain.AwaitBinding, len(awaitBindings))
	for _, ab := range awaitBindings {
		awaitByNode[ab.NodeName] = ab
	}

	// 4. 构建只读 Context（nil EventBus 安全：buildNodeInput/EvalAny 不触达 EventBus）
	input := parseTaskInput(task.InputJSON)
	replayCtx := &nodes.Context{
		Workflow: wf.Source(),
		Ctx:      ctx,
		Task:     task,
		Input:    input,
		Output: map[string]any{
			"input": input,
			"nodes": map[string]any{},
		},
		Runtime: make(map[string]*domain.NodeRuntime),
	}

	// 5. 预填 Runtime map（buildNodeInput 通过 g.Parents 查 ctx.Runtime 时需要）
	for _, n := range savedNodes {
		replayCtx.Runtime[n.Name] = n
	}

	nodeDefs := wf.Nodes()
	dag := wf.Graph()

	var frames []NodeTraceFrame

	// 6. 按 index 顺序遍历，逐步注入输出、重算入参
	for _, runtime := range savedNodes {
		nodeDef, hasDef := nodeDefs[runtime.Name]

		// 确定节点输出
		output, outputPersisted := e.resolveReplayOutput(runtime, nodeDef, hasDef)

		// 注入到 ctx.Output["nodes"]，供后续节点的 buildNodeInput 使用
		if len(output) > 0 {
			nodesMap := replayCtx.Output["nodes"].(map[string]any)
			nodesMap[runtime.Name] = map[string]any{
				"output": output,
				"status": string(runtime.State),
			}
		}

		// 重算解析后入参（纯函数，不调任何外部接口）
		var resolvedInput map[string]any
		if hasDef {
			resolvedInput, _ = e.buildNodeInput(replayCtx, nodeDef, dag)
		}

		frame := NodeTraceFrame{
			Name:             runtime.Name,
			Index:            runtime.Index,
			BizIndex:         runtime.BizIndex,
			State:            runtime.State,
			StartedAt:        runtime.StartedAt,
			FinishedAt:       runtime.FinishedAt,
			ResolvedInput:    resolvedInput,
			Output:           output,
			OutputPersisted:  outputPersisted,
			ActivatedEdges:   runtime.ActivatedEdges,
			ReuseKind:        runtime.ReuseKind,
			ReusedFromTaskID: runtime.ReusedFromTaskID,
			Error:            runtime.Error,
		}

		if runtime.StartedAt != nil && runtime.FinishedAt != nil {
			ms := runtime.FinishedAt.Sub(*runtime.StartedAt).Milliseconds()
			frame.DurationMs = &ms
		}

		// Await 节点附加 binding 详情
		if ab, ok := awaitByNode[runtime.Name]; ok {
			frame.AwaitInfo = &AwaitTraceInfo{
				AwaitType:        ab.AwaitType,
				Source:           ab.Source,
				Status:           ab.Status,
				Provider:         ab.Provider,
				ProviderTaskID:   ab.ProviderTaskID,
				ResultPayload:    ab.ResultPayload,
				ErrorMessage:     ab.ErrorMessage,
				PollAttempts:     ab.PollAttempts,
				WaitingStartedAt: ab.WaitingStartedAt,
				CompletedAt:      ab.CompletedAt,
			}
		}

		// Map/Loop 节点展开逐 item 结果
		if hasDef && runtime.Checkpoint != nil {
			switch nodeDef.Type {
			case definition.NodeMap:
				frame.MapItems = extractMapItems(runtime)
			case definition.NodeLoop:
				frame.MapItems = extractLoopItems(runtime)
			}
		}

		frames = append(frames, frame)
	}

	trace := &ReplayTrace{
		TaskID:            taskID,
		WorkflowVersionID: task.WorkflowVersionID,
		Status:            task.Status,
		StartedAt:         task.StartedAt,
		Input:             input,
		Nodes:             frames,
	}
	if !task.UpdatedAt.IsZero() {
		t := task.UpdatedAt
		trace.FinishedAt = &t
	}

	return trace, nil
}

// resolveReplayOutput 确定节点的回放输出。
// 优先使用持久化的 output_json；若为空（persist_output:false 节点或 map/loop），
// 尝试从 checkpoint_json 重建；仍为空则标注为未持久化。
func (e *Engine) resolveReplayOutput(
	runtime *domain.NodeRuntime,
	nodeDef nodes.Node,
	hasDef bool,
) (output map[string]any, persisted bool) {
	if len(runtime.Output) > 0 {
		return deepCloneMap(runtime.Output), true
	}

	if !hasDef || runtime.Checkpoint == nil {
		return nil, false
	}

	// 尝试从 checkpoint 重建（map / loop 节点）
	switch nodeDef.Type {
	case definition.NodeMap:
		rebuilt, err := rebuildMapNodeOutput(runtime)
		if err == nil && len(rebuilt) > 0 {
			return rebuilt, true
		}
	case definition.NodeLoop:
		rebuilt, err := rebuildLoopNodeOutput(runtime)
		if err == nil && len(rebuilt) > 0 {
			return rebuilt, true
		}
	}

	return nil, false
}

// extractMapItems 从 checkpoint 中展开 map 节点的逐 item 结果
func extractMapItems(runtime *domain.NodeRuntime) []MapItemFrame {
	cp := runtime.Checkpoint
	if cp == nil {
		return nil
	}
	rawResults, _ := cp["results"].(map[string]any)
	rawReused, _ := cp["reused_items"].(map[string]any)
	if len(rawResults) == 0 {
		return nil
	}

	// 按 index 排序输出
	indexes := make([]int, 0, len(rawResults))
	for k := range rawResults {
		idx := 0
		fmt.Sscanf(k, "%d", &idx)
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	items := make([]MapItemFrame, 0, len(indexes))
	for _, idx := range indexes {
		key := fmt.Sprintf("%d", idx)
		out, _ := rawResults[key].(map[string]any)
		reused, _ := rawReused[key].(bool)
		items = append(items, MapItemFrame{
			Index:  idx,
			Output: out,
			Reused: reused,
		})
	}
	return items
}

// ReplayToWS 将已持久化的原始事件按 sequence 顺序重发到 task:{taskID} channel。
// 客户端订阅方式与真实任务完全一致，收到的事件类型也完全相同。
// speedMs 控制事件间推送延迟（毫秒），0 表示无延迟。
func (e *Engine) ReplayToWS(ctx context.Context, taskID int64, speedMs int) error {
	task, err := e.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("replay ws: load task %d: %w", taskID, err)
	}
	switch task.Status {
	case domain.TaskSuccess, domain.TaskFailed, domain.TaskCanceled:
	default:
		return fmt.Errorf("replay ws: task %d is in status %q, only terminal tasks can be replayed", taskID, task.Status)
	}

	events, err := e.eventRepo.FindPersistentByTaskID(ctx, taskID, 0, 0, false)
	if err != nil {
		return fmt.Errorf("replay ws: load events for task %d: %w", taskID, err)
	}

	ch := websocket.TaskChannel(taskID)
	delay := time.Duration(speedMs) * time.Millisecond

	for i := range events {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		e.eventBus.PublishToChannel(ch, &events[i])

		if delay > 0 && i < len(events)-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return nil
}

// extractLoopItems 从 checkpoint 中展开 loop 节点的逐迭代结果
func extractLoopItems(runtime *domain.NodeRuntime) []MapItemFrame {
	cp := runtime.Checkpoint
	if cp == nil {
		return nil
	}
	rawResults, _ := cp["results"].([]any)
	if len(rawResults) == 0 {
		return nil
	}

	items := make([]MapItemFrame, 0, len(rawResults))
	for i, v := range rawResults {
		out, _ := v.(map[string]any)
		items = append(items, MapItemFrame{
			Index:  i,
			Output: out,
		})
	}
	return items
}
