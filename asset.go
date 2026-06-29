package flux

import (
	"github.com/tuxi/flux/definition"
	"github.com/tuxi/flux/skill"
	"github.com/tuxi/flux/tool"
)

// Asset 是可以被 Engine 执行的能力单元。
// 不关心底层是 workflow、tool 还是 skill——对 Engine 都是"一个可以 Run 的东西"。
type Asset interface {
	name() string
}

// ── Workflow Asset ──

type workflowAsset struct{ def *definition.WorkflowDefinition }
func (a *workflowAsset) name() string { return a.def.Name }

// Workflow 包装一个 WorkflowDefinition 为 Asset。
// Engine 内部走 Compile → Plan → StaticSource → Scheduler。
func Workflow(def *definition.WorkflowDefinition) Asset {
	return &workflowAsset{def: def}
}

// ── Tool Asset ──

type toolAsset struct{ tool tool.Tool }
func (a *toolAsset) name() string { return a.tool.Name() }

// Tool 包装一个 tool.Tool 为 Asset。
// 作为叶子节点被 workflow 或 planner 调用。
func Tool(t tool.Tool) Asset {
	return &toolAsset{tool: t}
}

// ── Skill Asset ──

type skillAsset struct{ spec *skill.SkillSpec }
func (a *skillAsset) name() string { return a.spec.Name }

// Skill 包装一个 SKILL.md 为 Asset。
// Skill 在 Engine 内部展开为 Workflow 或 Tool，对调用方透明。
func Skill(spec *skill.SkillSpec) Asset {
	return &skillAsset{spec: spec}
}
