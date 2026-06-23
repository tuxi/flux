package engine

import (
	"fmt"
	"sort"
	"strconv"

	"flux/domain"
	"flux/utils"
	"flux/workflow"
	"flux/workflow/nodes"
)

func (e *Engine) rebuildNodeOutputFromCheckpoint(
	ctx *nodes.Context,
	wf workflow.Workflow,
	runtime *domain.NodeRuntime,
) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if runtime == nil {
		return fmt.Errorf("runtime is nil")
	}

	nodeDef := findNode(wf.Nodes(), runtime.Name)
	if nodeDef == nil {
		return fmt.Errorf("node def not found: %s", runtime.Name)
	}

	rebuilder, ok := e.checkpointRebuilders.Get(string(nodeDef.Type))
	if !ok {
		return fmt.Errorf("checkpoint rebuild not supported for node type: %s", nodeDef.Type)
	}

	rebuilt, err := rebuilder(*nodeDef, runtime)
	if err != nil {
		return err
	}
	if rebuilt == nil {
		return fmt.Errorf("checkpoint rebuild returned nil for node: %s", runtime.Name)
	}

	runtime.Output = rebuilt
	runtime.OutputHash = ctx.CalculateOutputHash(rebuilt)

	if err := ctx.SetNodeOutput(
		runtime.Name,
		deepCloneMap(rebuilt),
		nodeDef.Step.OutputSchema(),
	); err != nil {
		return err
	}

	ctx.UpdateNodeStatus(runtime.Name, string(runtime.State))
	return nil
}

// computeOutputFromCheckpoint
//
// 当前先做“最小可用集”：
// 1. Map 节点：checkpoint["results"] -> output["results"]
// 2. 以后你们可以继续扩展其它带 durable checkpoint 的节点
func (e *Engine) computeOutputFromCheckpoint(
	wf workflow.Workflow,
	nodeDef nodes.Node,
	runtime *domain.NodeRuntime,
) (map[string]any, error) {
	if runtime.Checkpoint == nil {
		return nil, nil
	}

	if e.checkpointRebuilders == nil {
		return nil, nil
	}

	fn, ok := e.checkpointRebuilders.Get(string(nodeDef.Type))
	if !ok {
		return nil, nil
	}

	return fn(nodeDef, runtime)
}

func rebuildMapNodeOutput(runtime *domain.NodeRuntime) (map[string]any, error) {
	cp := runtime.Checkpoint
	if cp == nil {
		return nil, nil
	}

	rawResults, _ := cp["results"].(map[string]any)
	if rawResults == nil {
		return nil, nil
	}

	total := 0
	if v, ok := cp["total"]; ok {
		switch x := v.(type) {
		case int:
			total = x
		case int32:
			total = int(x)
		case int64:
			total = int(x)
		case float32:
			total = int(x)
		case float64:
			total = int(x)
		}
	}

	// 如果没有 total，就按 key 排序重建
	if total <= 0 {
		indexes := make([]int, 0, len(rawResults))
		for k := range rawResults {
			idx, err := strconv.Atoi(k)
			if err != nil {
				return nil, fmt.Errorf("invalid map results index key: %s", k)
			}
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)

		results := make([]any, 0, len(indexes))
		for _, idx := range indexes {
			v, ok := rawResults[strconv.Itoa(idx)]
			if !ok {
				continue
			}
			results = append(results, deepCloneAny(v))
		}

		return map[string]any{
			"results": results,
		}, nil
	}

	results := make([]any, total)
	for i := 0; i < total; i++ {
		if v, ok := rawResults[strconv.Itoa(i)]; ok {
			results[i] = deepCloneAny(v)
		}
	}

	return map[string]any{
		"results": results,
	}, nil
}

func rebuildLoopNodeOutput(runtime *domain.NodeRuntime) (map[string]any, error) {
	cp := runtime.Checkpoint
	if cp == nil {
		return nil, nil
	}

	rawResults, ok := cp["results"]
	if !ok || rawResults == nil {
		return nil, nil
	}

	results, ok := rawResults.([]any)
	if ok {
		cloned := make([]any, len(results))
		for i, v := range results {
			cloned[i] = deepCloneAny(v)
		}
		return map[string]any{
			"results": cloned,
		}, nil
	}

	// 兼容极端情况：如果某些驱动/序列化导致它变成 []interface{} 以外的结构
	if converted, ok := utils.ToAnySlice(rawResults); ok {
		cloned := make([]any, len(converted))
		for i, v := range converted {
			cloned[i] = deepCloneAny(v)
		}
		return map[string]any{
			"results": cloned,
		}, nil
	}

	return nil, fmt.Errorf("invalid loop checkpoint results type")
}
