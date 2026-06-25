package skill

import (
	"context"
	"fmt"

	"flux/tool"
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
func (t *SaveAsSkillTool) Description() string  { return "把当前成功执行的 DAG 保存为可复用的 Skill（SKILL.md + workflow.yaml）。下次 agent 可以直接调这个 skill。" }
func (t *SaveAsSkillTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

func (t *SaveAsSkillTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{Fields: map[string]tool.FieldSchema{
		"name":         {Type: "string", Required: true, Desc: "Skill 名称（简洁、语义化，如 generate_product_video）"},
		"description":  {Type: "string", Required: true, Desc: "Skill 描述，帮助 agent 将来选它"},
		"workflow_yaml": {Type: "string", Required: false, Desc: "可选：DAG 的 YAML 定义；不传则只写 SKILL.md 无 workflow"},
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

	spec := &SkillSpec{
		Name:           name,
		Description:    desc,
		Implementation: ImplWorkflow,
		Workflow:       "workflow.yaml",
		Body:           fmt.Sprintf("## Purpose\n%s\n\nAuto-generated skill.", desc),
	}

	var wf []byte
	if y, _ := input["workflow_yaml"].(string); y != "" {
		wf = []byte(y)
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
