package skill

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Export 把 SkillSpec 写出为 SKILL.md（+ 可选 workflow.yaml）到 skillDir/<spec.Name>/ 下。
// workflow 是可选的工作流定义（YAML/JSON 字节），不为空时写为 workflow.yaml。
//
// 这是动态 DAG → 固化 Skill 闭环的落点：
//
//	agent 跑通后调用 Export(spec, dir, workflow) → 下次直接 Load("generate_video")。
func Export(spec *SkillSpec, skillDir string, workflow []byte) error {
	dir := filepath.Join(skillDir, spec.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// 写 SKILL.md
	b, err := RenderSKILLMD(spec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), b, 0o644); err != nil {
		return err
	}

	// 可选 workflow.yaml
	if len(workflow) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), workflow, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// RenderSKILLMD 把 SkillSpec 渲染成 SKILL.md 字节（YAML frontmatter + markdown body）。
func RenderSKILLMD(spec *SkillSpec) ([]byte, error) {
	var b strings.Builder

	// YAML frontmatter
	fm, err := yaml.Marshal(spec)
	if err != nil {
		return nil, err
	}
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")

	// markdown body
	if spec.Body != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(spec.Body))
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}
