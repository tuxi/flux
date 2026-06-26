package dreamai_test

import (
	"context"
	"testing"

	"flux/adapters/dreamai"
	"flux/definition"
	"flux/planner"
	"flux/runtime"
	"flux/tool"
)

// commerceAssetPrepareDef 构造与 DreamAI 的 commerce_asset_prepare 完全一致的 DAG。
func commerceAssetPrepareDef() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		Name: "commerce_asset_prepare",
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name:    "normalize_assets",
				Type:    definition.NodeTool,
				Config:  map[string]any{"tool": "goods_normalize_assets"},
				Version: "v1",
			},
			{
				Name:    "extract_reference_frames",
				Type:    definition.NodeTool,
				Config:  map[string]any{"tool": "extract_reference_frames"},
				Version: "v1",
			},
			{
				Name:    "build_reference_registry",
				Type:    definition.NodeTool,
				Config:  map[string]any{"tool": "build_reference_registry"},
				Version: "v1",
			},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "normalize_assets", Type: definition.EdgeNormal},
			{From: "normalize_assets", To: "extract_reference_frames", Type: definition.EdgeNormal},
			{From: "extract_reference_frames", To: "build_reference_registry", Type: definition.EdgeNormal},
			{From: "build_reference_registry", To: "end", Type: definition.EdgeNormal},
		},
		Output: definition.OutputDefinition{ResultType: "registry"},
	}
}

// passthroughTool 回显 input——模拟 DreamAI 工具行为。
type passthroughTool struct{ name string }

func (t passthroughTool) Name() string                           { return t.name }
func (passthroughTool) Description() string                      { return "passthrough" }
func (passthroughTool) Mode() tool.ExecutionMode                 { return tool.SyncExecution }
func (passthroughTool) InputSchema() tool.DataSchema             { return tool.DataSchema{} }
func (passthroughTool) OutputSchema() tool.DataSchema            { return tool.DataSchema{} }
func (passthroughTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	return tool.Success(input), nil
}

// TestAdapter_CommerceAssetPrepare 验证：
//
//	DreamAI WorkflowDefinition → Compile → Flux Plan → Scheduler.Run → 全部成功。
//
// 这是 Phase 1 POC：如果这个最简单的 DAG 能在 Flux 上跑通，
// 则 Adapter 模式对全部 32 个 workflow 都成立。
func TestAdapter_CommerceAssetPrepare(t *testing.T) {
	def := commerceAssetPrepareDef()
	reg := tool.NewRegistry()
	reg.Register(passthroughTool{name: "goods_normalize_assets"})
	reg.Register(passthroughTool{name: "extract_reference_frames"})
	reg.Register(passthroughTool{name: "build_reference_registry"})

	// ── Adapter：WorkflowDefinition → Flux Plan ──
	plan, err := dreamai.Compile(def, reg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// 验证编译结果
	if len(plan.Nodes) != 3 {
		t.Fatalf("应编译出 3 个节点（排除 start/end），实际=%d", len(plan.Nodes))
	}
	// 验证依赖链：normalize_assets 无依赖，extract → normalize，build → extract
	deps := map[string][]string{}
	for name, n := range plan.Nodes {
		deps[name] = n.DependsOn
	}
	if len(deps["normalize_assets"]) != 0 {
		t.Fatalf("normalize_assets 应为根节点（无依赖），实际=%v", deps["normalize_assets"])
	}
	if deps["extract_reference_frames"][0] != "normalize_assets" {
		t.Fatalf("extract 应依赖 normalize，实际=%v", deps["extract_reference_frames"])
	}
	if deps["build_reference_registry"][0] != "extract_reference_frames" {
		t.Fatalf("build 应依赖 extract，实际=%v", deps["build_reference_registry"])
	}
	t.Logf("✅ Compile：3 个节点，依赖链正确：normalize → extract → build")

	// ── Run on Flux Scheduler ──
	invoker := planner.NewToolInvoker(reg)
	state := runtime.NewMemState(map[string]any{"product": "test-item"})
	sched := runtime.NewScheduler(invoker, planner.NopAwait{}, planner.NopStore{}, planner.NopEmitter{})

	res, err := sched.Run(context.Background(), runtime.NewStaticSource(plan), state)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != runtime.StatusCompleted {
		t.Fatalf("应完成，status=%d", res.Status)
	}

	// 验证所有节点都执行了
	for _, name := range []string{"normalize_assets", "extract_reference_frames", "build_reference_registry"} {
		if st := state.State(name); st != runtime.NodeSuccess {
			t.Fatalf("%s 应为 NodeSuccess，实际=%d", name, st)
		}
	}

	t.Logf("✅ Run：3/3 节点全部 NodeSuccess")
	t.Logf("   normalize_assets → %v", state.Output("normalize_assets"))
	t.Logf("   extract_reference_frames → %v", state.Output("extract_reference_frames"))
	t.Logf("   build_reference_registry → %v", state.Output("build_reference_registry"))
	t.Log("✅✅ Phase 1 POC 通过：DreamAI WorkflowDefinition → Adapter → Flux Scheduler → 全部成功")
}

// TestAdapter_CompilePreservesToolNames 验证编译结果保留原始工具名。
func TestAdapter_CompilePreservesToolNames(t *testing.T) {
	def := commerceAssetPrepareDef()
	reg := tool.NewRegistry()
	reg.Register(passthroughTool{name: "goods_normalize_assets"})
	reg.Register(passthroughTool{name: "extract_reference_frames"})
	reg.Register(passthroughTool{name: "build_reference_registry"})

	plan, _ := dreamai.Compile(def, reg)

	expected := map[string]string{
		"normalize_assets":         "goods_normalize_assets",
		"extract_reference_frames": "extract_reference_frames",
		"build_reference_registry": "build_reference_registry",
	}
	for nodeName, toolName := range expected {
		n, ok := plan.Nodes[nodeName]
		if !ok {
			t.Fatalf("缺少节点 %s", nodeName)
		}
		if n.ToolName != toolName {
			t.Fatalf("%s.ToolName = %q，期望 %q", nodeName, n.ToolName, toolName)
		}
	}
	t.Log("✅ 所有工具名保留正确")
}
