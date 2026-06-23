package nodes

import (
	"context"
	"flux/definition"
	"flux/domain"
	"flux/eventbus"
	"testing"

	"github.com/stretchr/testify/require"
)

func newMapExecContext(
	t *testing.T,
	taskRepo *loopFakeTaskRepo,
	nodeRepo *loopFakeNodeRepo,
	runtime *domain.NodeRuntime,
	input map[string]any,
) *NodeExecContext {
	t.Helper()

	def := &definition.WorkflowDefinition{
		Name: "map_test_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.map_render.output.results[0].primary_file_url",
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name: "map_render",
				Type: definition.NodeMap,
				Config: map[string]any{
					"items":    "input.items",
					"iterator": "item",
					"workflow": "map_child",
					"parallel": 2,
				},
			},
			{Name: "end", Type: definition.NodeEnd},
		},
	}
	ctx := &Context{
		Ctx:      context.Background(),
		Task:     &domain.Task{ID: 1, RootID: 1},
		Workflow: def,
		Input:    cloneMapAny(input),
		Output: map[string]any{
			"input": cloneMapAny(input),
			"nodes": map[string]any{},
		},
		Runtime: map[string]*domain.NodeRuntime{
			"map_render": runtime,
		},
		EventBus: eventbus.NewEventBus(nil, nil),
	}
	ctx.EnsureOutputInitialized()

	execCtx := &NodeExecContext{
		TaskContext: ctx,
		Input:       cloneMapAny(input),
		Output:      map[string]any{},
		NodeDef: &definition.NodeDefinition{
			Name:   "map_render",
			Type:   definition.NodeMap,
			Config: map[string]any{"workflow": "map_child"},
		},
		Executor: &loopFakeExecutor{
			taskRepo: taskRepo,
			nodeRepo: nodeRepo,
		},
	}
	return execCtx
}

func TestMapProcessExistingChildren_FanInSuccessIntoCheckpoint(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2)
	runtime := &domain.NodeRuntime{
		Name:  "map_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			mapCPTotal:       2,
			mapCPDone:        0,
			mapCPResults:     map[string]any{},
			mapCPItemHashes:  map[string]any{},
			mapCPReusedItems: map[string]any{},
		},
	}
	child0 := &domain.Task{
		ID:        201,
		Status:    domain.TaskSuccess,
		MapIndex:  intPtr(0),
		InputJSON: mustJSON(t, map[string]any{"index": 0, "__map_item_hash": "hash-0"}),
		OutputJSON: mustJSON(t, map[string]any{
			"final": map[string]any{
				"result_type":      "video",
				"primary_file_url": "https://example.com/a.mp4",
			},
		}),
	}
	child1 := &domain.Task{
		ID:        202,
		Status:    domain.TaskSuspended,
		MapIndex:  intPtr(1),
		InputJSON: mustJSON(t, map[string]any{"index": 1, "__map_item_hash": "hash-1"}),
	}
	taskRepo := newLoopFakeTaskRepo()
	parentID := int64(1)
	parentNode := "map_render"
	child0.ParentID, child0.ParentNode = &parentID, &parentNode
	child1.ParentID, child1.ParentNode = &parentID, &parentNode
	nodeRepo := newLoopFakeNodeRepo(runtime)
	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": []any{"frame-a", "frame-b"},
	})
	execCtx.TaskContext.Task.ID = parentID

	// ListByParentNode relies on ParentID/ParentNode, so inject via repo-less tasks map isn't enough.
	// Reuse engine-side fake repo implementation behavior through a small inline list override is not available here,
	// so we store the tasks on the loop fake and use ParentID/ParentNode aware helper below.
	taskRepo.tasks["child-0"] = child0
	taskRepo.tasks["child-1"] = child1

	err := step.processExistingChildren(execCtx, runtime, []any{"frame-a", "frame-b"})
	require.NoError(t, err)

	results := runtime.Checkpoint[mapCPResults].(map[string]any)
	item0, ok := results["0"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/a.mp4", item0["primary_file_url"])
	require.Equal(t, 1, runtime.Checkpoint[mapCPDone])

	itemHashes := runtime.Checkpoint[mapCPItemHashes].(map[string]any)
	require.Equal(t, "hash-0", itemHashes["0"])
}

func TestMapEmitFanoutProgressSummaryOnly(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2)
	runtime := &domain.NodeRuntime{
		Name:  "map_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			mapCPTotal:       8,
			mapCPDone:        3,
			mapCPResults:     map[string]any{"0": map[string]any{"large": "result"}},
			mapCPReusedItems: map[string]any{"1": true},
		},
	}
	parentID := int64(1)
	parentNode := "map_render"
	running0 := &domain.Task{ID: 201, Status: domain.TaskRunning, MapIndex: intPtr(4), ParentID: &parentID, ParentNode: &parentNode}
	running1 := &domain.Task{ID: 202, Status: domain.TaskSuspended, MapIndex: intPtr(5), ParentID: &parentID, ParentNode: &parentNode}
	failed := &domain.Task{ID: 203, Status: domain.TaskFailed, MapIndex: intPtr(6), ParentID: &parentID, ParentNode: &parentNode}
	taskRepo := newLoopFakeTaskRepo()
	taskRepo.tasks["running-0"] = running0
	taskRepo.tasks["running-1"] = running1
	taskRepo.tasks["failed"] = failed
	execCtx := newMapExecContext(t, taskRepo, newLoopFakeNodeRepo(runtime), runtime, map[string]any{
		"items": []any{"a", "b"},
	})
	ch := execCtx.TaskContext.EventBus.Subscribe(TaskEventFanoutProgress)

	step.emitFanoutProgress(execCtx, runtime)

	evt := <-ch
	require.Equal(t, TaskEventFanoutProgress, evt.Type)
	require.Equal(t, "map", evt.Meta["fanout_kind"])
	require.Equal(t, 8, evt.Meta["total"])
	require.Equal(t, 3, evt.Meta["done"])
	require.Equal(t, 2, evt.Meta["running"])
	require.Equal(t, 1, evt.Meta["failed"])
	require.Equal(t, 1, evt.Meta["reused"])
	require.Equal(t, 5, evt.Meta["current_index"])
	require.NotContains(t, evt.Meta, mapCPResults)
	require.NotContains(t, evt.Meta, "checkpoint")
}

func TestMapProcessExistingChildren_PartialMode_WritesFallbackForFailedChild(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2).
		WithFailurePolicy("partial").
		WithMaxChildRetries(0).
		WithFallbackSource("item")

	runtime := &domain.NodeRuntime{
		Name:  "map_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			mapCPTotal:       3,
			mapCPDone:        0,
			mapCPResults:     map[string]any{},
			mapCPItemHashes:  map[string]any{},
			mapCPReusedItems: map[string]any{},
		},
	}

	parentID := int64(1)
	parentNode := "map_render"

	// index 0: success
	child0 := &domain.Task{
		ID:         201,
		Status:     domain.TaskSuccess,
		MapIndex:   intPtr(0),
		ParentID:   &parentID,
		ParentNode: &parentNode,
		InputJSON:  mustJSON(t, map[string]any{"index": 0, "__map_item_hash": "hash-0"}),
		OutputJSON: mustJSON(t, map[string]any{
			"final": map[string]any{
				"result_type":      "image",
				"primary_file_url": "https://example.com/generated-a.jpg",
			},
		}),
	}
	// index 1: failed (deterministic error)
	child1 := &domain.Task{
		ID:           202,
		Status:       domain.TaskFailed,
		RetryCount:   5,
		ErrorMessage: "parameter error: invalid aspect_ratio",
		MapIndex:     intPtr(1),
		ParentID:     &parentID,
		ParentNode:   &parentNode,
		InputJSON:    mustJSON(t, map[string]any{"index": 1}),
	}
	// index 2: success
	child2 := &domain.Task{
		ID:         203,
		Status:     domain.TaskSuccess,
		MapIndex:   intPtr(2),
		ParentID:   &parentID,
		ParentNode: &parentNode,
		InputJSON:  mustJSON(t, map[string]any{"index": 2, "__map_item_hash": "hash-2"}),
		OutputJSON: mustJSON(t, map[string]any{
			"final": map[string]any{
				"result_type":      "image",
				"primary_file_url": "https://example.com/generated-b.jpg",
			},
		}),
	}

	taskRepo := newLoopFakeTaskRepo()
	taskRepo.tasks["child-0"] = child0
	taskRepo.tasks["child-1"] = child1
	taskRepo.tasks["child-2"] = child2
	nodeRepo := newLoopFakeNodeRepo(runtime)

	items := []any{
		map[string]any{"source_image_url": "https://example.com/orig-a.jpg"},
		map[string]any{"source_image_url": "https://example.com/orig-b.jpg", "aspect_ratio": "1:1"},
		map[string]any{"source_image_url": "https://example.com/orig-c.jpg"},
	}

	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": items,
	})
	execCtx.TaskContext.Task.ID = parentID

	err := step.processExistingChildren(execCtx, runtime, items)
	require.NoError(t, err)

	// 3 results written (2 success + 1 fallback)
	results := runtime.Checkpoint[mapCPResults].(map[string]any)
	require.Len(t, results, 3)
	require.Equal(t, 3, runtime.Checkpoint[mapCPDone])

	// index 0: success result
	r0, ok := results["0"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/generated-a.jpg", r0["primary_file_url"])

	// index 1: fallback result
	r1, ok := results["1"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fallback", r1["status"])
	require.Equal(t, "low", r1["quality"])
	require.Equal(t, "original", r1["source"])
	require.Equal(t, true, r1["degraded"])
	require.Equal(t, true, r1["fallback_used"])
	require.Equal(t, "item", r1["fallback_source"])
	require.Contains(t, r1["error"], "parameter error")
	require.Equal(t, "https://example.com/orig-b.jpg", r1["primary_file_url"])
	require.Equal(t, "https://example.com/orig-b.jpg", r1["image_url"])
	require.NotNil(t, r1["original_item"])

	// index 2: success result
	r2, ok := results["2"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/generated-b.jpg", r2["primary_file_url"])

	// finalizeCompleted: verify meta and quality stats in output
	err = step.finalizeCompleted(execCtx, runtime)
	require.NoError(t, err)

	require.NotNil(t, runtime.Output)
	outResults, ok := runtime.Output["results"].([]any)
	require.True(t, ok)
	require.Len(t, outResults, 3)

	out0, ok := outResults[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "success", out0["status"])
	require.Equal(t, "high", out0["quality"])
	require.Equal(t, "ai", out0["source"])
	require.Equal(t, false, out0["degraded"])

	out1, ok := outResults[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fallback", out1["status"])
	require.Equal(t, "low", out1["quality"])
	require.Equal(t, true, out1["degraded"])

	meta, ok := runtime.Output["meta"].(map[string]any)
	require.True(t, ok)
	successIndexes, ok := meta["success_indexes"].([]int)
	require.True(t, ok)
	require.Equal(t, []int{0, 2}, successIndexes)
	fallbackIndexes, ok := meta["fallback_indexes"].([]int)
	require.True(t, ok)
	require.Equal(t, []int{1}, fallbackIndexes)
	failedIndexes, ok := meta["failed_indexes"].([]int)
	require.True(t, ok)
	require.Equal(t, []int{1}, failedIndexes)

	require.Equal(t, 2, runtime.Output["success_count"])
	require.Equal(t, 1, runtime.Output["fallback_count"])
	require.Equal(t, 2, runtime.Output["high_quality_count"])
	require.Equal(t, 1, runtime.Output["low_quality_count"])
	require.InDelta(t, 1.0/3.0, runtime.Output["fallback_rate"], 0.01)
}

func TestMapProcessExistingChildren_PartialMode_HandlesCanceledChild(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2).
		WithFailurePolicy("partial")

	runtime := &domain.NodeRuntime{
		Name:  "map_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			mapCPTotal:       2,
			mapCPDone:        0,
			mapCPResults:     map[string]any{},
			mapCPItemHashes:  map[string]any{},
			mapCPReusedItems: map[string]any{},
		},
	}

	parentID := int64(1)
	parentNode := "map_render"

	child0 := &domain.Task{
		ID:         301,
		Status:     domain.TaskCanceled,
		RetryCount: 5,
		MapIndex:   intPtr(0),
		ParentID:   &parentID,
		ParentNode: &parentNode,
		InputJSON:  mustJSON(t, map[string]any{"index": 0}),
	}

	taskRepo := newLoopFakeTaskRepo()
	taskRepo.tasks["child-0"] = child0
	nodeRepo := newLoopFakeNodeRepo(runtime)

	items := []any{
		map[string]any{"source_image_url": "https://example.com/orig.jpg"},
		map[string]any{"source_image_url": "https://example.com/orig2.jpg"},
	}

	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": items,
	})
	execCtx.TaskContext.Task.ID = parentID

	err := step.processExistingChildren(execCtx, runtime, items)
	require.NoError(t, err)

	results := runtime.Checkpoint[mapCPResults].(map[string]any)
	require.Len(t, results, 1)
	r0, ok := results["0"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fallback", r0["status"])
}

func TestMapProcessExistingChildren_FailFast_StillFailsOnChildFailed(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2) // default fail_fast

	runtime := &domain.NodeRuntime{
		Name:  "map_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			mapCPTotal:       1,
			mapCPDone:        0,
			mapCPResults:     map[string]any{},
			mapCPItemHashes:  map[string]any{},
			mapCPReusedItems: map[string]any{},
		},
	}

	parentID := int64(1)
	parentNode := "map_render"
	failedChild := &domain.Task{
		ID:         401,
		Status:     domain.TaskFailed,
		RetryCount: 2,
		MapIndex:   intPtr(0),
		ParentID:   &parentID,
		ParentNode: &parentNode,
		InputJSON:  mustJSON(t, map[string]any{"index": 0}),
	}

	taskRepo := newLoopFakeTaskRepo()
	taskRepo.tasks["failed-child"] = failedChild
	nodeRepo := newLoopFakeNodeRepo(runtime)

	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": []any{"frame-a"},
	})
	execCtx.TaskContext.Task.ID = parentID

	err := step.processExistingChildren(execCtx, runtime, []any{"frame-a"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "map child task failed")
}

func TestMapFillReusableItems_UsesParentSnapshotAndMarksReuse(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2)
	runtime := &domain.NodeRuntime{
		Name:  "map_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			mapCPTotal:       2,
			mapCPDone:        0,
			mapCPResults:     map[string]any{},
			mapCPItemHashes:  map[string]any{},
			mapCPReusedItems: map[string]any{},
		},
	}
	nodeRepo := newLoopFakeNodeRepo(runtime)
	taskRepo := newLoopFakeTaskRepo()
	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": []any{
			map[string]any{"image": "a.jpg"},
			map[string]any{"image": "b.jpg"},
		},
	})
	execCtx.TaskContext.ParentSnapshot = &ReuseSnapshot{
		TaskID: 99,
		Nodes: map[string]*domain.NodeRuntime{
			"map_render": {
				TaskID: 99,
				Name:   "map_render",
				Checkpoint: map[string]any{
					mapCPItemHashes: map[string]any{
						"0": CalculateMapItemHash(map[string]any{"image": "a.jpg"}),
						"1": "different-hash",
					},
					mapCPResults: map[string]any{
						"0": map[string]any{"primary_file_url": "https://example.com/reused-a.mp4"},
						"1": map[string]any{"primary_file_url": "https://example.com/reused-b.mp4"},
					},
				},
			},
		},
	}
	execCtx.TaskContext.MapItemReuse = map[string]map[int]bool{
		"map_render": {0: true, 1: false},
	}

	err := step.fillReusableItems(execCtx, runtime, []any{
		map[string]any{"image": "a.jpg"},
		map[string]any{"image": "b.jpg"},
	})
	require.NoError(t, err)

	results := runtime.Checkpoint[mapCPResults].(map[string]any)
	item0, ok := results["0"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/reused-a.mp4", item0["primary_file_url"])
	_, exists := results["1"]
	require.False(t, exists)

	reusedItems := runtime.Checkpoint[mapCPReusedItems].(map[string]any)
	require.Equal(t, true, reusedItems["0"])
	require.Equal(t, 1, runtime.Checkpoint[mapCPDone])
}

func TestMapRun_FinalizesWhenAllItemsAreReused(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2)
	runtime := &domain.NodeRuntime{Name: "map_render", State: domain.NodePending}
	nodeRepo := newLoopFakeNodeRepo(runtime)
	taskRepo := newLoopFakeTaskRepo()
	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": []any{
			map[string]any{"image": "a.jpg"},
			map[string]any{"image": "b.jpg"},
		},
	})
	execCtx.TaskContext.ParentSnapshot = &ReuseSnapshot{
		TaskID: 88,
		Nodes: map[string]*domain.NodeRuntime{
			"map_render": {
				TaskID: 88,
				Name:   "map_render",
				Checkpoint: map[string]any{
					mapCPItemHashes: map[string]any{
						"0": CalculateMapItemHash(map[string]any{"image": "a.jpg"}),
						"1": CalculateMapItemHash(map[string]any{"image": "b.jpg"}),
					},
					mapCPResults: map[string]any{
						"0": map[string]any{"primary_file_url": "https://example.com/a.mp4"},
						"1": map[string]any{"primary_file_url": "https://example.com/b.mp4"},
					},
				},
			},
		},
	}

	err := step.Run(execCtx)
	require.NoError(t, err)

	require.NotNil(t, runtime.Output)
	results, ok := runtime.Output["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 2)
	require.Equal(t, domain.ReuseMapItems, runtime.ReuseKind)
	require.Equal(t, results, execCtx.Output["results"])
}

func TestMapRun_FailsWhenExistingChildFailed(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2)
	runtime := &domain.NodeRuntime{Name: "map_render", State: domain.NodeRunning}
	taskRepo := newLoopFakeTaskRepo()
	parentID := int64(1)
	parentNode := "map_render"
	failedChild := &domain.Task{
		ID:         301,
		Status:     domain.TaskFailed,
		RetryCount: 2,
		MapIndex:   intPtr(0),
		ParentID:   &parentID,
		ParentNode: &parentNode,
		InputJSON:  mustJSON(t, map[string]any{"index": 0}),
	}
	taskRepo.tasks["failed-child"] = failedChild
	nodeRepo := newLoopFakeNodeRepo(runtime)
	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": []any{"frame-a"},
	})
	execCtx.TaskContext.Task.ID = parentID

	err := step.Run(execCtx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "map child task failed")
}

func TestMapDispatchPendingChildren_SkipsIndexesAlreadyInFlight(t *testing.T) {
	step := NewMapStep("input.items", "item", "map_child", 2)
	runtime := &domain.NodeRuntime{
		Name:  "map_render",
		State: domain.NodeRunning,
		Checkpoint: map[string]any{
			mapCPTotal:       2,
			mapCPDone:        0,
			mapCPResults:     map[string]any{},
			mapCPItemHashes:  map[string]any{},
			mapCPReusedItems: map[string]any{},
		},
	}
	parentID := int64(1)
	parentNode := "map_render"
	inFlightChild := &domain.Task{
		ID:         401,
		Status:     domain.TaskSuspended,
		MapIndex:   intPtr(0),
		ParentID:   &parentID,
		ParentNode: &parentNode,
		InputJSON: mustJSON(t, map[string]any{
			"index":           0,
			"item":            map[string]any{"image": "a.jpg"},
			"__map_item_hash": CalculateMapItemHash(map[string]any{"image": "a.jpg"}),
		}),
	}
	taskRepo := newLoopFakeTaskRepo()
	taskRepo.tasks["child-0"] = inFlightChild
	nodeRepo := newLoopFakeNodeRepo(runtime)
	executor := &loopFakeExecutor{
		taskRepo: taskRepo,
		nodeRepo: nodeRepo,
	}
	execCtx := newMapExecContext(t, taskRepo, nodeRepo, runtime, map[string]any{
		"items": []any{
			map[string]any{"image": "a.jpg"},
			map[string]any{"image": "b.jpg"},
		},
	})
	execCtx.Executor = executor
	execCtx.TaskContext.Task.ID = parentID

	err := step.dispatchPendingChildren(execCtx, runtime, []any{
		map[string]any{"image": "a.jpg"},
		map[string]any{"image": "b.jpg"},
	})
	require.NoError(t, err)
	require.Len(t, executor.runInputs, 1)
	require.Equal(t, 1, executor.runInputs[0]["index"])
	item, ok := executor.runInputs[0]["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "b.jpg", item["image"])
}

func TestResolveValue_FallbackSkipsNodeWithoutOutput(t *testing.T) {
	ctx := &Context{
		Ctx: context.Background(),
		Output: map[string]any{
			"input": map[string]any{},
			"nodes": map[string]any{
				"merge_product_image_resources": map[string]any{
					"status": "skipped",
				},
				"normalize_assets": map[string]any{
					"status": "success",
					"output": map[string]any{
						"product_images": []any{
							map[string]any{"url": "https://example.com/a.png"},
						},
					},
				},
			},
		},
	}
	execCtx := &NodeExecContext{TaskContext: ctx}

	val, err := resolveValue(
		"merge_product_image_resources.product_images ?? normalize_assets.product_images",
		execCtx,
	)
	require.NoError(t, err)

	items, ok := val.([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://example.com/a.png", first["url"])
}

func intPtr(i int) *int {
	return &i
}
