package skill_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"flux/skill"
	"flux/tool"
	"flux/tool/builtin"
)

func testToolFactory(t *testing.T) skill.ToolFactory {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())
	return func(name string) (tool.Tool, error) {
		tl, ok := reg.Get(name)
		if !ok {
			return nil, fmt.Errorf("tool not found: %s", name)
		}
		return tl, nil
	}
}

func TestResolve_ToolSkill(t *testing.T) {
	r := skill.NewResolver(testToolFactory(t))
	s := &skill.SkillSpec{
		Name:           "merge",
		Description:    "merge results",
		Implementation: skill.ImplTool,
		Tool:           "merge_result",
	}
	exe, err := r.Resolve(s)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ts, ok := exe.(*skill.ToolSkill)
	if !ok {
		t.Fatalf("want *ToolSkill, got %T", exe)
	}
	def := ts.Definition()
	if def.Name != "merge" {
		t.Fatalf("name: want merge (skill name), got %q", def.Name)
	}
	// 委托给底层工具，Execute 应成功
	res, err := ts.Execute(context.Background(), map[string]any{"x": 1}, nil)
	if err != nil || !res.Success {
		t.Fatalf("ToolSkill 应能委托执行给底层工具: %v", err)
	}
}

func TestResolve_WorkflowSkill(t *testing.T) {
	r := skill.NewResolver(nil)
	s := &skill.SkillSpec{
		Name:           "generate_video",
		Description:    "Generate videos",
		Implementation: skill.ImplWorkflow,
		Workflow:       "workflow.yaml",
		Inputs: map[string]skill.InputSpec{
			"image":        {Type: "string", Description: "Product image URL", Required: true},
			"product_desc": {Type: "string", Description: "Product description"},
		},
	}
	exe, err := r.Resolve(s)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	ws, ok := exe.(*skill.WorkflowSkill)
	if !ok {
		t.Fatalf("want *WorkflowSkill, got %T", exe)
	}
	if ws.Definition().Name != "generate_video" {
		t.Fatalf("name mismatch: %q", ws.Definition().Name)
	}
	if ws.WorkflowRef != "workflow.yaml" {
		t.Fatalf("WorkflowRef: want workflow.yaml, got %q", ws.WorkflowRef)
	}

	// InputSchema 应带进 Definition
	schema := ws.Definition().InputSchema
	if len(schema) == 0 {
		t.Fatal("InputSchema 不应为空")
	}
	var parsed struct {
		Properties map[string]struct{ Type string } `json:"properties"`
		Required   []string                         `json:"required"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("InputSchema 非法 JSON: %v", err)
	}
	if parsed.Properties["image"].Type != "string" {
		t.Fatalf("image.type: want string, got %s", parsed.Properties["image"].Type)
	}
	if len(parsed.Required) != 1 || parsed.Required[0] != "image" {
		t.Fatalf("required: want [image], got %v", parsed.Required)
	}
}

func TestResolve_AgentSkill(t *testing.T) {
	r := skill.NewResolver(nil)
	s := &skill.SkillSpec{
		Name:           "code_fix",
		Description:    "Fix code",
		Implementation: skill.ImplAgent,
	}
	exe, err := r.Resolve(s)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	as, ok := exe.(*skill.AgentSkill)
	if !ok {
		t.Fatalf("want *AgentSkill, got %T", exe)
	}
	if as.Definition().Name != "code_fix" {
		t.Fatalf("name mismatch: %q", as.Definition().Name)
	}
}

func TestRegistry(t *testing.T) {
	r := skill.NewResolver(testToolFactory(t))
	reg := skill.NewRegistry()

	s := &skill.SkillSpec{
		Name:           "merge",
		Implementation: skill.ImplTool,
		Tool:           "merge_result",
	}
	exe, _ := r.Resolve(s)
	reg.Register(s.Name, exe)

	got, ok := reg.Get("merge")
	if !ok {
		t.Fatal("should find merge in registry")
	}
	if len(reg.List()) != 1 {
		t.Fatalf("List: want 1, got %d", len(reg.List()))
	}
	_, notFound := reg.Get("nonexistent")
	if notFound {
		t.Fatal("Get('nonexistent') should return false")
	}
	_ = got
}

func TestLoadAndRegister(t *testing.T) {
	root := t.TempDir()
	dir := root + "/greet"
	os.MkdirAll(dir, 0o755)
	writeSkill(t, dir, `---
name: greeter
implementation: tool
tool: merge_result
inputs:
  name:
    type: string
    description: person name
    required: true
---
`)
	loader := skill.NewLoader(root)
	r := skill.NewResolver(testToolFactory(t))
	reg := skill.NewRegistry()

	names, err := skill.LoadAndRegister(context.Background(), loader, r, reg)
	if err != nil {
		t.Fatalf("LoadAndRegister: %v", err)
	}
	if len(names) != 1 || names[0] != "greeter" {
		t.Fatalf("registered: %v", names)
	}
	exe, ok := reg.Get("greeter")
	if !ok {
		t.Fatal("greeter should be registered")
	}
	// InputSchema 从 SKILL.md inputs 段来
	if len(exe.Definition().InputSchema) == 0 {
		t.Fatal("InputSchema 应来自 SKILL.md inputs 段")
	}
}
