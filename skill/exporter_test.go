package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"flux/skill"
)

func TestExportAndReload_RoundTrip(t *testing.T) {
	root := t.TempDir()

	spec := &skill.SkillSpec{
		Name:           "product_video_v3",
		Description:    "Generate product marketing videos — auto-generated from agent session",
		Implementation: skill.ImplWorkflow,
		Workflow:       "workflow.yaml",
		Inputs: map[string]skill.InputSpec{
			"image":       {Type: "string", Description: "Product image URL", Required: true},
			"description": {Type: "string", Description: "Product description text"},
		},
		Body: "## Purpose\nAuto-generated skill from agent session.\n",
	}

	workflowYAML := []byte(`nodes:
  - id: generate_image
    tool: generate_image
  - id: generate_video
    tool: generate_video
    depends_on: [generate_image]
`)

	// 写出
	if err := skill.Export(spec, root, workflowYAML); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// 确认目录结构
	skillDir := filepath.Join(root, "product_video_v3")
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md 未生成: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "workflow.yaml")); err != nil {
		t.Fatalf("workflow.yaml 未生成: %v", err)
	}

	// 回读
	loaded, err := skill.LoadDir(skillDir)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if loaded.Name != "product_video_v3" {
		t.Fatalf("name: want product_video_v3, got %q", loaded.Name)
	}
	if loaded.Implementation != skill.ImplWorkflow {
		t.Fatalf("impl: want workflow, got %s", loaded.Implementation)
	}
	if loaded.Workflow != "workflow.yaml" {
		t.Fatalf("workflow ref: %q", loaded.Workflow)
	}
	if loaded.Inputs["image"].Required != true {
		t.Fatal("image should be required")
	}
	if len(loaded.Body) == 0 {
		t.Fatal("body should survive round-trip")
	}
}

func TestExport_WithoutWorkflow(t *testing.T) {
	root := t.TempDir()

	spec := &skill.SkillSpec{
		Name:           "greet",
		Implementation: skill.ImplTool,
		Tool:           "echo",
	}

	if err := skill.Export(spec, root, nil); err != nil {
		t.Fatalf("Export: %v", err)
	}

	skillDir := filepath.Join(root, "greet")
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "workflow.yaml")); err == nil {
		t.Fatal("workflow.yaml should NOT be created when workflow is nil")
	}

	// 回读
	loaded, _ := skill.LoadDir(skillDir)
	if loaded.Tool != "echo" {
		t.Fatalf("tool: want echo, got %q", loaded.Tool)
	}
}

func TestRenderSKILLMD(t *testing.T) {
	spec := &skill.SkillSpec{
		Name:        "hello",
		Description: "Say hello",
		Inputs: map[string]skill.InputSpec{
			"name": {Type: "string", Required: true},
		},
		Body: "## Body\nHello world.\n",
	}
	b, err := skill.RenderSKILLMD(spec)
	if err != nil {
		t.Fatalf("RenderSKILLMD: %v", err)
	}
	s := string(b)
	if !contains(s, "name: hello") || !contains(s, "## Body") {
		t.Fatalf("Rendered content incomplete:\n%s", s)
	}
	// 写回文件再 Load 验证
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SKILL.md"), b, 0o644)
	loaded, _ := skill.LoadDir(dir)
	if loaded.Name != "hello" {
		t.Fatalf("round-trip name: %q", loaded.Name)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
