package skill

import (
	"context"
	"fmt"

	"flux/tool"
)

// SubWorkflowTool 把 WorkflowSkill 包装成 tool.Tool。
// planner 调它时，内部走 engine.Run(workflow) 创建子 task —— 但 planner 不知道这一点，
// 它只看 ToolDefinition。
//
// Execute 留一个 WorkflowRunner 注入点：S2b 验证接口一致性；
// 真实引擎挂接留给 S3（B 方向 engine + await）。

// WorkflowRunner 是 workflow 执行的注入点（S2b → 回调，S3 → 真实 engine）。
type WorkflowRunner func(ctx context.Context, spec *SkillSpec, input map[string]any) (*tool.Result, error)

// NewSubWorkflowTool 把 WorkflowSkill 包成 tool.Tool。
// def 提供 InputSchema（来自 SKILL.md），让 planner 知道调这个 skill 该传什么参数。
func NewSubWorkflowTool(spec *SkillSpec, def tool.ToolDefinition, runner WorkflowRunner) tool.Tool {
	return &subWorkflowTool{spec: spec, def: def, runner: runner}
}

type subWorkflowTool struct {
	spec   *SkillSpec
	def    tool.ToolDefinition // 完整的定义（含 InputSchema），暴露给 planner
	runner WorkflowRunner
}

func (t *subWorkflowTool) Name() string            { return t.spec.Name }
func (t *subWorkflowTool) Description() string      { return t.spec.Description }
func (t *subWorkflowTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func (t *subWorkflowTool) InputSchema() tool.DataSchema  { return tool.DataSchema{} }
func (t *subWorkflowTool) OutputSchema() tool.DataSchema { return tool.DataSchema{} }

// Definition 提供完整 ToolDefinition（含 JSON Schema InputSchema），
// 绕过 DataSchema 的浅层限制，让 planner 看到精确的参数定义。
func (t *subWorkflowTool) Definition() tool.ToolDefinition { return t.def }

func (t *subWorkflowTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	if t.runner == nil {
		return tool.Fail(fmt.Errorf("workflow skill %q: no runner configured", t.spec.Name)), nil
	}
	return t.runner(ctx, t.spec, input)
}

// 确保实现 tool.Tool 和 tool.DefinedTool
var _ tool.Tool = (*subWorkflowTool)(nil)
var _ tool.DefinedTool = (*subWorkflowTool)(nil)
