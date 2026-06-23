package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flux/definition"
	"flux/domain"
	"flux/repository"
)

// WorkflowRegistry 工作流注册
type WorkflowRegistry struct {
	workflows map[string]*definition.WorkflowDefinition

	workflowRepo repository.WorkflowRepository
	versionRepo  repository.WorkflowVersionRepository
}

func NewWorkflowRegistry(
	workflowRepo repository.WorkflowRepository,
	versionRepo repository.WorkflowVersionRepository,
) *WorkflowRegistry {
	return &WorkflowRegistry{
		workflows:    map[string]*definition.WorkflowDefinition{},
		workflowRepo: workflowRepo,
		versionRepo:  versionRepo,
	}
}

func (r *WorkflowRegistry) Register(def *definition.WorkflowDefinition) {
	if _, ok := r.workflows[def.Name]; ok {
		panic(fmt.Errorf("workflow %s already exists", def.Name))
	}
	r.workflows[def.Name] = def
}

func (r *WorkflowRegistry) Sync(ctx context.Context) error {
	fmt.Println("WorkflowRegistry Sync Start")

	for name, wf := range r.workflows {
		fmt.Println("sync workflow:", name)

		if err := r.syncWorkflow(ctx, wf); err != nil {
			return err
		}
	}

	fmt.Println("WorkflowRegistry Sync Finished")
	return nil
}

func (r *WorkflowRegistry) syncWorkflow(
	ctx context.Context,
	def *definition.WorkflowDefinition,
) error {
	hash := def.Hash()

	//--------------------------------
	// 1. 获取或创建 workflow definition
	//--------------------------------
	wf, err := r.workflowRepo.GetByName(ctx, def.Name)
	if err != nil {
		wf = &domain.Workflow{
			Name:        def.Name,
			Description: def.Desc,
		}

		if err := r.workflowRepo.Create(ctx, wf); err != nil {
			return err
		}
	} else {
		if wf != nil && wf.Description != def.Desc {
			wf = &domain.Workflow{
				ID:          wf.ID,
				Name:        def.Name,
				Description: def.Desc,
			}
			if err := r.workflowRepo.Update(ctx, wf); err != nil {
				return err
			}
		}
	}

	//--------------------------------
	// 2. 获取最新版本，对比 hash
	//--------------------------------
	latest, err := r.versionRepo.GetLatestByWorkflowID(ctx, wf.ID)
	if err == nil && latest != nil {
		if latest.Hash == hash {
			// hash 未变，但定义 JSON 可能因非语义字段（如 label）变化而不同
			js, _ := json.Marshal(def)
			if bytes.Equal(js, latest.DefinitionJSON) {
				fmt.Println("workflow unchanged skip:", def.Name)
				return nil
			}
			fmt.Println("workflow definition json updated (non-semantic change):", def.Name)
			return r.versionRepo.UpdateDefinitionJSON(ctx, latest.ID, js)
		}
	}

	fmt.Println("workflow changed publish new version:", def.Name)

	//--------------------------------
	// 3. 先发布新版本
	//--------------------------------
	js, _ := json.Marshal(def)

	version := &domain.WorkflowVersion{
		WorkflowID:     wf.ID,
		Version:        time.Now().Unix(),
		Hash:           hash,
		DefinitionJSON: js,
	}

	if err := r.versionRepo.Create(ctx, version); err != nil {
		return err
	}

	//--------------------------------
	// 4. 将 nodes / edges 绑定到当前 version
	//--------------------------------
	//if err := r.syncNodes(ctx, version.ID, def.Nodes); err != nil {
	//	return err
	//}
	//
	//if err := r.syncEdges(ctx, version.ID, def.Edges); err != nil {
	//	return err
	//}

	return nil
}

//
//func (r *WorkflowRegistry) syncNodes(
//	ctx context.Context,
//	workflowVersionID int64,
//	nodes []definition.NodeDefinition,
//) error {
//	for _, n := range nodes {
//		config := n.Config
//		if config == nil {
//			config = make(map[string]any)
//		}
//
//		// 注意：这里仍然把 InputMapping 收敛进 ConfigJSON
//		if n.InputMapping != nil {
//			config["input_mapping"] = n.InputMapping
//		}
//
//		configJSON, _ := json.Marshal(config)
//
//		node := &entity.WorkflowNodeDefinitionModel{
//			WorkflowVersionID: workflowVersionID,
//			Name:              n.Name,
//			Type:              string(n.Type),
//			ConfigJSON:        configJSON,
//			Weight:            n.Weight,
//		}
//
//		if err := r.nodeRepo.Create(ctx, node); err != nil {
//			return err
//		}
//	}
//
//	return nil
//}
//
//func (r *WorkflowRegistry) syncEdges(
//	ctx context.Context,
//	workflowVersionID int64,
//	edges []definition.EdgeDefinition,
//) error {
//	for _, e := range edges {
//		edge := &entity.WorkflowEdgeDefinitionModel{
//			WorkflowVersionID: workflowVersionID,
//			FormNode:          e.From,
//			ToNode:            e.To,
//			ConditionExpr:     e.Condition,
//			CaseValue:         e.CaseKey,
//			Priority:          e.Priority,
//			Type:              string(e.Type),
//			Label:             e.Label,
//		}
//
//		if err := r.edgeRepo.Create(ctx, edge); err != nil {
//			return err
//		}
//	}
//
//	return nil
//}
