package skill

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader 从目录加载 SKILL.md 文件，产出 SkillSpec。
type Loader struct {
	// Roots 是搜索 skill 的根目录列表，按优先级排列。
	// 典型值：{projectSkills, userSkills}。
	Roots []string
}

// NewLoader 创建 Loader。roots 按优先级排列（前 > 后）。
func NewLoader(roots ...string) *Loader { return &Loader{Roots: roots} }

// Load 在 roots 中按优先级查找名为 name 的 skill 并解析。
func (l *Loader) Load(name string) (*SkillSpec, error) {
	for _, root := range l.Roots {
		dir := filepath.Join(root, name)
		spec, err := LoadDir(dir)
		if err == nil {
			return spec, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("skill %q not found in %v", name, l.Roots)
}

// List 列出所有 roots 中的 skill 目录（含有 SKILL.md 的目录）。
func (l *Loader) List() ([]*SkillSpec, error) {
	var specs []*SkillSpec
	seen := map[string]bool{}
	for _, root := range l.Roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // root 不存在则跳过
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			dir := filepath.Join(root, e.Name())
			spec, err := LoadDir(dir)
			if err != nil {
				continue // 没有有效 SKILL.md 的目录跳过
			}
			seen[e.Name()] = true
			specs = append(specs, spec)
		}
	}
	return specs, nil
}

// LoadDir 解析指定目录下的 SKILL.md。
func LoadDir(dir string) (*SkillSpec, error) {
	path := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(dir, data)
}

// Parse 解析单个 SKILL.md 的完整内容。
func Parse(dir string, content []byte) (*SkillSpec, error) {
	fm, body, err := splitFrontMatter(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "SKILL.md"), err)
	}
	var spec SkillSpec
	if err := yaml.Unmarshal(fm, &spec); err != nil {
		return nil, fmt.Errorf("%s: yaml: %w", filepath.Join(dir, "SKILL.md"), err)
	}
	spec.Dir = dir
	spec.Body = string(bytes.TrimSpace(body))
	spec.DefaultSpec()
	return &spec, nil
}

// splitFrontMatter 把 SKILL.md 内容切为 YAML 前置元数据 + Markdown 正文。
// 前置元数据以 --- 开头和结尾。
func splitFrontMatter(content []byte) ([]byte, []byte, error) {
	sc := bufio.NewScanner(bytes.NewReader(content))
	// 第一行必须是 ---
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return nil, content, fmt.Errorf("frontmatter must start with ---")
	}
	var yamlLines []string
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "---" {
			break
		}
		yamlLines = append(yamlLines, sc.Text())
	}
	// 收集剩余 Markdown 正文
	var bodyLines []string
	for sc.Scan() {
		bodyLines = append(bodyLines, sc.Text())
	}
	return []byte(strings.Join(yamlLines, "\n")), []byte(strings.Join(bodyLines, "\n")), sc.Err()
}

// Visit 遍历所有 roots，对每个找到的 skill 调用 fn。
func (l *Loader) Visit(fn func(*SkillSpec) error) error {
	specs, err := l.List()
	if err != nil {
		return err
	}
	for _, s := range specs {
		if err := fn(s); err != nil {
			return err
		}
	}
	return nil
}

// ── 工具函数 ──

// DefaultRoots 返回 Flux 的默认 skill 搜索路径。
func DefaultRoots() []string {
	home, _ := os.UserHomeDir()
	return []string{
		"./skills",                                // 项目级
		filepath.Join(home, ".flux", "skills"),    // 用户级
	}
}

// WalkSkills 遍历目录，找出所有包含 SKILL.md 的子目录。
func WalkSkills(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 权限等跳过
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		dirs = append(dirs, filepath.Dir(path))
		return filepath.SkipDir
	})
	return dirs, err
}
