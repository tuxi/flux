package mcp_test

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"

	"flux/mcp"
	"flux/skill"
	"flux/tool"
	"flux/tool/builtin"
)

func TestMCPServer_ExposesSkillsInToolsList(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(builtin.NewMergeResultTool())

	skillReg := skill.NewRegistry()
	ws := &skill.WorkflowSkill{
		Def:         tool.ToolDefinition{Name: "generate_video", Description: "Generate videos"},
		WorkflowRef: "workflow.yaml",
	}
	skillReg.Register("generate_video", ws)

	runner := func(_ context.Context, _ *skill.SkillSpec, _ map[string]any) (*tool.Result, error) {
		return tool.Success(nil), nil
	}
	if err := skill.RegisterAsTools(skillReg, reg, runner); err != nil {
		t.Fatalf("RegisterAsTools: %v", err)
	}

	srv := mcp.NewServer(reg)
	csR, csW := io.Pipe()
	scR, scW := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serveDone := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, csR, scW)
		close(serveDone)
	}()

	pt := &pipeTransport{w: csW, r: bufio.NewReader(scR)}
	client, err := mcp.NewClient(ctx, pt)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	// 关 csW 让 csR 得 EOF → server.Serve 退出；保证 scW 在客户端读完前不关
	defer func() { _ = csW.Close(); <-serveDone }()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	var foundMerge, foundSkill bool
	for _, tl := range tools {
		if tl.Name == "merge_result" {
			foundMerge = true
		}
		if tl.Name == "generate_video" {
			foundSkill = true
		}
	}
	if !foundMerge {
		t.Fatal("tools/list 应包含本地工具 merge_result")
	}
	if !foundSkill {
		t.Fatalf("tools/list 应包含 skill 工具 generate_video，实际: %d 工具", len(tools))
	}
	t.Logf("✅ S5 集成证明：MCP tools/list 统一返回本地 + Skill 工具（共 %d 个）", len(tools))
}
