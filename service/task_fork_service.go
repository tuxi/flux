package service

import (
	"context"
	"encoding/json"
	"flux/definition"
	"flux/domain"
	engine2 "flux/engine"
	"flux/pkg/uuid"
	repository2 "flux/repository"
	"flux/workflow"
	"fmt"
)

type TaskForkService struct {
	taskRepo            repository2.TaskRepository
	workflowVersionRepo repository2.WorkflowVersionRepository
	builder             *workflow.Builder
	eng                 *engine2.Engine
	idGen               uuid.SnowNode
}

func NewTaskForkService(
	taskRepo repository2.TaskRepository,
	workflowVersionRepo repository2.WorkflowVersionRepository,
	builder *workflow.Builder,
	eng *engine2.Engine,
) *TaskForkService {
	return &TaskForkService{
		taskRepo:            taskRepo,
		workflowVersionRepo: workflowVersionRepo,
		builder:             builder,
		eng:                 eng,
		idGen:               *uuid.NewNode(4),
	}
}

func (s *TaskForkService) RedoRun(
	ctx context.Context,
	sourceTaskID int64,
	resumeSpec *domain.ResumeSpec,
	overrideInput map[string]any,
	editAction string,
	editLabel string,
	note string,
) (*domain.Task, error) {
	sourceTask, err := s.taskRepo.GetByID(ctx, sourceTaskID)
	if err != nil {
		return nil, err
	}
	if sourceTask == nil {
		return nil, fmt.Errorf("source task not found")
	}

	// 1. 加载 workflow definition，并校验 resumeSpec
	if resumeSpec != nil && resumeSpec.ResumeFrom != "" {
		if err := s.validateResumeSpec(ctx, sourceTask, resumeSpec); err != nil {
			return nil, err
		}
	}

	// 2. 合并 input
	newInput := parseTaskInput(sourceTask.InputJSON)
	if newInput == nil {
		newInput = map[string]any{}
	}
	for k, v := range overrideInput {
		newInput[k] = v
	}

	newInputJSON, err := json.Marshal(newInput)
	if err != nil {
		return nil, err
	}

	// 3. 生成 patch json
	var patchJSON []byte
	if resumeSpec != nil && len(resumeSpec.Patches) > 0 {
		patchJSON, err = json.Marshal(resumeSpec.Patches)
		if err != nil {
			return nil, err
		}
	}

	// 4. 计算 base run id
	baseRunID := sourceTask.BaseRunID
	if baseRunID == 0 {
		baseRunID = sourceTask.ID
	}

	// 5. 生成新 task
	newTaskID := s.idGen.GenSnowID()

	newTask := &domain.Task{
		ID:                   newTaskID,
		RootID:               sourceTask.RootID,
		BaseRunID:            baseRunID,
		ForkedFrom:           &sourceTask.ID,
		RunDepth:             sourceTask.RunDepth + 1,
		WorkflowVersionID:    sourceTask.WorkflowVersionID,
		WorkflowDefinitionID: sourceTask.WorkflowDefinitionID,
		UserID:               sourceTask.UserID,
		Type:                 sourceTask.Type,
		Status:               domain.TaskPending,
		InputJSON:            newInputJSON,
		PatchJSON:            patchJSON,

		EntryType:         sourceTask.EntryType,
		ToolDefinitionID:  sourceTask.ToolDefinitionID,
		ToolModeID:        sourceTask.ToolModeID,
		ToolModeVersionID: sourceTask.ToolModeVersionID,
		TemplateID:        sourceTask.TemplateID,
		TemplateVersionID: sourceTask.TemplateVersionID,
		EntryTitle:        sourceTask.EntryTitle,
		EntrySubtitle:     sourceTask.EntrySubtitle,
		RouteKey:          sourceTask.RouteKey,
		ModeKey:           sourceTask.ModeKey,
	}
	if newTask.RootID == 0 {
		newTask.RootID = sourceTask.ID
	}

	if resumeSpec != nil && resumeSpec.ResumeFrom != "" {
		newTask.ResumeFrom = &resumeSpec.ResumeFrom
	}

	// edit action 给一个稳定默认值，方便前端和 timeline 看懂
	defaultEditAction := "redo_partial"
	newTask.EditAction = &defaultEditAction

	if editLabel != "" {
		newTask.EditLabel = &editLabel
	}
	if editAction != "" {
		newTask.EditAction = &editAction
	}

	// note 当前 domain.Task 没有字段，就先不落；以后可以进 event / task metadata

	// 6. 落库
	if err := s.taskRepo.Create(ctx, newTask); err != nil {
		return nil, err
	}

	// 7. 入队
	if err := s.taskRepo.Enqueue(ctx, newTask.ID); err != nil {
		return nil, err
	}

	return newTask, nil
}

func parseTaskInput(inputJSON []byte) map[string]any {
	if len(inputJSON) == 0 {
		return map[string]any{}
	}

	var input map[string]any
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		return map[string]any{}
	}
	return input
}

func (s *TaskForkService) validateResumeSpec(
	ctx context.Context,
	sourceTask *domain.Task,
	resumeSpec *domain.ResumeSpec,
) error {
	if sourceTask == nil || resumeSpec == nil || resumeSpec.ResumeFrom == "" {
		return nil
	}

	dbVersion, err := s.workflowVersionRepo.Get(ctx, sourceTask.WorkflowVersionID)
	if err != nil {
		return err
	}

	var wfDef definition.WorkflowDefinition
	if err := json.Unmarshal(dbVersion.DefinitionJSON, &wfDef); err != nil {
		return err
	}

	wf, err := s.builder.Build(&wfDef)
	if err != nil {
		return err
	}

	// 这里优先复用 engine 的校验逻辑
	if s.eng != nil {
		if err := s.eng.ValidateResumeSpecForExternal(wf, resumeSpec); err == nil {
			return nil
		}
	}

	// 如果 engine 还没有暴露公共方法，这里做最小校验
	if _, ok := wf.Nodes()[resumeSpec.ResumeFrom]; !ok {
		return fmt.Errorf("resume_from node not found: %s", resumeSpec.ResumeFrom)
	}

	return nil
}
