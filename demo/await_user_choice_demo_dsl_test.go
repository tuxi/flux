package demo

import (
	"flux/definition"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAwaitUserChoiceDemoWorkflow_UsesAwaitSignalAndConditionalEdges(t *testing.T) {
	def := AwaitUserChoiceDemoWorkflow()

	require.Equal(t, "await_user_choice_demo", def.Name)

	var awaitNode *definition.NodeDefinition
	for i := range def.Nodes {
		if def.Nodes[i].Name == "user_choice" {
			awaitNode = &def.Nodes[i]
			break
		}
	}

	require.NotNil(t, awaitNode)
	require.EqualValues(t, definition.NodeAwait, awaitNode.Type)
	require.Equal(t, "user_input", awaitNode.Config["await_type"])
	require.Equal(t, "signal", awaitNode.Config["source"])
	require.Equal(t, "await_user_choice_demo_signal", awaitNode.Config["signal_name"])
	require.Equal(t, "input.callback_token", awaitNode.Config["callback_token_expr"])

	var hasApproveEdge bool
	var hasReviseEdge bool
	for _, edge := range def.Edges {
		if edge.From == "user_choice" && edge.To == "approved_path" {
			hasApproveEdge = true
			require.EqualValues(t, definition.EdgeCondition, edge.Type)
			require.Equal(t, "user_choice.choice == 'approve'", edge.Condition)
		}
		if edge.From == "user_choice" && edge.To == "revise_path" {
			hasReviseEdge = true
			require.EqualValues(t, definition.EdgeCondition, edge.Type)
			require.Equal(t, "user_choice.choice == 'revise'", edge.Condition)
		}
	}

	require.True(t, hasApproveEdge)
	require.True(t, hasReviseEdge)
}
