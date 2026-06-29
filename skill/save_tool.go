package skill

import (
	"context"
	"fmt"

	"github.com/tuxi/flux/tool"
)

// SaveAsSkillTool 是一个 tool.Tool——agent 在跑通一个动态 DAG 后调用它，
// 把 DAG + 元数据保存为 skills/<name>/SKILL.md + workflow.yaml。
//
// 这是 Skill 进化闭环的触发点：
//
//	agent 执行成功 → 调用 save_as_skill → Export → Loader → Resolver → RegisterAsTools → 下次直接用。
//
// 参数：name（skill 名）、description、workflow_yaml（DAG 的 YAML 定义）。
type SaveAsSkillTool struct {
	SkillDir    string             // skills/ 的根目录
	OnSave      func(name string)  // 可选：保存成功后的回调（比如重新注册到 registry）
}

func NewSaveAsSkillTool(skillDir string) *SaveAsSkillTool {
	return &SaveAsSkillTool{SkillDir: skillDir}
}

func (t *SaveAsSkillTool) Name() string        { return "save_as_skill" }
func (t *SaveAsSkillTool) Description() string {
	return "把当前成功执行的操作保存为可复用的 Skill。若提供 workflow_yaml 则存为 workflow 型，否则存为 tool 型（需指定 tool_name，如 shell）。下次 agent 可以直接调这个 skill。"
}
func (t *SaveAsSkillTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func (t *SaveAsSkillTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"name":         {Type: "string", Required: true, Desc: "Skill 名称（简洁、语义化，如 list_dir）"},
		"description":  {Type: "string", Required: true, Desc: "Skill 描述，帮助 agent 将来选它"},
		"workflow_yaml": {Type: "string", Required: false, Desc: "可选：DAG 的 YAML 定义。提供则存为 workflow 型"},
		"tool_name":    {Type: "string", Required: false, Desc: "workflow_yaml 为空时必填：底层工具名（如 shell、grep）。Skill 将包装该工具"},
	}}
}

func (t *SaveAsSkillTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"path":  {Type: "string", Desc: "保存的 SKILL.md 绝对路径"},
		"name":  {Type: "string", Desc: "保存后的 skill 名称"},
	}}
}

func (t *SaveAsSkillTool) Execute(_ context.Context, input map[string]any, _ tool.ToolEmitter) (*tool.Result, error) {
	name, _ := input["name"].(string)
	desc, _ := input["description"].(string)
	if name == "" {
		return tool.Fail(fmt.Errorf("save_as_skill: name 不能为空")), nil
	}

	wfYAML, _ := input["workflow_yaml"].(string)
	toolName, _ := input["tool_name"].(string)

	var (
		spec *SkillSpec
		wf   []byte
	)

	if wfYAML != "" {
		// 有 DAG → workflow 型
		spec = &SkillSpec{
			Name:           name,
			Description:    desc,
			Implementation: ImplWorkflow,
			Workflow:       "workflow.yaml",
			Body:           fmt.Sprintf("## Purpose\n%s\n\nAuto-generated skill.", desc),
		}
		wf = []byte(wfYAML)
	} else {
		// 无 DAG → tool 型（包装底层工具）
		if toolName == "" {
			toolName = "shell" // 默认：大多数简单操作都是 shell
		}
		spec = &SkillSpec{
			Name:           name,
			Description:    desc,
			Implementation: ImplTool,
			Tool:           toolName,
			Body:           fmt.Sprintf("## Purpose\n%s\n\nWraps the `%s` tool.\n\nAuto-generated skill.", desc, toolName),
		}
	}

	if err := Export(spec, t.SkillDir, wf); err != nil {
		return tool.Fail(fmt.Errorf("save_as_skill: export failed: %w", err)), nil
	}

	if t.OnSave != nil {
		t.OnSave(name)
	}

	return tool.Success(map[string]any{
		"path": t.SkillDir + "/" + name + "/SKILL.md",
		"name": name,
	}), nil
}

// 确保是 tool.Tool
var _ tool.Tool = (*SaveAsSkillTool)(nil)
