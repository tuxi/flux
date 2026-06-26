package skill

import (
	"fmt"

	"flux/tool"
)

// RegisterAsTools 把 skill.Registry 里的所有 ExecutableSkill 转成 tool.Tool，
// 注册进 tool.Registry。planner 经 MCP tools/list 看到的就是统一菜单。
//
// S5 的唯一边界：不扩展 runtime、不在 MCP 层引入执行语义。
// Skill 内部的 Tool/Workflow/Agent 对 MCP 不可见——MCP 只看到 ToolDefinition。
func RegisterAsTools(skillReg *Registry, toolReg *tool.Registry, runner WorkflowRunner) error {
	for _, exe := range skillReg.List() {
		tl, err := AsTool(exe, runner)
		if err != nil {
			return err
		}
		toolReg.Register(tl)
	}
	return nil
}

// AsTool 把 ExecutableSkill 转成 tool.Tool。
//
//   - ToolSkill      → ToolSkill 自身就是 tool.Tool（Name() 返回 skill 名，其余委托给底层工具）
//   - WorkflowSkill  → 包成 SubWorkflowTool（内部走 WorkflowRunner）
//   - AgentSkill     → S4 前不支持执行，返回 error
func AsTool(exe ExecutableSkill, runner WorkflowRunner) (tool.Tool, error) {
	switch s := exe.(type) {
	case *ToolSkill:
		return s, nil // ToolSkill 已实现 tool.Tool
	case *WorkflowSkill:
		spec := &SkillSpec{
			Name:           s.Def.Name,
			Description:    s.Def.Description,
			Implementation: ImplWorkflow,
			Workflow:       s.WorkflowRef,
		}
		return NewSubWorkflowTool(spec, runner), nil
	case *AgentSkill:
		return nil, fmt.Errorf("skill %q: agent skills not yet executable (S4)", s.Def.Name)
	default:
		return nil, fmt.Errorf("unknown skill type: %T", exe)
	}
}
