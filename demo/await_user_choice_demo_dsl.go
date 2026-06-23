package demo

import "flux/definition"

func AwaitUserChoiceDemoWorkflow() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		Name: "await_user_choice_demo",
		Desc: "Demo workflow for signal-based await and conditional branching",
		Output: definition.OutputDefinition{
			ResultType: "choice_demo",
			Extras: map[string]string{
				"choice":         "nodes.user_choice.output.choice",
				"comment":        "nodes.user_choice.output.comment",
				"operator_id":    "nodes.user_choice.output.operator_id",
				"callback_token": "input.callback_token",
			},
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{
				Name: "user_choice",
				Type: definition.NodeAwait,
				Config: map[string]any{
					"await_type":          "user_input",
					"source":              "signal",
					"signal_name":         "await_user_choice_demo_signal",
					"callback_token_expr": "input.callback_token",
				},
			},
			{
				Name: "approved_path",
				Type: definition.NodeTool,
				Config: map[string]any{
					"tool": "choice_path_result",
				},
				InputMapping: map[string]string{
					"path":           "'approved_path'",
					"decision_label": "'approved_publish'",
					"choice":         "user_choice.choice",
					"comment":        "user_choice.comment",
				},
			},
			{
				Name: "revise_path",
				Type: definition.NodeTool,
				Config: map[string]any{
					"tool": "choice_path_result",
				},
				InputMapping: map[string]string{
					"path":           "'revise_path'",
					"decision_label": "'needs_revision'",
					"choice":         "user_choice.choice",
					"comment":        "user_choice.comment",
				},
			},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "user_choice", Type: definition.EdgeNormal},
			{From: "user_choice", To: "approved_path", Type: definition.EdgeCondition, Condition: "user_choice.choice == 'approve'"},
			{From: "user_choice", To: "revise_path", Type: definition.EdgeCondition, Condition: "user_choice.choice == 'revise'"},
			{From: "approved_path", To: "end", Type: definition.EdgeNormal},
			{From: "revise_path", To: "end", Type: definition.EdgeNormal},
		},
	}
}
