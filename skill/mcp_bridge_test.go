package skill_test

import (
	"context"
	"testing"

	"github.com/tuxi/flux/skill"
	"github.com/tuxi/flux/tool"
	"github.com/tuxi/flux/tool/builtin"
)

func TestAsTool_ToolSkillIsDirectPassThrough(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())

	// ToolSkill 现在以 skill 名对外暴露（Name() 返回 skill 名）
	tl, err := skill.AsTool(&skill.ToolSkill{SkillName: "my_skill", Tool: builtin.NewMergeResultTool()}, nil)
	if err != nil {
		t.Fatalf("AsTool: %v", err)
	}
	if tl.Name() != "my_skill" {
		t.Fatalf("name: want my_skill, got %q", tl.Name())
	}
	// 执行仍委托给底层工具
	res, err := tl.Execute(context.Background(), map[string]any{"x": 1}, nil)
	if err != nil || !res.Success {
		t.Fatalf("execute: %v", err)
	}
}

func TestAsTool_WorkflowSkillViaRunner(t *testing.T) {
	called := false
	runner := func(_ context.Context, _ *skill.SkillSpec, _ map[string]any) (*tool.Result, error) {
		called = true
		return tool.Success(map[string]any{"ok": true}), nil
	}

	s := &skill.WorkflowSkill{
		Def:          tool.ToolDefinition{Name: "my_workflow", Description: "desc"},
		WorkflowRef:  "workflow.yaml",
	}
	tl, err := skill.AsTool(s, runner)
	if err != nil {
		t.Fatalf("AsTool: %v", err)
	}
	if tl.Name() != "my_workflow" {
		t.Fatalf("name: %q", tl.Name())
	}
	res, _ := tl.Execute(context.Background(), nil, nil)
	if !res.Success || !called {
		t.Fatalf("runner should be called")
	}
}

func TestRegisterAsTools_UnifiedMenu(t *testing.T) {
	// 工具 reg：本地 leaf
	leafReg := tool.NewRegistry()
	leafReg.Register(builtin.NewMergeResultTool())

	// skill reg：一个 WorkflowSkill
	skillReg := skill.NewRegistry()
	ws := &skill.WorkflowSkill{
		Def:         tool.ToolDefinition{Name: "generate_video", Description: "Generate videos"},
		WorkflowRef: "workflow.yaml",
	}
	skillReg.Register("generate_video", ws)

	runner := func(_ context.Context, _ *skill.SkillSpec, _ map[string]any) (*tool.Result, error) {
		return tool.Success(nil), nil
	}

	// 统一注册
	if err := skill.RegisterAsTools(skillReg, leafReg, runner); err != nil {
		t.Fatalf("RegisterAsTools: %v", err)
	}

	// 验证：leaf + skill 共处一堂
	tl, ok := leafReg.Get("merge_result")
	if !ok || tl.Name() != "merge_result" {
		t.Fatalf("leaf tool missing")
	}
	tl2, ok := leafReg.Get("generate_video")
	if !ok || tl2.Name() != "generate_video" {
		t.Fatalf("skill-based tool missing after RegisterAsTools")
	}

	// MCP server 读的就是这个 reg.List()
	all := leafReg.List()
	names := map[string]bool{}
	for _, t := range all {
		names[t.Name()] = true
	}
	if !names["merge_result"] || !names["generate_video"] {
		t.Fatalf("统一菜单应同时包含 leaf 和 skill 工具，得 %v", names)
	}

	// tool.DefinitionOf 对 Skill 工具也一样工作
	def := tool.DefinitionOf(tl2)
	if def.Name != "generate_video" {
		t.Fatalf("DefinitionOf skill-based tool: %q", def.Name)
	}
}
