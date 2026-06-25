package skill_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"flux/skill"
	"flux/tool"
)

func TestEvolutionLoop_SaveReloadAndUse(t *testing.T) {
	skillDir := t.TempDir()

	// ── 阶段 1：agent 调用 save_as_skill ──
	saveTool := skill.NewSaveAsSkillTool(skillDir)
	res, err := saveTool.Execute(context.Background(), map[string]any{
		"name":         "product_video_v3",
		"description":  "Generate product videos from image + description",
		"workflow_yaml": "nodes:\n  - id: gen\n    tool: generate_video\n",
	}, nil)
	if err != nil || !res.Success {
		t.Fatalf("save_as_skill: %v %v", err, res)
	}

	// 确认文件落地
	skillPath := filepath.Join(skillDir, "product_video_v3", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("SKILL.md 未落盘: %v", err)
	}
	wfPath := filepath.Join(skillDir, "product_video_v3", "workflow.yaml")
	if _, err := os.Stat(wfPath); err != nil {
		t.Fatalf("workflow.yaml 未落盘: %v", err)
	}

	// ── 阶段 2：系统自动加载为新 Skill ──
	loader := skill.NewLoader(skillDir)
	spec, err := loader.Load("product_video_v3")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if spec.Workflow != "workflow.yaml" {
		t.Fatalf("Workflow ref: %q", spec.Workflow)
	}

	resolver := skill.NewResolver(nil)
	exe, err := resolver.Resolve(spec)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// ── 阶段 3：注册为工具，agent 下次可见 ──
	reg := tool.NewRegistry()
	runner := func(_ context.Context, _ *skill.SkillSpec, _ map[string]any) (*tool.Result, error) {
		return tool.Success(map[string]any{"ran": "product_video_v3"}), nil
	}
	if err := skill.RegisterAsTools(skill.NewRegistryWith(exe), reg, runner); err != nil {
		t.Fatalf("RegisterAsTools: %v", err)
	}

	tl, ok := reg.Get("product_video_v3")
	if !ok {
		t.Fatal("product_video_v3 未在工具 reg 中")
	}
	execRes, _ := tl.Execute(context.Background(), nil, nil)
	if execRes.Data["ran"] != "product_video_v3" {
		t.Fatalf("Skill 未被正确执行: %v", execRes)
	}

	t.Log("✅✅ S6 进化闭环：agent → save_as_skill → Export → Load → Resolve → RegisterAsTools → agent 再调用")
}

func TestEvolutionLoop_OnSaveCallback(t *testing.T) {
	skillDir := t.TempDir()
	var savedName string
	saveTool := skill.NewSaveAsSkillTool(skillDir)
	saveTool.OnSave = func(name string) { savedName = name }

	_, err := saveTool.Execute(context.Background(), map[string]any{
		"name":        "code_review",
		"description": "Review code",
	}, nil)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if savedName != "code_review" {
		t.Fatalf("OnSave callback: got %q", savedName)
	}
	// OnSave 后可重新 LoadAndRegister
	loader := skill.NewLoader(skillDir)
	resolver := skill.NewResolver(nil)
	reg := skill.NewRegistry()
	names, _ := skill.LoadAndRegister(context.Background(), loader, resolver, reg)
	if len(names) != 1 || names[0] != "code_review" {
		t.Fatalf("OnSave 后 LoadAndRegister: %v", names)
	}
}
