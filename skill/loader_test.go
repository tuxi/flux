package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tuxi/flux/skill"
)

func writeSkill(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDir_WorkflowSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, `---
name: generate_video
description: Generate product marketing videos
implementation: workflow
workflow: workflow.yaml
---
# Generate Video
## Purpose
Generate product marketing videos.
`)

	spec, err := skill.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if spec.Name != "generate_video" {
		t.Fatalf("name: want generate_video, got %q", spec.Name)
	}
	if spec.Description != "Generate product marketing videos" {
		t.Fatalf("desc mismatch: %q", spec.Description)
	}
	if spec.Implementation != skill.ImplWorkflow {
		t.Fatalf("impl: want workflow, got %s", spec.Implementation)
	}
	if spec.Workflow != "workflow.yaml" {
		t.Fatalf("workflow: want workflow.yaml, got %q", spec.Workflow)
	}
	if spec.Dir != dir {
		t.Fatalf("dir mismatch: %q", spec.Dir)
	}
	if len(spec.Body) == 0 {
		t.Fatal("body should not be empty")
	}
}

func TestLoadDir_ToolSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, `---
name: web_search
description: Search the web
implementation: tool
tool: web_search_tool
---
Search tool.
`)

	spec, _ := skill.LoadDir(dir)
	if spec.Implementation != skill.ImplTool {
		t.Fatalf("impl: want tool, got %s", spec.Implementation)
	}
	if spec.Tool != "web_search_tool" {
		t.Fatalf("tool: got %q", spec.Tool)
	}
}

func TestLoadDir_AgentSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, `---
name: code_fix
description: Fix compile errors
implementation: agent
goal: fix compile errors and make tests pass
---
Agent skill.
`)

	spec, _ := skill.LoadDir(dir)
	if spec.Implementation != skill.ImplAgent {
		t.Fatalf("impl: want agent, got %s", spec.Implementation)
	}
	if spec.Goal != "fix compile errors and make tests pass" {
		t.Fatalf("goal: got %q", spec.Goal)
	}
}

func TestLoadDir_Defaults(t *testing.T) {
	// 只给 description，其余全用默认值
	dir := t.TempDir()
	writeSkill(t, dir, `---
description: Default test
---
Body.
`)

	spec, _ := skill.LoadDir(dir)
	if spec.Name != dir { // 目录名兜底
		t.Fatalf("name default: want dir, got %q", spec.Name)
	}
	if spec.Implementation != skill.ImplTool { // 默认 tool
		t.Fatalf("impl default: want tool, got %s", spec.Implementation)
	}
}

func TestLoadDir_MissingFile(t *testing.T) {
	dir := t.TempDir() // 空目录，没有 SKILL.md
	_, err := skill.LoadDir(dir)
	if err == nil {
		t.Fatal("expected error for missing SKILL.md")
	}
}

func TestLoader_ListAndVisit(t *testing.T) {
	root := t.TempDir()

	// 建两个 skill 目录
	a := filepath.Join(root, "generate_video")
	os.MkdirAll(a, 0o755)
	writeSkill(t, a, `---
name: generate_video
description: videos
implementation: workflow
workflow: workflow.yaml
---
`)

	b := filepath.Join(root, "code_fix")
	os.MkdirAll(b, 0o755)
	writeSkill(t, b, `---
name: code_fix
description: fix
implementation: agent
goal: fix
---
`)

	// stray 子目录（不是 skill）
	os.MkdirAll(filepath.Join(root, "not_a_skill"), 0o755)

	l := skill.NewLoader(root)
	specs, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("want 2 skills, got %d", len(specs))
	}

	// Visit
	var visited []string
	if err := l.Visit(func(s *skill.SkillSpec) error {
		visited = append(visited, s.Name)
		return nil
	}); err != nil {
		t.Fatalf("Visit: %v", err)
	}
	if len(visited) != 2 {
		t.Fatalf("Visit: want 2, got %d", len(visited))
	}
}

func TestLoader_LoadPrioritizesRoots(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()

	// root1 有一个同名 skill
	os.MkdirAll(filepath.Join(root1, "greet"), 0o755)
	writeSkill(t, filepath.Join(root1, "greet"), `---
name: greet_root1
description: from root1
---
`)

	// root2 也有同名的——应该命中 root1（优先级高）
	os.MkdirAll(filepath.Join(root2, "greet"), 0o755)
	writeSkill(t, filepath.Join(root2, "greet"), `---
name: greet_root2
description: from root2
---
`)

	l := skill.NewLoader(root1, root2)
	spec, err := l.Load("greet")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if spec.Name != "greet_root1" {
		t.Fatalf("want root1 (higher prio), got %q", spec.Name)
	}
}

func TestLoader_LoadNotFound(t *testing.T) {
	l := skill.NewLoader(t.TempDir())
	_, err := l.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}
