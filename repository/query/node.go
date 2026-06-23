package query

import (
	"context"
	"encoding/json"
	"time"

	"flux/domain"
	"flux/domain/entity"
	"flux/repository"
	"flux/utils"

	"gorm.io/gorm"
)

type nodeRuntimeRepository struct {
	db *gorm.DB
}

func NewNodeRuntimeRepository(db *gorm.DB) repository.NodeRuntimeRepository {
	return &nodeRuntimeRepository{db: db}
}

func (r *nodeRuntimeRepository) Create(ctx context.Context, n *domain.NodeRuntime) error {

	var nodeErr *string
	if n.Error != "" {
		nodeErr = &n.Error
	}
	outputJSON, _ := json.Marshal(n.Output)
	activatedEdgesJSON, _ := json.Marshal(n.ActivatedEdges)
	checkpointJSON, _ := json.Marshal(n.Checkpoint)

	var dirtyReason *string
	if n.DirtyReason != "" {
		dirtyReason = &n.DirtyReason
	}

	model := entity.TaskNodeModel{
		TaskID:             n.TaskID,
		NodeName:           n.Name,
		State:              string(n.State),
		StartedAt:          n.StartedAt,
		FinishedAt:         n.FinishedAt,
		LastHeartbeat:      n.LastHeartbeat,
		OutputJSON:         outputJSON,
		InputHash:          n.InputHash,
		CheckpointJSON:     checkpointJSON,
		Error:              nodeErr,
		Index:              n.Index,
		BizIndex:           n.BizIndex,
		Weight:             n.Weight,
		ActivatedEdgesJSON: activatedEdgesJSON,
		ReusedFromTaskID:   n.ReusedFromTaskID,
		ReusedFromNode:     n.ReusedFromNode,
		IsInjected:         n.IsInjected,
		IsDirty:            n.IsDirty,
		DirtyReason:        dirtyReason,
		CheckpointedAt:     n.CheckpointedAt,
		ReuseKind:          string(n.ReuseKind),
		OutputHash:         n.OutputHash,
	}

	if n.Error != "" {
		model.Error = &n.Error
	}

	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	n.ID = model.ID
	return nil
}

func (r *nodeRuntimeRepository) Update(ctx context.Context, n *domain.NodeRuntime) error {

	outputJSON, _ := json.Marshal(n.Output)
	checkpointJSON, _ := json.Marshal(n.Checkpoint)
	activatedEdgesJSON, _ := json.Marshal(n.ActivatedEdges)
	//if n.Name == "map_images" && (n.State == domain.NodeSuccessPendingEdges || n.State == domain.NodeSuccess) {
	//	fmt.Printf("map node name: %s status: %s | update outputJSON: %s\n", n.Name, n.State, n.Output)
	//}
	update := map[string]interface{}{
		"state":                string(n.State),
		"started_at":           n.StartedAt,
		"finished_at":          n.FinishedAt,
		"updated_at":           time.Now(),
		"last_heartbeat":       n.LastHeartbeat,
		"error":                n.Error,
		"output_json":          outputJSON,
		"input_hash":           n.InputHash,
		"output_hash":          n.OutputHash,
		"checkpoint_json":      checkpointJSON,
		"index":                n.Index,
		"weight":               n.Weight,
		"biz_index":            n.BizIndex,
		"activated_edges_json": activatedEdgesJSON,
		"reused_from_task_id":  n.ReusedFromTaskID,
		"reused_from_node":     n.ReusedFromNode,
		"is_injected":          n.IsInjected,
		"is_dirty":             n.IsDirty,
		"checkpointed_at":      n.CheckpointedAt,
		"reuse_kind":           string(n.ReuseKind),
	}

	if n.Error != "" {
		update["error"] = n.Error
	}

	if n.DirtyReason != "" {
		update["dirty_reason"] = n.DirtyReason
	} else {
		update["dirty_reason"] = nil
	}

	return r.db.WithContext(ctx).
		Model(&entity.TaskNodeModel{}).
		Where("task_id = ? AND node_name = ?", n.TaskID, n.Name).
		Updates(update).Error
}

func (r *nodeRuntimeRepository) FindByTaskID(ctx context.Context, taskID int64) ([]*domain.NodeRuntime, error) {

	var models []entity.TaskNodeModel

	err := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("index asc, id asc").
		Find(&models).Error

	if err != nil {
		return nil, err
	}

	var result []*domain.NodeRuntime
	for _, m := range models {
		var output map[string]any
		_ = json.Unmarshal(m.OutputJSON, &output)
		var activatedEdges map[string]bool
		_ = json.Unmarshal(m.ActivatedEdgesJSON, &activatedEdges)
		var ccheckpoint map[string]any
		_ = json.Unmarshal(m.CheckpointJSON, &ccheckpoint)

		var dirtyReason string
		if m.DirtyReason != nil {
			dirtyReason = *m.DirtyReason
		}

		result = append(result, &domain.NodeRuntime{
			ID:               m.ID,
			TaskID:           m.TaskID,
			Name:             m.NodeName,
			State:            domain.NodeState(m.State),
			StartedAt:        m.StartedAt,
			FinishedAt:       m.FinishedAt,
			LastHeartbeat:    m.LastHeartbeat,
			Output:           output,
			InputHash:        m.InputHash,
			Checkpoint:       ccheckpoint,
			Error:            utils.ValueOrEmpty(m.Error),
			Index:            m.Index,
			BizIndex:         m.BizIndex,
			Weight:           m.Weight,
			ActivatedEdges:   activatedEdges,
			ReusedFromTaskID: m.ReusedFromTaskID,
			ReusedFromNode:   m.ReusedFromNode,
			IsInjected:       m.IsInjected,
			IsDirty:          m.IsDirty,
			DirtyReason:      dirtyReason,
			CheckpointedAt:   m.CheckpointedAt,
			ReuseKind:        domain.ReuseKind(m.ReuseKind),
			OutputHash:       m.OutputHash,
		})
	}

	return result, nil
}

func (r *nodeRuntimeRepository) FindByTaskIDAndNode(ctx context.Context, taskID int64, node string) (*domain.NodeRuntime, error) {

	var m entity.TaskNodeModel

	err := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Where("node_name = ?", node).
		First(&m).Error

	if err != nil {
		return nil, err
	}
	var output map[string]any
	_ = json.Unmarshal(m.OutputJSON, &output)
	var activatedEdges map[string]bool
	_ = json.Unmarshal(m.ActivatedEdgesJSON, &activatedEdges)
	var ccheckpoint map[string]any
	_ = json.Unmarshal(m.CheckpointJSON, &ccheckpoint)

	var dirtyReason string
	if m.DirtyReason != nil {
		dirtyReason = *m.DirtyReason
	}

	return &domain.NodeRuntime{
		ID:               m.ID,
		TaskID:           m.TaskID,
		Name:             m.NodeName,
		State:            domain.NodeState(m.State),
		StartedAt:        m.StartedAt,
		FinishedAt:       m.FinishedAt,
		LastHeartbeat:    m.LastHeartbeat,
		Output:           output,
		InputHash:        m.InputHash,
		Checkpoint:       ccheckpoint,
		Error:            utils.ValueOrEmpty(m.Error),
		Index:            m.Index,
		BizIndex:         m.BizIndex,
		Weight:           m.Weight,
		ActivatedEdges:   activatedEdges,
		ReusedFromTaskID: m.ReusedFromTaskID,
		ReusedFromNode:   m.ReusedFromNode,
		IsInjected:       m.IsInjected,
		IsDirty:          m.IsDirty,
		DirtyReason:      dirtyReason,
		CheckpointedAt:   m.CheckpointedAt,
		ReuseKind:        domain.ReuseKind(m.ReuseKind),
		OutputHash:       m.OutputHash,
	}, nil
}

func (r *nodeRuntimeRepository) MarkRunningAsRetrying(ctx context.Context, taskID int64) error {
	update := map[string]interface{}{
		"state":      string(domain.NodeRetrying),
		"updated_at": time.Now(),
	}
	err := r.db.WithContext(ctx).
		Model(&entity.TaskNodeModel{}).
		Where("task_id = ?", taskID).
		Where("state = ?", domain.NodeRunning).
		Updates(update).Error
	return err
}

func (r *nodeRuntimeRepository) MarkAsRetrying(ctx context.Context, taskID int64, name string) error {
	update := map[string]interface{}{
		"state":      string(domain.NodeRetrying),
		"updated_at": time.Now(),
	}
	err := r.db.WithContext(ctx).
		Model(&entity.TaskNodeModel{}).
		Where("task_id = ?", taskID).
		Where("node_name = ?", name).
		Updates(update).Error
	return err
}

func (r *nodeRuntimeRepository) MarkFailed(ctx context.Context, taskID int64, name string, errMessage string) error {
	update := map[string]interface{}{
		"state":      string(domain.NodeFailed),
		"updated_at": time.Now(),
		"error":      errMessage,
	}
	err := r.db.WithContext(ctx).
		Model(&entity.TaskNodeModel{}).
		Where("task_id = ?", taskID).
		Where("node_name = ?", name).
		Updates(update).Error
	return err
}

// FindExpiredRunningNodes 查找可能超时的节点
func (r *nodeRuntimeRepository) FindExpiredRunningNodes(
	ctx context.Context,
	expire time.Time,
) ([]*domain.NodeRuntime, error) {

	var models []entity.TaskNodeModel

	// 只恢复24小时内任务
	recent := time.Now().Add(-24 * time.Hour)

	err := r.db.WithContext(ctx).
		Where("state IN ?", []string{
			string(domain.NodeRunning),
			string(domain.NodeRetrying),
			string(domain.NodeSuccessPendingEdges),
			string(domain.NodeFailedPendingEdges),
		}).
		Where("(last_heartbeat IS NOT NULL AND last_heartbeat < ?) OR (last_heartbeat IS NULL AND started_at < ?)", expire, expire).
		Where("started_at > ?", recent).
		Limit(100). // 防止一次恢复过多
		Find(&models).Error

	if err != nil {
		return nil, err
	}

	var result []*domain.NodeRuntime

	for _, m := range models {
		var output map[string]any
		_ = json.Unmarshal(m.OutputJSON, &output)
		var activatedEdges map[string]bool
		_ = json.Unmarshal(m.ActivatedEdgesJSON, &activatedEdges)
		var ccheckpoint map[string]any
		_ = json.Unmarshal(m.CheckpointJSON, &ccheckpoint)

		var dirtyReason string
		if m.DirtyReason != nil {
			dirtyReason = *m.DirtyReason
		}

		result = append(result, &domain.NodeRuntime{
			ID:               m.ID,
			TaskID:           m.TaskID,
			Name:             m.NodeName,
			State:            domain.NodeState(m.State),
			StartedAt:        m.StartedAt,
			FinishedAt:       m.FinishedAt,
			LastHeartbeat:    m.LastHeartbeat,
			Output:           output,
			InputHash:        m.InputHash,
			Checkpoint:       ccheckpoint,
			Error:            utils.ValueOrEmpty(m.Error),
			Index:            m.Index,
			BizIndex:         m.BizIndex,
			Weight:           m.Weight,
			ActivatedEdges:   activatedEdges,
			ReusedFromTaskID: m.ReusedFromTaskID,
			ReusedFromNode:   m.ReusedFromNode,
			IsInjected:       m.IsInjected,
			IsDirty:          m.IsDirty,
			DirtyReason:      dirtyReason,
			CheckpointedAt:   m.CheckpointedAt,
			ReuseKind:        domain.ReuseKind(m.ReuseKind),
			OutputHash:       m.OutputHash,
		})
	}
	return result, nil
}

// AttemptCompletePendingEdges 尝试将节点标记为完成
// 返回 (bool, error): bool 为 true 表示抢占成功，有权执行后续 Resume 逻辑。
func (r *nodeRuntimeRepository) AttemptCompletePendingEdges(ctx context.Context, taskID int64, nodeName string, output map[string]any, errMsg string) (bool, error) {

	updateData := map[string]interface{}{
		"updated_at":  time.Now(),
		"finished_at": time.Now(),
	}
	// 只有 output != nil 才覆盖 output_json
	if output != nil {
		outputJSON, _ := json.Marshal(output)
		updateData["output_json"] = outputJSON
	}

	stateWhere := []string{string(domain.NodeRunning), string(domain.NodeRetrying), string(domain.NodeAwaiting)}
	targetState := string(domain.NodeSuccessPendingEdges)

	if errMsg != "" {
		updateData["error"] = errMsg
		targetState = string(domain.NodeFailedPendingEdges)
	}
	updateData["state"] = targetState

	// 关键：增加状态过滤，只有处于运行中或重试中的节点才能被标记为完成
	// 这样如果另一个线程已经把它改成了 SuccessPending，RowsAffected 就会是 0
	result := r.db.WithContext(ctx).
		Model(&entity.TaskNodeModel{}).
		Where("task_id = ? AND node_name = ? AND state IN ?", taskID, nodeName, stateWhere).
		Updates(updateData)

	if result.Error != nil {
		return false, result.Error
	}

	return result.RowsAffected > 0, nil
}

func (r *nodeRuntimeRepository) CloneCheckpoint(ctx context.Context, fromTaskID, toTaskID int64) error {
	nodes, err := r.FindByTaskID(ctx, fromTaskID)
	if err != nil {
		return err
	}

	now := time.Now()

	for _, n := range nodes {
		outputJSON, _ := json.Marshal(n.Output)
		activatedEdgesJSON, _ := json.Marshal(n.ActivatedEdges)
		checkpointJSON, _ := json.Marshal(n.Checkpoint)

		model := entity.TaskNodeModel{
			TaskID:             toTaskID,
			NodeName:           n.Name,
			State:              string(domain.NodePending), // 新 run 默认 pending
			StartedAt:          nil,
			FinishedAt:         nil,
			Error:              nil,
			LastHeartbeat:      nil,
			OutputJSON:         outputJSON,
			InputHash:          n.InputHash,
			OutputHash:         n.OutputHash,
			CheckpointJSON:     checkpointJSON,
			Index:              n.Index,
			BizIndex:           n.BizIndex,
			Weight:             n.Weight,
			ActivatedEdgesJSON: activatedEdgesJSON,
			IsInjected:         false,
			IsDirty:            false,
			DirtyReason:        nil,
			CheckpointedAt:     &now,
			ReusedFromTaskID:   &fromTaskID,
			ReuseKind:          string(n.ReuseKind),
		}

		reusedNode := n.Name
		model.ReusedFromNode = &reusedNode

		if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
			return err
		}
	}

	return nil
}
