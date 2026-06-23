package engine

import (
	"context"
	"encoding/json"
	"flux/definition"
	"flux/domain"
	"flux/runtimekeys"
	"flux/utils"
	"flux/workflow/nodes"
	"fmt"
	"strings"
)

type ParentFanoutNodeKind string

const (
	ParentFanoutNodeNone ParentFanoutNodeKind = ""
	ParentFanoutNodeMap  ParentFanoutNodeKind = "map"
	ParentFanoutNodeLoop ParentFanoutNodeKind = "loop"
)

func (e *Engine) RunSubWorkflow(
	execCtx *nodes.NodeExecContext,
	workflowName string,
	input map[string]any,
) (map[string]any, error) {

	runCtx := execCtx.TaskContext

	if runCtx.Depth > 10 {
		return nil, fmt.Errorf("subworkflow recursion depth exceeded")
	}

	ctx := runCtx.Ctx

	// 1️⃣ 获取 workflow
	dbWorkflow, err := e.WorkflowRepo.GetByName(ctx, workflowName)
	if err != nil {
		return nil, err
	}

	version, err := e.WorkflowVersionRepo.GetLatestByWorkflowID(ctx, dbWorkflow.ID)
	if err != nil {
		return nil, err
	}

	// 2️⃣ 构建 SubKey（幂等）
	subKey := runtimekeys.BuildSubWorkflowKey(
		runCtx.Task.ID,
		execCtx.NodeDef.Name,
		workflowName,
		input,
	)

	// 3️⃣ 查询是否存在
	existing, _ := e.taskRepo.FindBySubKey(ctx, subKey)

	if existing != nil {

		switch existing.Status {

		case domain.TaskSuccess:
			// 分两种情况：map node 任务 挂起
			if execCtx.NodeDef.Type == definition.NodeMap {
				// ❗统一：直接挂起，禁止直接返回结果，否则影响fan in
				return nil, &domain.WorkflowSuspendedError{
					Reason: domain.SuspendSubWorkflow,
				}
			}
			emitPlainSubWorkflowFanoutProgress(execCtx, 1, 0, 0)
			// workflow node 任务，直接返回结果
			return utils.ParseFinal(existing.OutputJSON)
		case domain.TaskPending:
			err = e.taskRepo.Enqueue(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			emitPlainSubWorkflowFanoutProgress(execCtx, 0, 1, 0)
			// 已入队，挂起等待子任务完成
			return nil, &domain.WorkflowSuspendedError{
				Reason: domain.SuspendSubWorkflow,
			}
		case domain.TaskFailed:
			existing.RetryCount++
			if existing.RetryCount > domain.MaxAutoRetryCount {
				return nil, fmt.Errorf("sub-workflow permanently failed: task=%d, retries exhausted (retry_count=%d)", existing.ID, existing.RetryCount-1)
			}
			existing.Status = domain.TaskPending
			_ = e.taskRepo.Update(ctx, existing)
			err = e.taskRepo.Enqueue(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			emitPlainSubWorkflowFanoutProgress(execCtx, 0, 1, 0)
			// ❗统一：直接挂起，禁止直接返回结果，否则影响fan in
			return nil, &domain.WorkflowSuspendedError{
				Reason: domain.SuspendSubWorkflow,
			}
		case domain.TaskRunning,
			domain.TaskSuspended:
			emitPlainSubWorkflowFanoutProgress(execCtx, 0, 1, 0)
			return nil, &domain.WorkflowSuspendedError{
				Reason: domain.SuspendSubWorkflow,
			}
		case domain.TaskCanceled:
			// 子任务被取消，绝大多数来自父任务恢复重试时 cancelChildTasksForRetry 的误伤
			// （recovery_scanner 把“正常等子任务”的 subworkflow 父节点误判为 crash 所致）。
			// 必须原地复用同一行复活，绝不能用相同 sub_key 重新 INSERT —— 否则会撞
			// idx_tasks_sub_key_not_null 唯一约束，且父任务会永久挂在一个已死的子任务上。
			existing.RetryCount++
			if existing.RetryCount > domain.MaxAutoRetryCount {
				return nil, fmt.Errorf("sub-workflow permanently canceled: task=%d, retries exhausted (retry_count=%d)", existing.ID, existing.RetryCount-1)
			}
			existing.Status = domain.TaskPending
			existing.ErrorMessage = ""
			if err = e.taskRepo.Update(ctx, existing); err != nil {
				return nil, err
			}
			if err = e.taskRepo.Enqueue(ctx, existing.ID); err != nil {
				return nil, err
			}
			emitPlainSubWorkflowFanoutProgress(execCtx, 0, 1, 0)
			// ❗统一：直接挂起，禁止直接返回结果，否则影响fan in
			return nil, &domain.WorkflowSuspendedError{
				Reason: domain.SuspendSubWorkflow,
			}
		}
	}

	// 4️⃣ 创建子任务（真正的 Activity）
	taskID := e.iSrv.GenSnowID()
	inputJSON, _ := json.Marshal(input)

	task := domain.Task{
		ID:                   taskID,
		UserID:               runCtx.Task.UserID,
		ParentID:             &runCtx.Task.ID,
		RootID:               runCtx.Task.RootID,
		Type:                 runCtx.Task.Type,
		WorkflowVersionID:    version.ID,
		WorkflowDefinitionID: version.WorkflowID,
		Status:               domain.TaskPending, // ❗重要：不是 running
		InputJSON:            inputJSON,
		SubKey:               &subKey,
		ParentNode:           &execCtx.NodeDef.Name,
	}
	idxVal, ok := input["index"].(int)
	if ok && (execCtx.NodeDef.Type == definition.NodeMap || execCtx.NodeDef.Type == definition.NodeLoop) {
		task.MapIndex = &idxVal
	}
	err = e.taskRepo.Create(ctx, &task)
	if err != nil {

		// 并发冲突
		existing, err2 := e.taskRepo.FindBySubKey(ctx, subKey)
		if err2 == nil && existing != nil {
			return nil, &domain.WorkflowSuspendedError{
				Reason: domain.SuspendSubWorkflow,
			}
		}

		return nil, err
	}

	// 入队
	err = e.taskRepo.Enqueue(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	emitPlainSubWorkflowFanoutProgress(execCtx, 0, 1, 0)
	// ❗关键：挂起父 workflow
	return nil, &domain.WorkflowSuspendedError{
		Reason: domain.SuspendSubWorkflow,
	}
}

func emitPlainSubWorkflowFanoutProgress(execCtx *nodes.NodeExecContext, done, running, failed int) {
	if execCtx == nil || execCtx.NodeDef == nil || execCtx.NodeDef.Type != definition.NodeSubWorkflow {
		return
	}
	progress := 0.0
	if done >= 1 {
		progress = 1
	}
	nodes.EmitFanoutProgress(execCtx, nodes.FanoutProgress{
		Kind:     nodes.FanoutKindSubWorkflow,
		Total:    1,
		Done:     done,
		Running:  running,
		Failed:   failed,
		Progress: progress,
	})
}

// classifyParentFanoutNode
// 用 checkpoint 结构判断当前父节点是普通 subworkflow / map / loop。
// 这是 listener 层做策略分流的唯一入口，避免散落在各处写 if checkpoint["xxx"]。
func (e *Engine) classifyParentFanoutNode(parentRuntime *domain.NodeRuntime) ParentFanoutNodeKind {
	if parentRuntime == nil || parentRuntime.Checkpoint == nil {
		return ParentFanoutNodeNone
	}

	cp := parentRuntime.Checkpoint

	// 1. 显式字段优先
	if raw, ok := cp[nodes.CPFanoutKind()]; ok {
		switch strings.TrimSpace(asString(raw)) {
		case string(ParentFanoutNodeLoop):
			return ParentFanoutNodeLoop
		case string(ParentFanoutNodeMap):
			return ParentFanoutNodeMap
		}
	}

	// 2. fallback：兼容旧 checkpoint
	_, hasRunningIndex := cp["running_index"]
	_, hasCurrentIndex := cp["current_index"]
	_, hasCarryState := cp["carry_state"]
	_, hasRunningSubKey := cp["running_sub_key"]
	if hasRunningIndex || hasCurrentIndex || hasCarryState || hasRunningSubKey {
		return ParentFanoutNodeLoop
	}

	_, hasItemHashes := cp["item_hashes"]
	_, hasReusedItems := cp["reused_items"]
	if hasItemHashes || hasReusedItems {
		return ParentFanoutNodeMap
	}

	return ParentFanoutNodeNone
}

type childEventContext struct {
	child         *domain.Task
	parentID      int64
	parentNode    string
	parentRuntime *domain.NodeRuntime
}

// loadChildEventContext
// 从 child task 中解析 parentID / parentNode / parentRuntime。
func (e *Engine) loadChildEventContext(taskID int64) (*childEventContext, error) {
	child, err := e.taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		return nil, err
	}
	if child == nil || child.ParentID == nil {
		return nil, nil
	}

	parentNode := ""
	if child.ParentNode != nil {
		parentNode = *child.ParentNode
	}
	if parentNode == "" {
		return nil, nil
	}

	parentRuntime, err := e.nodeRepo.FindByTaskIDAndNode(context.Background(), *child.ParentID, parentNode)
	if err != nil {
		return nil, err
	}
	if parentRuntime == nil {
		return nil, nil
	}

	return &childEventContext{
		child:         child,
		parentID:      *child.ParentID,
		parentNode:    parentNode,
		parentRuntime: parentRuntime,
	}, nil
}
