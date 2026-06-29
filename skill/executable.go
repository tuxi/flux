package skill

import (
	"context"
	"encoding/json"

	"github.com/tuxi/flux/tool"
)

// ExecutableSkill 是 SkillSpec 解析后的"可执行体"。
// 三种实现：ToolSkill（叶子工具）/ WorkflowSkill（引擎 DAG）/ AgentSkill（动态规划）。
//
// 对外只暴露 Definition()—— planner 永远只看 ToolDefinition，
// 不关心底层是 tool、workflow 还是 agent。
type ExecutableSkill interface {
	Definition() tool.ToolDefinition
}

// ── ToolSkill：包装一个 tool.Tool，对外以 skill 名出现 ──

// ToolSkill 包装一个现有的 tool.Tool，但以 skill 名暴露（Name() 返回 skill 名而非工具名）。
// 其余方法（Description/InputSchema/Execute/Mode）全部委托给底层工具。
// 这样 planner 看到的是 "list_files" 而不是底下的 "shell"。
type ToolSkill struct {
	SkillName string    // skill 名（来自 SKILL.md）
	Tool      tool.Tool // 底层工具
}

func (s *ToolSkill) Name() string             { return s.SkillName }
func (s *ToolSkill) Description() string       { return s.Tool.Description() }
func (s *ToolSkill) Mode() tool.ExecutionMode  { return s.Tool.Mode() }
func (s *ToolSkill) InputSchema() tool.DataSchema  { return s.Tool.InputSchema() }
func (s *ToolSkill) OutputSchema() tool.DataSchema { return s.Tool.OutputSchema() }
func (s *ToolSkill) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	return s.Tool.Execute(ctx, input, emitter)
}

func (s *ToolSkill) Definition() tool.ToolDefinition {
	def := tool.DefinitionOf(s.Tool)
	def.Name = s.SkillName
	return def
}

var _ ExecutableSkill = (*ToolSkill)(nil)
var _ tool.Tool = (*ToolSkill)(nil)

// ── WorkflowSkill：持有一个 workflow 引用（路径或 DB id），执行时再编译 ──

// WorkflowSkill 不持有已编译的 runtime.Plan，而是持引用（路径/DB id）。
// 原因：skill 是能力引用，不是已编译执行体。未来 workflow 可能来自
// DB(workflow_versions)、OSSURL、Git repo 的 workflow.yaml——引用解耦加载。
type WorkflowSkill struct {
	Def         tool.ToolDefinition
	WorkflowRef string // 路径（相对 Dir）或 DB id 等引用——执行时再编译
	Dir         string // SKILL.md 所在目录（运行时拼接完整路径）
}

func (s *WorkflowSkill) Definition() tool.ToolDefinition { return s.Def }

var _ ExecutableSkill = (*WorkflowSkill)(nil)

// ── AgentSkill：包装一个 goal → PlanSource（S4 补全）──

type AgentSkill struct {
	Def tool.ToolDefinition
	// Source runtime.PlanSource — S4 补全
}

func (s *AgentSkill) Definition() tool.ToolDefinition { return s.Def }

var _ ExecutableSkill = (*AgentSkill)(nil)

// ── 工厂函数 ──

// ToolFactory：根据 skill 引用的 tool 名查找 tool.Tool。
type ToolFactory func(toolName string) (tool.Tool, error)

// Resolver 把 SkillSpec 解析成 ExecutableSkill。
type Resolver struct {
	Tools ToolFactory
	// Agent factory 留到 S4
}

func NewResolver(tools ToolFactory) *Resolver {
	return &Resolver{Tools: tools}
}

// Resolve 把 SkillSpec 转成 ExecutableSkill。
func (r *Resolver) Resolve(spec *SkillSpec) (ExecutableSkill, error) {
	def := specToDefinition(spec)

	switch spec.Implementation {
	case ImplTool:
		t, err := r.Tools(spec.Tool)
		if err != nil {
			return nil, err
		}
		return &ToolSkill{SkillName: spec.Name, Tool: t}, nil

	case ImplWorkflow:
		return &WorkflowSkill{Def: def, WorkflowRef: spec.Workflow, Dir: spec.Dir}, nil

	case ImplAgent:
		// S4 以前：只建定义，不提供 PlanSource
		return &AgentSkill{Def: def}, nil

	default:
		// 未指定或未知 → 当做 Tool 尝试
		t, err := r.Tools(spec.Tool)
		if err != nil {
			return nil, err
		}
		return &ToolSkill{SkillName: spec.Name, Tool: t}, nil
	}
}

// specToDefinition 把 SkillSpec 的前置元数据映射为 ToolDefinition，带 InputSchema。
// planner 据此决定调该 skill 时该传什么——不再靠自然语言猜参数。
func specToDefinition(spec *SkillSpec) tool.ToolDefinition {
	return tool.ToolDefinition{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: inputsToJSONSchema(spec.Inputs),
		OutputSchema: nil,
		Annotations: tool.Annotations{},
	}
}

// inputsToJSONSchema 把 SKILL.md 的 inputs 段转成 JSON Schema（tool.ToolDefinition 兼容格式）。
func inputsToJSONSchema(inputs map[string]InputSpec) json.RawMessage {
	if len(inputs) == 0 {
		return nil
	}
	props := map[string]any{}
	var required []string
	for name, is := range inputs {
		props[name] = map[string]any{
			"type":        is.Type,
			"description": is.Description,
		}
		if is.Required {
			required = append(required, name)
		}
	}
	obj := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	b, _ := json.Marshal(obj)
	return b
}

// ── Registry：按名字存取 ExecutableSkill ──

type Registry struct {
	skills map[string]ExecutableSkill
}

func NewRegistry() *Registry {
	return &Registry{skills: map[string]ExecutableSkill{}}
}

// NewRegistryWith 从已有的 ExecutableSkill 创建 Registry（便捷方法）。
func NewRegistryWith(exe ExecutableSkill) *Registry {
	r := NewRegistry()
	r.Register(exe.Definition().Name, exe)
	return r
}

func (r *Registry) Register(name string, s ExecutableSkill) {
	r.skills[name] = s
}

func (r *Registry) Get(name string) (ExecutableSkill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

func (r *Registry) List() []ExecutableSkill {
	out := make([]ExecutableSkill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	return out
}

// LoadAndRegister 从 Loader 加载所有 skill 并经 Resolver 注册进 Registry。
// 解析失败的 skill 跳过（不阻塞其他）。
func LoadAndRegister(ctx context.Context, loader *Loader, resolver *Resolver, reg *Registry) ([]string, error) {
	specs, err := loader.List()
	if err != nil {
		return nil, err
	}
	var registered []string
	for _, spec := range specs {
		exe, err := resolver.Resolve(spec)
		if err != nil {
			continue // 解析失败跳过
		}
		reg.Register(spec.Name, exe)
		registered = append(registered, spec.Name)
	}
	return registered, nil
}
