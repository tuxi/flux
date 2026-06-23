package engine

import (
	"context"
	"flux/definition"
	"flux/domain"
	"flux/tool"
	"flux/workflow"
	"flux/workflow/nodes"
	"testing"

	"github.com/stretchr/testify/require"
)

type patchTestTool struct {
	name   string
	output tool.DataSchema
}

func (t *patchTestTool) Name() string { return t.name }

func (t *patchTestTool) Description() string { return "patch test tool" }

func (t *patchTestTool) InputSchema() tool.DataSchema { return tool.DataSchema{} }

func (t *patchTestTool) OutputSchema() tool.DataSchema { return t.output }

func (t *patchTestTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(map[string]any{}), nil
}

func (t *patchTestTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func newPatchTestWorkflow(t *testing.T) workflow.Workflow {
	t.Helper()

	toolReg := tool.NewRegistry()
	toolReg.Register(&patchTestTool{
		name: "merge_video",
		output: tool.DataSchema{
			Fields: map[string]tool.FieldSchema{
				"file": {Type: "string"},
			},
		},
	})
	toolReg.Register(&patchTestTool{
		name: "single_upload_storage",
		output: tool.DataSchema{
			Fields: map[string]tool.FieldSchema{
				"url": {Type: "string"},
			},
		},
	})

	var def = &definition.WorkflowDefinition{
		Name: "patch_test_workflow",
		Output: definition.OutputDefinition{
			ResultType:     "video",
			PrimaryFileUrl: "nodes.upload_storage.output.url",
		},
		Nodes: []definition.NodeDefinition{
			{
				Name:   "start",
				Type:   "start",
				Weight: 0,
			},
			{
				Name:   "map_images",
				Type:   "map",
				Weight: 0.6,
				Config: map[string]any{
					"items":    "input.image_urls",
					"iterator": "image",
					"workflow": "dummy_sub",
					"parallel": 2,
				},
			},
			{
				Name:   "merge_video",
				Type:   "tool",
				Weight: 0.2,
				Config: map[string]any{
					"tool": "merge_video",
				},
				InputMapping: map[string]string{
					"videos": "map_images.results",
				},
			},
			{
				Name:   "upload_storage",
				Type:   "tool",
				Weight: 0.2,
				Config: map[string]any{
					"tool": "single_upload_storage",
				},
				InputMapping: map[string]string{
					"file_path": "merge_video.file",
				},
			},
			{
				Name:   "end",
				Type:   "end",
				Weight: 0,
			},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "map_images", Type: definition.EdgeNormal},
			{From: "map_images", To: "merge_video", Type: definition.EdgeNormal},
			{From: "merge_video", To: "upload_storage", Type: definition.EdgeNormal},
			{From: "upload_storage", To: "end", Type: definition.EdgeNormal},
		},
	}

	builder := workflow.NewBuilder(nodes.InitNodeRegistry(toolReg))

	wf, err := builder.Build(def)
	require.NoError(t, err)
	return wf
}

func newPatchTestContext(t *testing.T, wf workflow.Workflow) *nodes.Context {
	t.Helper()

	ctx := &nodes.Context{
		Workflow: wf.Source(),
		Input: map[string]any{
			"image_urls": []string{"a.jpg", "b.jpg"},
			"resolution": "720p",
		},
		Output: map[string]any{
			"input": map[string]any{
				"image_urls": []string{"a.jpg", "b.jpg"},
				"resolution": "720p",
			},
			"nodes": map[string]any{},
		},
		Runtime: map[string]*domain.NodeRuntime{
			"map_images": {
				Name:  "map_images",
				State: domain.NodeSuccess,
				Output: map[string]any{
					"results": []any{
						map[string]any{"file_path": "a.mp4"},
						map[string]any{"file_path": "b.mp4"},
					},
				},
				Checkpoint: map[string]any{
					"results": map[string]any{
						"0": map[string]any{"file_path": "a.mp4"},
						"1": map[string]any{"file_path": "b.mp4"},
					},
					"item_hashes": map[string]any{
						"0": "hash-a",
						"1": "hash-b",
					},
					"reused_items": map[string]any{
						"0": false,
						"1": false,
					},
					"done":  2,
					"total": 2,
				},
			},
			"merge_video": {
				Name:  "merge_video",
				State: domain.NodePending,
			},
			"upload_storage": {
				Name:  "upload_storage",
				State: domain.NodePending,
			},
		},
		ActivatedEdges: map[string]bool{},
		PatchedNodes:   map[string]bool{},
	}
	ctx.EnsureOutputInitialized()

	// 预种 map_images output 到 context
	nodeDef := findNode(wf.Nodes(), "map_images")
	require.NotNil(t, nodeDef)

	err := ctx.SetNodeOutput("map_images", deepCloneMap(ctx.Runtime["map_images"].Output), nodeDef.Step.OutputSchema())
	require.NoError(t, err)
	ctx.UpdateNodeStatus("map_images", string(domain.NodeSuccess))

	return ctx
}

func TestPatchPath_Set_ArrayObjectMixed(t *testing.T) {
	root := map[string]any{
		"results": []any{
			map[string]any{"file_path": "a.mp4"},
			map[string]any{"file_path": "b.mp4"},
		},
	}

	err := SetByPath(root, "results[1].file_path", "patched.mp4")
	require.NoError(t, err)

	v, ok := GetByPath(root, "results[1].file_path")
	require.True(t, ok)
	require.Equal(t, "patched.mp4", v)
}

func TestPatchPath_Delete_ArrayObjectMixed(t *testing.T) {
	root := map[string]any{
		"results": []any{
			map[string]any{"file_path": "a.mp4"},
			map[string]any{"file_path": "b.mp4"},
		},
	}

	err := DeleteByPath(root, "results[1].file_path")
	require.NoError(t, err)

	_, ok := GetByPath(root, "results[1].file_path")
	require.False(t, ok)

	v0, ok := GetByPath(root, "results[0].file_path")
	require.True(t, ok)
	require.Equal(t, "a.mp4", v0)
}

func TestPatchPath_Merge_Object(t *testing.T) {
	root := map[string]any{
		"intent": map[string]any{
			"scene": "beach",
		},
	}

	err := MergeByPath(root, "intent", map[string]any{
		"motion": "slow_pan",
		"time":   "sunset",
	})
	require.NoError(t, err)

	v, ok := GetByPath(root, "intent")
	require.True(t, ok)

	obj, ok := v.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "beach", obj["scene"])
	require.Equal(t, "slow_pan", obj["motion"])
	require.Equal(t, "sunset", obj["time"])
}

func TestApplySinglePatchInMemory_SyncsRuntimeAndContext(t *testing.T) {
	wf := newPatchTestWorkflow(t)
	ctx := newPatchTestContext(t, wf)

	e := &Engine{}

	runtime := ctx.Runtime["map_images"]
	require.NotNil(t, runtime)

	patch := domain.RuntimePatch{
		Target: domain.PatchTargetNodeOutput,
		Node:   "map_images",
		Op:     domain.PatchOpSet,
		Path:   "results[1].file_path",
		Value:  "patched.mp4",
	}

	err := e.applySinglePatchInMemory(ctx, wf, runtime, patch)
	require.NoError(t, err)

	// runtime.Output 应更新
	v1, ok := GetByPath(runtime.Output, "results[1].file_path")
	require.True(t, ok)
	require.Equal(t, "patched.mp4", v1)

	// ctx.Output 也应更新
	out := ctx.GetNodeOutput("map_images")
	require.NotNil(t, out)

	v2, ok := GetByPath(out, "results[1].file_path")
	require.True(t, ok)
	require.Equal(t, "patched.mp4", v2)

	require.True(t, ctx.PatchedNodes["map_images"])
	require.True(t, runtime.IsDirty)
	require.Equal(t, DirtyReasonPatchedState, runtime.DirtyReason)
	require.False(t, runtime.IsInjected)
}

func TestCheckpointPatch_RebuildMapOutput(t *testing.T) {
	wf := newPatchTestWorkflow(t)
	ctx := newPatchTestContext(t, wf)

	e := &Engine{
		checkpointRebuilders: newCheckpointRebuildRegistry(),
	}
	e.checkpointRebuilders.Register(definition.NodeMap, func(nodeDef nodes.Node, runtime *domain.NodeRuntime) (map[string]any, error) {
		return rebuildMapNodeOutput(runtime)
	})

	runtime := ctx.Runtime["map_images"]
	require.NotNil(t, runtime)

	patch := domain.RuntimePatch{
		Target: domain.PatchTargetNodeCheckpoint,
		Node:   "map_images",
		Op:     domain.PatchOpSet,
		Path:   "results.1.file_path",
		Value:  "patched-from-checkpoint.mp4",
	}

	err := e.applySinglePatchInMemory(ctx, wf, runtime, patch)
	require.NoError(t, err)

	// checkpoint 被改
	cpVal, ok := GetByPath(runtime.Checkpoint, "results.1.file_path")
	require.True(t, ok)
	require.Equal(t, "patched-from-checkpoint.mp4", cpVal)

	// output 也被重建
	outVal, ok := GetByPath(runtime.Output, "results[1].file_path")
	require.True(t, ok)
	require.Equal(t, "patched-from-checkpoint.mp4", outVal)

	// context output 也同步
	ctxOut := ctx.GetNodeOutput("map_images")
	require.NotNil(t, ctxOut)

	ctxVal, ok := GetByPath(ctxOut, "results[1].file_path")
	require.True(t, ok)
	require.Equal(t, "patched-from-checkpoint.mp4", ctxVal)
}

func TestSetByPath_ArrayObjectMixed(t *testing.T) {
	root := map[string]any{
		"results": []any{
			map[string]any{"file_path": "a.mp4"},
			map[string]any{"file_path": "b.mp4"},
		},
	}

	err := SetByPath(root, "results[1].file_path", "patched.mp4")
	require.NoError(t, err)

	v, ok := GetByPath(root, "results[1].file_path")
	require.True(t, ok)
	require.Equal(t, "patched.mp4", v)
}

func TestValidateRuntimePatch_RejectsInvalidCases(t *testing.T) {
	wf := newPatchTestWorkflow(t)
	e := &Engine{}

	err := e.validateRuntimePatch(wf, domain.RuntimePatch{
		Target: "bad_target",
		Node:   "map_images",
		Op:     domain.PatchOpSet,
		Path:   "results.0.file_path",
		Value:  "patched.mp4",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported patch target")

	err = e.validateRuntimePatch(wf, domain.RuntimePatch{
		Target: domain.PatchTargetNodeOutput,
		Node:   "map_images",
		Op:     "bad_op",
		Path:   "results.0.file_path",
		Value:  "patched.mp4",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported patch op")

	err = e.validateRuntimePatch(wf, domain.RuntimePatch{
		Target: domain.PatchTargetNodeOutput,
		Node:   "map_images",
		Op:     domain.PatchOpSet,
		Path:   "",
		Value:  "patched.mp4",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "patch path is empty")

	err = e.validateRuntimePatch(wf, domain.RuntimePatch{
		Target: domain.PatchTargetNodeOutput,
		Node:   "map_images",
		Op:     domain.PatchOpMerge,
		Path:   "",
		Value:  "not-a-map",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "merge patch value must be map")
}

func TestValidatePatchResumeRelation_RejectsUpstreamBoundary(t *testing.T) {
	wf := newPatchTestWorkflow(t)
	e := &Engine{}

	err := e.validatePatchResumeRelation(wf, domain.RuntimePatch{
		Target: domain.PatchTargetNodeOutput,
		Node:   "map_images",
		Op:     domain.PatchOpSet,
		Path:   "results.1.file_path",
		Value:  "patched.mp4",
	}, "start")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not downstream")

	err = e.validatePatchResumeRelation(wf, domain.RuntimePatch{
		Target: domain.PatchTargetNodeOutput,
		Node:   "map_images",
		Op:     domain.PatchOpSet,
		Path:   "results.1.file_path",
		Value:  "patched.mp4",
	}, "merge_video")
	require.NoError(t, err)
}

func TestIsExecutionRequired(t *testing.T) {
	require.True(t, isExecutionRequired(string(ExecutionReasonResumeBoundary)))
	require.True(t, isExecutionRequired(string(ExecutionReasonUpstreamDirty)))
	require.True(t, isExecutionRequired(string(ExecutionReasonInputChanged)))
	require.True(t, isExecutionRequired(string(ExecutionReasonMissingParent)))
	require.True(t, isExecutionRequired(string(ExecutionReasonParentNotReady)))
	require.True(t, isExecutionRequired(string(ExecutionReasonInputResolveFail)))

	require.False(t, isExecutionRequired(string(ExecutionReasonNone)))
	require.False(t, isExecutionRequired(string(ExecutionReasonReuseNode)))
	require.False(t, isExecutionRequired(string(ExecutionReasonPatchedNode)))
}
