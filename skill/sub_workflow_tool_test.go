package skill_test

import (
	"context"
	"testing"

	"flux/skill"
	"flux/tool"
)

func TestSubWorkflowTool_IsATool(t *testing.T) {
	// 模拟一个 runner：简单回显 input
	runner := func(_ context.Context, _ *skill.SkillSpec, input map[string]any) (*tool.Result, error) {
		return tool.Success(map[string]any{"ran": true, "input": len(input)}), nil
	}

	spec := &skill.SkillSpec{
		Name:           "generate_video",
		Description:    "Generate product marketing videos",
		Implementation: skill.ImplWorkflow,
	}
	tl := skill.NewSubWorkflowTool(spec, runner)

	// 1) 证实它满足 tool.Tool 接口
	var _ tool.Tool = tl

	if tl.Name() != "generate_video" {
		t.Fatalf("name mismatch: %q", tl.Name())
	}
	if tl.Description() != "Generate product marketing videos" {
		t.Fatalf("desc mismatch: %q", tl.Description())
	}
	if tl.Mode() != tool.SyncExecution {
		t.Fatalf("mode: want sync, got %s", tl.Mode())
	}

	// 2) Execute 走 runner 路径
	res, err := tl.Execute(context.Background(), map[string]any{}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("should succeed: %s", res.Error)
	}
	if res.Data["ran"] != true {
		t.Fatalf("runner not called: %v", res.Data)
	}

	// 3) 没有 runner 时应失败
	tlNoop := skill.NewSubWorkflowTool(spec, nil)
	res2, _ := tlNoop.Execute(context.Background(), nil, nil)
	if res2.Success {
		t.Fatal("should fail without runner")
	}
}

func TestSubWorkflowTool_DefinitionOf(t *testing.T) {
	spec := &skill.SkillSpec{
		Name:           "greet_workflow",
		Description:    "Greeting workflow",
		Implementation: skill.ImplWorkflow,
	}
	runner := func(_ context.Context, _ *skill.SkillSpec, _ map[string]any) (*tool.Result, error) {
		return tool.Success(nil), nil
	}
	tl := skill.NewSubWorkflowTool(spec, runner)

	// tool.DefinitionOf 能正确处理它（用 DataSchema 合成 JSON Schema）
	def := tool.DefinitionOf(tl)
	if def.Name != "greet_workflow" {
		t.Fatalf("DefinitionOf name: %q", def.Name)
	}
	if def.Description != "Greeting workflow" {
		t.Fatalf("DefinitionOf desc: %q", def.Description)
	}
}
