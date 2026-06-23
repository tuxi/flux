package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flux/domain"
	"flux/domain/entity"
	"flux/dto"
	"flux/repository"
	"flux/utils"

	"gorm.io/gorm"
)

type taskRepository struct {
	db    *gorm.DB
	queue repository.TaskQueue
}

func NewTaskRepository(db *gorm.DB, queue repository.TaskQueue) repository.TaskRepository {
	return &taskRepository{db: db, queue: queue}
}

func (r *taskRepository) Create(ctx context.Context, task *domain.Task) error {

	model := entity.TaskModel{
		ID:                   task.ID,
		RootID:               task.RootID,
		Type:                 task.Type,
		UserID:               task.UserID,
		Status:               string(task.Status),
		RetryCount:           task.RetryCount,
		InputJSON:            task.InputJSON,
		ParentID:             task.ParentID,
		WorkflowVersionID:    task.WorkflowVersionID,
		WorkflowDefinitionID: task.WorkflowDefinitionID,
		SubKey:               task.SubKey,
		ParentNode:           task.ParentNode,
		WorkerID:             task.WorkerID,
		StartedAt:            task.StartedAt,
		MapIndex:             task.MapIndex,
		Progress:             task.Progress,

		BaseRunID:  task.BaseRunID,
		ForkedFrom: task.ForkedFrom,
		RunDepth:   task.RunDepth,
		EditAction: task.EditAction,
		EditLabel:  task.EditLabel,
		ResumeFrom: task.ResumeFrom,
		PatchJSON:  task.PatchJSON,

		RouteKey:           task.RouteKey,
		ModeKey:            task.ModeKey,
		EntryType:          task.EntryType,
		EntryTitle:         task.EntryTitle,
		EntrySubtitle:      task.EntrySubtitle,
		ToolDefinitionID:   task.ToolDefinitionID,
		ToolModeID:         task.ToolModeID,
		ToolModeVersionID:  task.ToolModeVersionID,
		TemplateID:         task.TemplateID,
		TemplateVersionID:  task.TemplateVersionID,
		EstimatedCostTotal: task.EstimatedCostTotal,
		ActualCostTotal:    task.ActualCostTotal,
		CostStatus:         task.CostStatus,
	}

	err := r.db.WithContext(ctx).Create(&model).Error
	if err != nil {
		return err
	}

	task.ID = model.ID
	return nil
}

func (r *taskRepository) GetByID(ctx context.Context, id int64) (*domain.Task, error) {

	var model entity.TaskModel
	err := r.db.WithContext(ctx).First(&model, id).Error
	if err != nil {
		return nil, err
	}

	return &domain.Task{
		ID:                   model.ID,
		UserID:               model.UserID,
		Type:                 model.Type,
		Status:               domain.TaskStatus(model.Status),
		RetryCount:           model.RetryCount,
		InputJSON:            model.InputJSON,
		OutputJSON:           model.OutputJSON,
		ParentID:             model.ParentID,
		WorkflowVersionID:    model.WorkflowVersionID,
		WorkflowDefinitionID: model.WorkflowDefinitionID,
		RootID:               model.RootID,
		SubKey:               model.SubKey,
		ParentNode:           model.ParentNode,
		MapIndex:             model.MapIndex,
		Progress:             model.Progress,

		BaseRunID:  model.BaseRunID,
		ForkedFrom: model.ForkedFrom,
		RunDepth:   model.RunDepth,
		EditAction: model.EditAction,
		EditLabel:  model.EditLabel,
		ResumeFrom: model.ResumeFrom,
		PatchJSON:  model.PatchJSON,

		RouteKey:           model.RouteKey,
		ModeKey:            model.ModeKey,
		EntryType:          model.EntryType,
		EntryTitle:         model.EntryTitle,
		EntrySubtitle:      model.EntrySubtitle,
		ToolDefinitionID:   model.ToolDefinitionID,
		ToolModeID:         model.ToolModeID,
		ToolModeVersionID:  model.ToolModeVersionID,
		TemplateID:         model.TemplateID,
		TemplateVersionID:  model.TemplateVersionID,
		EstimatedCostTotal: model.EstimatedCostTotal,
		ActualCostTotal:    model.ActualCostTotal,
		CostStatus:         model.CostStatus,

		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}, nil
}

func (r *taskRepository) Update(ctx context.Context, task *domain.Task) error {

	err := r.db.WithContext(ctx).
		Model(&entity.TaskModel{}).
		Where("id = ?", task.ID).
		Updates(map[string]interface{}{
			"status":                 task.Status,
			"retry_count":            task.RetryCount,
			"updated_at":             time.Now(),
			"input_json":             task.InputJSON,
			"output_json":            task.OutputJSON,
			"parent_id":              task.ParentID,
			"error_message":          task.ErrorMessage,
			"workflow_version_id":    task.WorkflowVersionID,
			"workflow_definition_id": task.WorkflowDefinitionID,
			"root_id":                task.RootID,
			"sub_key":                task.SubKey,
			"parent_node":            task.ParentNode,
			"map_index":              task.MapIndex,
			"progress":               task.Progress,

			"base_run_id": task.BaseRunID,
			"forked_from": task.ForkedFrom,
			"run_depth":   task.RunDepth,
			"edit_action": task.EditAction,
			"edit_label":  task.EditLabel,
			"resume_from": task.ResumeFrom,
			"patch_json":  task.PatchJSON,

			"route_key":            task.RouteKey,
			"mode_key":             task.ModeKey,
			"entry_type":           task.EntryType,
			"entry_title":          task.EntryTitle,
			"entry_subtitle":       task.EntrySubtitle,
			"tool_definition_id":   task.ToolDefinitionID,
			"tool_mode_id":         task.ToolModeID,
			"tool_mode_version_id": task.ToolModeVersionID,
			"template_id":          task.TemplateID,
			"template_version_id":  task.TemplateVersionID,
			"estimated_cost_total": task.EstimatedCostTotal,
			"actual_cost_total":    task.ActualCostTotal,
			"cost_status":          task.CostStatus,
		}).Error
	return err
}

func (r *taskRepository) ListByParent(ctx context.Context, parentID int64) ([]*domain.Task, error) {

	var models []entity.TaskModel
	err := r.db.WithContext(ctx).
		Where("parent_id = ?", parentID).
		Find(&models).Error

	if err != nil {
		return nil, err
	}

	var result []*domain.Task
	for _, m := range models {
		result = append(result, &domain.Task{
			ID:                   m.ID,
			UserID:               m.UserID,
			Type:                 m.Type,
			Status:               domain.TaskStatus(m.Status),
			RetryCount:           m.RetryCount,
			InputJSON:            m.InputJSON,
			OutputJSON:           m.OutputJSON,
			ErrorMessage:         utils.ValueOrEmpty(m.ErrorMessage),
			ParentID:             m.ParentID,
			WorkflowVersionID:    m.WorkflowVersionID,
			WorkflowDefinitionID: m.WorkflowDefinitionID,
			SubKey:               m.SubKey,
			ParentNode:           m.ParentNode,
			MapIndex:             m.MapIndex,
			Progress:             m.Progress,

			BaseRunID:  m.BaseRunID,
			ForkedFrom: m.ForkedFrom,
			RunDepth:   m.RunDepth,
			EditAction: m.EditAction,
			EditLabel:  m.EditLabel,
			ResumeFrom: m.ResumeFrom,
			PatchJSON:  m.PatchJSON,

			RouteKey:           m.RouteKey,
			ModeKey:            m.ModeKey,
			EntryType:          m.EntryType,
			EntryTitle:         m.EntryTitle,
			EntrySubtitle:      m.EntrySubtitle,
			ToolDefinitionID:   m.ToolDefinitionID,
			ToolModeID:         m.ToolModeID,
			ToolModeVersionID:  m.ToolModeVersionID,
			TemplateID:         m.TemplateID,
			TemplateVersionID:  m.TemplateVersionID,
			EstimatedCostTotal: m.EstimatedCostTotal,
			ActualCostTotal:    m.ActualCostTotal,
			CostStatus:         m.CostStatus,

			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}

	return result, nil
}

func (r *taskRepository) ListByRoot(ctx context.Context, rootID int64) ([]*domain.Task, error) {

	var models []entity.TaskModel
	err := r.db.WithContext(ctx).
		Where("root_id = ?", rootID).
		Find(&models).Error

	if err != nil {
		return nil, err
	}

	var result []*domain.Task
	for _, m := range models {
		result = append(result, &domain.Task{
			ID:                   m.ID,
			UserID:               m.UserID,
			RootID:               m.RootID,
			Type:                 m.Type,
			Status:               domain.TaskStatus(m.Status),
			RetryCount:           m.RetryCount,
			InputJSON:            m.InputJSON,
			OutputJSON:           m.OutputJSON,
			ErrorMessage:         utils.ValueOrEmpty(m.ErrorMessage),
			ParentID:             m.ParentID,
			WorkflowVersionID:    m.WorkflowVersionID,
			WorkflowDefinitionID: m.WorkflowDefinitionID,
			SubKey:               m.SubKey,
			ParentNode:           m.ParentNode,
			MapIndex:             m.MapIndex,
			Progress:             m.Progress,

			BaseRunID:  m.BaseRunID,
			ForkedFrom: m.ForkedFrom,
			RunDepth:   m.RunDepth,
			EditAction: m.EditAction,
			EditLabel:  m.EditLabel,
			ResumeFrom: m.ResumeFrom,
			PatchJSON:  m.PatchJSON,

			RouteKey:           m.RouteKey,
			ModeKey:            m.ModeKey,
			EntryType:          m.EntryType,
			EntryTitle:         m.EntryTitle,
			EntrySubtitle:      m.EntrySubtitle,
			ToolDefinitionID:   m.ToolDefinitionID,
			ToolModeID:         m.ToolModeID,
			ToolModeVersionID:  m.ToolModeVersionID,
			TemplateID:         m.TemplateID,
			TemplateVersionID:  m.TemplateVersionID,
			EstimatedCostTotal: m.EstimatedCostTotal,
			ActualCostTotal:    m.ActualCostTotal,
			CostStatus:         m.CostStatus,

			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}

	return result, nil
}

func (r *taskRepository) FindRunningRootTasks(ctx context.Context, before time.Time) ([]*domain.Task, error) {
	var models []entity.TaskModel
	err := r.db.WithContext(ctx).
		Where("updated_at < ?", before).
		Where("status = ?", domain.TaskRunning).
		Where("root_id IS NULL").
		Find(&models).
		Error
	if err != nil {
		return nil, err
	}
	var result []*domain.Task
	for _, m := range models {
		result = append(result, &domain.Task{
			ID:                   m.ID,
			UserID:               m.UserID,
			Type:                 m.Type,
			Status:               domain.TaskStatus(m.Status),
			RetryCount:           m.RetryCount,
			InputJSON:            m.InputJSON,
			OutputJSON:           m.OutputJSON,
			ErrorMessage:         utils.ValueOrEmpty(m.ErrorMessage),
			WorkflowVersionID:    m.WorkflowVersionID,
			WorkflowDefinitionID: m.WorkflowDefinitionID,
			ParentID:             m.ParentID,
			StartedAt:            m.StartedAt,
			WorkerID:             m.WorkerID,
			RootID:               m.RootID,
			SubKey:               m.SubKey,
			ParentNode:           m.ParentNode,
			MapIndex:             m.MapIndex,
			Progress:             m.Progress,

			BaseRunID:  m.BaseRunID,
			ForkedFrom: m.ForkedFrom,
			RunDepth:   m.RunDepth,
			EditAction: m.EditAction,
			EditLabel:  m.EditLabel,
			ResumeFrom: m.ResumeFrom,
			PatchJSON:  m.PatchJSON,

			RouteKey:          m.RouteKey,
			ModeKey:           m.ModeKey,
			EntryType:         m.EntryType,
			EntryTitle:        m.EntryTitle,
			EntrySubtitle:     m.EntrySubtitle,
			ToolDefinitionID:  m.ToolDefinitionID,
			ToolModeID:        m.ToolModeID,
			ToolModeVersionID: m.ToolModeVersionID,
			TemplateID:        m.TemplateID,
			TemplateVersionID: m.TemplateVersionID,

			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return result, nil
}

func (r *taskRepository) FindByWorkflowID(ctx context.Context, workflowID int64) ([]*domain.Task, error) {
	var models []entity.TaskModel
	err := r.db.WithContext(ctx).
		Where("workflow_definition_id = ?", workflowID).
		Where("status = ?", domain.TaskRunning).
		Find(&models).
		Error
	if err != nil {
		return nil, err
	}
	var result []*domain.Task
	for _, m := range models {
		result = append(result, &domain.Task{
			ID:                   m.ID,
			UserID:               m.UserID,
			Type:                 m.Type,
			Status:               domain.TaskStatus(m.Status),
			RetryCount:           m.RetryCount,
			InputJSON:            m.InputJSON,
			OutputJSON:           m.OutputJSON,
			ErrorMessage:         utils.ValueOrEmpty(m.ErrorMessage),
			WorkflowVersionID:    m.WorkflowVersionID,
			WorkflowDefinitionID: m.WorkflowDefinitionID,
			ParentID:             m.ParentID,
			WorkerID:             m.WorkerID,
			StartedAt:            m.StartedAt,
			RootID:               m.RootID,
			SubKey:               m.SubKey,
			ParentNode:           m.ParentNode,
			MapIndex:             m.MapIndex,
			Progress:             m.Progress,

			BaseRunID:  m.BaseRunID,
			ForkedFrom: m.ForkedFrom,
			RunDepth:   m.RunDepth,
			EditAction: m.EditAction,
			EditLabel:  m.EditLabel,
			ResumeFrom: m.ResumeFrom,
			PatchJSON:  m.PatchJSON,

			RouteKey:          m.RouteKey,
			ModeKey:           m.ModeKey,
			EntryType:         m.EntryType,
			EntryTitle:        m.EntryTitle,
			EntrySubtitle:     m.EntrySubtitle,
			ToolDefinitionID:  m.ToolDefinitionID,
			ToolModeID:        m.ToolModeID,
			ToolModeVersionID: m.ToolModeVersionID,
			TemplateID:        m.TemplateID,
			TemplateVersionID: m.TemplateVersionID,

			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return result, nil
}

func (r *taskRepository) ListByUser(
	ctx context.Context,
	userID int64,
	params dto.PageRequest, // 使用结构体传递分页参数
) ([]*domain.Task, int64, error) {

	var models []entity.TaskModel
	var total int64

	// 1. 构建基础查询（只查根任务 ParentID IS NULL）
	query := r.db.WithContext(ctx).
		Model(&entity.TaskModel{}).
		Where("user_id = ?", userID).
		Where("parent_id IS NULL")

	// 2. 获取总数（分页前先计算符合条件的记录总数）
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 3. 处理排序逻辑
	sortColumn := "id" // 默认排序字段
	if params.Sort != "" {
		sortColumn = params.Sort
	}
	sortOrder := "desc" // 默认倒序
	if params.Order == "asc" {
		sortOrder = "asc"
	}
	orderStr := fmt.Sprintf("%s %s", sortColumn, sortOrder)

	// 4. 执行分页查询
	err := query.Order(orderStr).
		Limit(params.GetLimit()).
		Offset(params.Offset()).
		Find(&models).Error

	if err != nil {
		return nil, 0, err
	}

	// 5. 转换为 Domain 模型
	var result []*domain.Task

	for _, m := range models {
		result = append(result, &domain.Task{
			ID:                   m.ID,
			UserID:               m.UserID,
			Type:                 m.Type,
			Status:               domain.TaskStatus(m.Status),
			InputJSON:            m.InputJSON,
			OutputJSON:           m.OutputJSON,
			WorkflowVersionID:    m.WorkflowVersionID,
			WorkflowDefinitionID: m.WorkflowDefinitionID,
			StartedAt:            m.StartedAt,
			WorkerID:             m.WorkerID,
			RootID:               m.RootID,
			SubKey:               m.SubKey,
			ParentNode:           m.ParentNode,
			MapIndex:             m.MapIndex,
			Progress:             m.Progress,

			BaseRunID:  m.BaseRunID,
			ForkedFrom: m.ForkedFrom,
			RunDepth:   m.RunDepth,
			EditAction: m.EditAction,
			EditLabel:  m.EditLabel,
			ResumeFrom: m.ResumeFrom,
			PatchJSON:  m.PatchJSON,

			RouteKey:          m.RouteKey,
			ModeKey:           m.ModeKey,
			EntryType:         m.EntryType,
			EntryTitle:        m.EntryTitle,
			EntrySubtitle:     m.EntrySubtitle,
			ToolDefinitionID:  m.ToolDefinitionID,
			ToolModeID:        m.ToolModeID,
			ToolModeVersionID: m.ToolModeVersionID,
			TemplateID:        m.TemplateID,
			TemplateVersionID: m.TemplateVersionID,

			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}

	return result, total, nil
}

// TryClaimTask 以原子化方式尝试抢占一个待处理任务，确保并发环境下只有一个工作节点能成功接管该任务，从而避免任务重复执行‌。
// 这是一种典型的“任务锁”或“工作抢占”模式，广泛应用于分布式任务队列、微服务调度系统中，保障任务处理的唯一性与一致性。
func (r *taskRepository) TryClaimTask(
	ctx context.Context,
	taskID int64,
	workerID string,
) (bool, error) {

	now := time.Now()
	// 运行超时任务允许重新抢占
	leaseExpire := now.Add(-10 * time.Minute)

	result := r.db.WithContext(ctx).
		Model(&entity.TaskModel{}).
		Where(
			// 防止重复 Claim Running 任务到一种情况:
			//worker crash
			//task status = running
			//queue recovery
			//重新投递
			// 此时task status = running
			// Worker 就 永远抢不到任务。
			// 解决方案：允许 running 但超时任务重新 claim。
			"id = ? AND (status = ? OR (status = ? AND started_at < ?))",
			taskID,
			domain.TaskPending,
			domain.TaskRunning,
			leaseExpire,
		).
		Updates(map[string]interface{}{
			"status":     domain.TaskRunning,
			"worker_id":  workerID,
			"started_at": now,
			"updated_at": now,
		})

	if result.Error != nil {
		return false, result.Error
	}
	// rows == 1 说明抢占成功
	rows := result.RowsAffected
	return rows == 1, nil
}

func (r *taskRepository) FindBySubKey(
	ctx context.Context,
	subKey string,
) (*domain.Task, error) {

	var model entity.TaskModel
	err := r.db.WithContext(ctx).
		Where("sub_key = ?", subKey).
		First(&model).Error

	if err != nil {
		return nil, err
	}

	return &domain.Task{
		ID:                   model.ID,
		UserID:               model.UserID,
		Status:               domain.TaskStatus(model.Status),
		InputJSON:            model.InputJSON,
		OutputJSON:           model.OutputJSON,
		ParentID:             model.ParentID,
		RootID:               model.RootID,
		WorkflowVersionID:    model.WorkflowVersionID,
		WorkflowDefinitionID: model.WorkflowDefinitionID,
		SubKey:               model.SubKey,
		WorkerID:             model.WorkerID,
		StartedAt:            model.StartedAt,
		ParentNode:           model.ParentNode,
		MapIndex:             model.MapIndex,
		Progress:             model.Progress,

		BaseRunID:  model.BaseRunID,
		ForkedFrom: model.ForkedFrom,
		RunDepth:   model.RunDepth,
		EditAction: model.EditAction,
		EditLabel:  model.EditLabel,
		ResumeFrom: model.ResumeFrom,
		PatchJSON:  model.PatchJSON,

		RouteKey:          model.RouteKey,
		ModeKey:           model.ModeKey,
		EntryType:         model.EntryType,
		EntryTitle:        model.EntryTitle,
		EntrySubtitle:     model.EntrySubtitle,
		ToolDefinitionID:  model.ToolDefinitionID,
		ToolModeID:        model.ToolModeID,
		ToolModeVersionID: model.ToolModeVersionID,
		TemplateID:        model.TemplateID,
		TemplateVersionID: model.TemplateVersionID,

		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}, nil
}

func (r *taskRepository) ListByParentNode(
	ctx context.Context,
	parentID int64,
	nodeName string,
) ([]*domain.Task, error) {

	var tasks []*domain.Task

	err := r.db.WithContext(ctx).
		Where("parent_id = ? AND parent_node = ?", parentID, nodeName).
		Order("map_index ASC").
		Find(&tasks).Error

	return tasks, err
}

// Enqueue 任务ID 入队列，只有任务ID入队后才会运行任务
func (r *taskRepository) Enqueue(ctx context.Context, taskID int64) error {
	return r.queue.Push(ctx, taskID)
}

func (r *taskRepository) CreateFork(
	ctx context.Context,
	source *domain.Task,
	newTaskId int64,
	newInput []byte,
	editAction, editLabel string,
) (*domain.Task, error) {

	if source == nil {
		return nil, fmt.Errorf("source task is nil")
	}

	baseRunID := source.BaseRunID
	if baseRunID == 0 {
		baseRunID = source.ID
	}

	now := time.Now()

	task := &domain.Task{
		//ID:                   utils.GenSnowflakeID(),
		ID:                   newTaskId,
		UserID:               source.UserID,
		Type:                 source.Type,
		Status:               domain.TaskPending,
		InputJSON:            newInput,
		WorkflowVersionID:    source.WorkflowVersionID,
		WorkflowDefinitionID: source.WorkflowDefinitionID,
		RootID:               0, // 创建后设成自己
		Progress:             0,
		StartedAt:            now,
		BaseRunID:            baseRunID,
		ForkedFrom:           &source.ID,
		RunDepth:             source.RunDepth + 1,
		EditAction:           source.EditAction,
		EditLabel:            source.EditLabel,
		ResumeFrom:           source.ResumeFrom,
		PatchJSON:            source.PatchJSON,

		RouteKey:          source.RouteKey,
		ModeKey:           source.ModeKey,
		EntryType:         source.EntryType,
		EntryTitle:        source.EntryTitle,
		EntrySubtitle:     source.EntrySubtitle,
		ToolDefinitionID:  source.ToolDefinitionID,
		ToolModeID:        source.ToolModeID,
		ToolModeVersionID: source.ToolModeVersionID,
		TemplateID:        source.TemplateID,
		TemplateVersionID: source.TemplateVersionID,
	}

	task.RootID = task.ID

	if editAction != "" {
		task.EditAction = &editAction
	}
	if editLabel != "" {
		task.EditLabel = &editLabel
	}

	if err := r.Create(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

func (r *taskRepository) GetRootTaskByIDAndUser(
	ctx context.Context,
	taskID int64,
	userID int64,
) (*domain.Task, error) {
	var model entity.TaskModel
	err := r.db.WithContext(ctx).
		Where("id = ?", taskID).
		Where("user_id = ?", userID).
		Where("parent_id IS NULL").
		First(&model).Error
	if err != nil {
		return nil, err
	}

	return &domain.Task{
		ID:                   model.ID,
		UserID:               model.UserID,
		Type:                 model.Type,
		Status:               domain.TaskStatus(model.Status),
		RetryCount:           model.RetryCount,
		InputJSON:            model.InputJSON,
		OutputJSON:           model.OutputJSON,
		ParentID:             model.ParentID,
		WorkflowVersionID:    model.WorkflowVersionID,
		WorkflowDefinitionID: model.WorkflowDefinitionID,
		RootID:               model.RootID,
		SubKey:               model.SubKey,
		ParentNode:           model.ParentNode,
		MapIndex:             model.MapIndex,
		Progress:             model.Progress,

		BaseRunID:  model.BaseRunID,
		ForkedFrom: model.ForkedFrom,
		RunDepth:   model.RunDepth,
		EditAction: model.EditAction,
		EditLabel:  model.EditLabel,
		ResumeFrom: model.ResumeFrom,
		PatchJSON:  model.PatchJSON,

		RouteKey:          model.RouteKey,
		ModeKey:           model.ModeKey,
		EntryType:         model.EntryType,
		EntryTitle:        model.EntryTitle,
		EntrySubtitle:     model.EntrySubtitle,
		ToolDefinitionID:  model.ToolDefinitionID,
		ToolModeID:        model.ToolModeID,
		ToolModeVersionID: model.ToolModeVersionID,
		TemplateID:        model.TemplateID,
		TemplateVersionID: model.TemplateVersionID,

		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}, nil
}

func (r *taskRepository) ListByUserV2(
	ctx context.Context,
	userID int64,
	req dto.TaskListReq,
) ([]*dto.Task, int64, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}

	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	db := r.db.WithContext(ctx).
		Model(&entity.TaskModel{}).
		Where("user_id = ?", userID).
		Where("parent_id IS NULL")

	if req.OnlySuccess != nil && *req.OnlySuccess {
		db = db.Where("status = ?", domain.TaskSuccess)
	} else if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}

	if req.EntryType != "" {
		db = db.Where("entry_type = ?", req.EntryType)
	}

	if req.RouteKey != "" {
		db = db.Where("route_key = ?", req.RouteKey)
	}
	if keys := splitFilterKeys(req.ExcludeRouteKeys); len(keys) > 0 {
		db = db.Where("route_key NOT IN ?", keys)
	}

	if req.ModeKey != "" {
		db = db.Where("mode_key = ?", req.ModeKey)
	}
	if keys := splitFilterKeys(req.ExcludeModeKeys); len(keys) > 0 {
		db = db.Where("mode_key NOT IN ?", keys)
	}

	if keyword := strings.TrimSpace(req.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where(
			"(entry_title ILIKE ? OR entry_subtitle ILIKE ? OR route_key ILIKE ? OR mode_key ILIKE ?)",
			like, like, like, like,
		)
	}

	var models []entity.TaskModel
	query := db.Select(
		"id",
		"user_id",
		"status",
		"type",
		"entry_type",
		"entry_title",
		"entry_subtitle",
		"route_key",
		"mode_key",
		"output_json",
		"input_json",
		"error_message",
		"progress",
		"created_at",
		"updated_at",
	).Order("id DESC")

	var total int64
	if req.ResultType == "" {
		if err := db.Count(&total).Error; err != nil {
			return nil, 0, err
		}
		query = query.Offset((page - 1) * pageSize).Limit(pageSize)
	}

	err := query.Find(&models).Error
	if err != nil {
		return nil, 0, err
	}

	result := make([]*dto.Task, 0, len(models))
	for _, m := range models {
		final, err := domain.ParseFinal(m.OutputJSON)
		var input map[string]interface{}
		_ = json.Unmarshal(m.InputJSON, &input) // 反序列化输入参数，方便前端展示
		if err != nil && req.OnlySuccess != nil && *req.OnlySuccess {
			continue // 要求只获取成功的任务，所以final 必须存在
		}
		if req.ResultType != "" {
			if final == nil || final.ResultType != req.ResultType {
				continue
			}
		}
		result = append(result, &dto.Task{
			ID:            m.ID,
			UserID:        userID,
			Status:        m.Status,
			Type:          m.Type,
			EntryType:     &m.EntryType,
			EntryTitle:    m.EntryTitle,
			EntrySubtitle: m.EntrySubtitle,
			RouteKey:      m.RouteKey,
			ModeKey:       m.ModeKey,
			Result:        final,
			Input:         input,
			ErrorMessage:  m.ErrorMessage,
			Progress:      m.Progress,
			CreatedAt:     m.CreatedAt.Unix(),
			UpdatedAt:     m.UpdatedAt.Unix(),
		})
	}

	if req.ResultType != "" {
		total = int64(len(result))
		start := (page - 1) * pageSize
		if start >= len(result) {
			return []*dto.Task{}, total, nil
		}
		end := start + pageSize
		if end > len(result) {
			end = len(result)
		}
		result = result[start:end]
	}

	return result, total, nil
}

func splitFilterKeys(raw string) []string {
	parts := strings.Split(raw, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func (r *taskRepository) GetTaskDetail(ctx context.Context, taskID int64) (*dto.TaskDetail, error) {
	db := r.db.WithContext(ctx).
		Model(&entity.TaskModel{}).
		Where("id = ?", taskID).
		Where("parent_id IS NULL")

	var m entity.TaskModel
	query := db.Select(
		"id",
		"status",
		"user_id",
		"type",
		"entry_type",
		"entry_title",
		"entry_subtitle",
		"route_key",
		"mode_key",
		"output_json",
		"input_json",
		"error_message",
		"retry_count",
		"progress",
		"created_at",
		"updated_at",
	)

	err := query.First(&m).Error
	if err != nil {
		return nil, err
	}

	final, err := domain.ParseFinal(m.OutputJSON)
	var input map[string]interface{}
	_ = json.Unmarshal(m.InputJSON, &input) // 反序列化输入参数，方便前端展示
	return &dto.TaskDetail{
		ID:            m.ID,
		UserID:        m.UserID,
		Status:        m.Status,
		Type:          m.Type,
		EntryType:     &m.EntryType,
		EntryTitle:    m.EntryTitle,
		EntrySubtitle: m.EntrySubtitle,
		RouteKey:      m.RouteKey,
		ModeKey:       m.ModeKey,
		Result:        final,
		Input:         input,
		ErrorMessage:  m.ErrorMessage,
		RetryCount:    m.RetryCount,
		Progress:      m.Progress,
		CreatedAt:     m.CreatedAt.Unix(),
		UpdatedAt:     m.UpdatedAt.Unix(),
	}, nil
}

func (r *taskRepository) ListChildrenByParentID(ctx context.Context, parentID int64) ([]*domain.Task, error) {
	var models []entity.TaskModel

	err := r.db.WithContext(ctx).
		Where("parent_id = ?", parentID).
		Order("id ASC").
		Find(&models).
		Error
	if err != nil {
		return nil, err
	}

	result := make([]*domain.Task, 0, len(models))
	for _, m := range models {
		result = append(result, &domain.Task{
			ID:                   m.ID,
			UserID:               m.UserID,
			Type:                 m.Type,
			Status:               domain.TaskStatus(m.Status),
			RetryCount:           m.RetryCount,
			InputJSON:            m.InputJSON,
			OutputJSON:           m.OutputJSON,
			ErrorMessage:         utils.ValueOrEmpty(m.ErrorMessage),
			ParentID:             m.ParentID,
			WorkflowVersionID:    m.WorkflowVersionID,
			WorkflowDefinitionID: m.WorkflowDefinitionID,
			RootID:               m.RootID,
			SubKey:               m.SubKey,
			ParentNode:           m.ParentNode,
			MapIndex:             m.MapIndex,
			Progress:             m.Progress,

			BaseRunID:  m.BaseRunID,
			ForkedFrom: m.ForkedFrom,
			RunDepth:   m.RunDepth,
			EditAction: m.EditAction,
			EditLabel:  m.EditLabel,
			ResumeFrom: m.ResumeFrom,
			PatchJSON:  m.PatchJSON,

			RouteKey:          m.RouteKey,
			ModeKey:           m.ModeKey,
			EntryType:         m.EntryType,
			EntryTitle:        m.EntryTitle,
			EntrySubtitle:     m.EntrySubtitle,
			ToolDefinitionID:  m.ToolDefinitionID,
			ToolModeID:        m.ToolModeID,
			ToolModeVersionID: m.ToolModeVersionID,
			TemplateID:        m.TemplateID,
			TemplateVersionID: m.TemplateVersionID,

			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}

	return result, nil
}

// 批量更新更高效
func (r *taskRepository) BatchUpdateStatus(ctx context.Context, taskIDs []int64, status domain.TaskStatus, errMsg string) error {
	if len(taskIDs) == 0 {
		return nil
	}

	update := map[string]any{
		"status":     string(status),
		"updated_at": time.Now(),
	}

	if errMsg != "" {
		update["error_message"] = errMsg
	} else {
		update["error_message"] = nil
	}

	return r.db.WithContext(ctx).
		Model(&entity.TaskModel{}).
		Where("id IN ?", taskIDs).
		Updates(update).Error
}
