package demo

import (
	"context"
	"flux/tool"
)

type ChoicePathResultTool struct{}

func NewChoicePathResultTool() *ChoicePathResultTool {
	return &ChoicePathResultTool{}
}

func (t *ChoicePathResultTool) Name() string {
	return "choice_path_result"
}

func (t *ChoicePathResultTool) Description() string {
	return "Builds a deterministic result for a chosen demo branch"
}

func (t *ChoicePathResultTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"path": {
				Type:     "string",
				Required: true,
			},
			"decision_label": {
				Type:     "string",
				Required: true,
			},
			"choice": {
				Type:     "string",
				Required: true,
			},
			"comment": {
				Type:     "string",
				Required: false,
			},
		},
	}
}

func (t *ChoicePathResultTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"selected_path": {
				Type: "string",
			},
			"decision_label": {
				Type: "string",
			},
			"choice": {
				Type: "string",
			},
			"comment": {
				Type: "string",
			},
			"summary": {
				Type: "string",
			},
		},
	}
}

func (t *ChoicePathResultTool) Execute(ctx context.Context, input map[string]any, emitter tool.ToolEmitter) (*tool.Result, error) {
	path, _ := input["path"].(string)
	decisionLabel, _ := input["decision_label"].(string)
	choice, _ := input["choice"].(string)
	comment, _ := input["comment"].(string)

	return tool.Success(map[string]any{
		"selected_path":  path,
		"decision_label": decisionLabel,
		"choice":         choice,
		"comment":        comment,
		"summary":        decisionLabel + ":" + choice,
	}), nil
}

func (t *ChoicePathResultTool) Mode() tool.ExecutionMode {
	return tool.SyncExecution
}
