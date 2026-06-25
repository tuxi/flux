// Package skill 把 SKILL.md 文件解析成 Flux 可执行的 SkillSpec。
//
// 不自己发明格式——直接采用 Claude Code / Codex 的 SKILL.md 跨平台标准。
// Flux 的附加值：在 implementation 字段背后提供三种执行器（Tool / Workflow / Agent）。

package skill

// Implementation 区分 SKILL.md 的底层执行方式。
type Implementation string

const (
	ImplTool     Implementation = "tool"
	ImplWorkflow Implementation = "workflow"
	ImplAgent    Implementation = "agent"
)

// InputSpec 定义一个输入参数（从 SKILL.md frontmatter 的 inputs 段解析）。
type InputSpec struct {
	Type        string `yaml:"type"`
	Description string `yaml:"description,omitempty"`
	Required    bool   `yaml:"required,omitempty"`
}

// SkillSpec 是 SKILL.md 解析后的结构化表示。
// 前置元数据（YAML frontmatter）→ 前置字段；正文 → Body。
type SkillSpec struct {
	Name           string         `yaml:"name"`
	Description    string         `yaml:"description"`
	Implementation Implementation `yaml:"implementation"`
	// 实现专项字段
	Tool     string `yaml:"tool,omitempty"`     // ImplTool
	Workflow string `yaml:"workflow,omitempty"` // ImplWorkflow: workflow.yaml 路径（相对 skill 目录）
	Goal     string `yaml:"goal,omitempty"`     // ImplAgent

	// Inputs 声明此 skill 的输入参数（JSON Schema properties）。
	// Planner 据此决定调该 skill 时该传什么——不再靠自然语言猜。
	Inputs map[string]InputSpec `yaml:"inputs,omitempty"`

	// Dir 是 SKILL.md 所在目录的绝对路径，由 Loader 填充。
	Dir string `yaml:"-"`

	// Body 是 frontmatter 后面的 Markdown 正文（Agent 阅读用）。
	Body string `yaml:"-"`
}

// DefaultSpec 为缺失的必填字段填默认值。
func (s *SkillSpec) DefaultSpec() {
	if s.Name == "" {
		s.Name = s.Dir // 目录名作为 name 的兜底
	}
	if s.Implementation == "" {
		s.Implementation = ImplTool
	}
}
