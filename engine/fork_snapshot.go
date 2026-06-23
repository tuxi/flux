package engine

import (
	"flux/domain"
	"flux/workflow/nodes"
	"fmt"
)

func (e *Engine) LoadForkParentSnapshot(runCtx *nodes.Context) error {
	if runCtx == nil || runCtx.Task == nil {
		return fmt.Errorf("runCtx/task is nil")
	}
	if runCtx.Task.ForkedFrom == nil {
		return nil
	}

	parentTask, err := e.taskRepo.GetByID(runCtx.Ctx, *runCtx.Task.ForkedFrom)
	if err != nil {
		return err
	}
	if parentTask == nil {
		return fmt.Errorf("fork parent task not found")
	}

	parentNodes, err := e.nodeRepo.FindByTaskID(runCtx.Ctx, parentTask.ID)
	if err != nil {
		return err
	}

	parentMap := make(map[string]*domain.NodeRuntime, len(parentNodes))
	for _, n := range parentNodes {
		if n == nil {
			continue
		}
		parentMap[n.Name] = n
	}

	runCtx.ParentSnapshot = &nodes.ReuseSnapshot{
		TaskID: parentTask.ID,
		Nodes:  parentMap,
		Output: parseTaskOutput(parentTask.OutputJSON),
	}
	return nil
}
