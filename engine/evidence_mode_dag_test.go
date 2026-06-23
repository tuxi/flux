package engine

import (
	"context"
	"testing"

	"flux/definition"
	"flux/domain"
	"flux/tool"
	"flux/workflow"
	"flux/workflow/nodes"
	"github.com/stretchr/testify/require"
)

// newEvidenceModeWorkflow 复刻 video_to_prompt 的双入口汇聚拓扑（精简版，stub 工具）：
//
//	start ─[cond: != client]→ server_pre ─┐
//	start ─[cond: == client]──────────────┤
//	server_pre ─[normal]──────────────────┤
//	                                       ↓
//	                                    resolve → analyze → end
//
// 用于回归 condition 分支 + fan-in 汇聚：
//   - server 模式：server_pre 跑、resolve 等它、不被 client 条件边误跳。
//   - client 模式：server_pre 整支 skip，resolve 经 start 的 client 条件边触发，
//     且 resolve InputMapping 读到被跳过的 server_pre 输出为 null 也不报错。
func newEvidenceModeWorkflow(t *testing.T) (*workflow.Builder, workflow.Workflow) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Register(&syncResultTool{name: "pre_tool", output: map[string]any{"ready": true},
		schema: tool.DataSchema{Fields: map[string]tool.FieldSchema{"ready": {Type: "bool"}}}})
	reg.Register(&syncResultTool{name: "resolve_tool", output: map[string]any{"evidence": "ok"},
		schema: tool.DataSchema{Fields: map[string]tool.FieldSchema{"evidence": {Type: "string"}}}})
	reg.Register(&syncResultTool{name: "analyze_tool", output: map[string]any{"result": "done"},
		schema: tool.DataSchema{Fields: map[string]tool.FieldSchema{"result": {Type: "string"}}}})
	builder := workflow.NewBuilder(nodes.InitNodeRegistry(reg))

	def := &definition.WorkflowDefinition{
		Name: "evidence_mode_wf",
		Output: definition.OutputDefinition{
			ResultType: "prompt",
			Extras:     map[string]string{"result": "nodes.analyze.output.result"},
		},
		Nodes: []definition.NodeDefinition{
			{Name: "start", Type: definition.NodeStart},
			{Name: "server_pre", Type: definition.NodeTool, Config: map[string]any{"tool": "pre_tool"}},
			{Name: "resolve", Type: definition.NodeTool, Config: map[string]any{"tool": "resolve_tool"},
				// 故意读 server_pre（client 模式下被跳过 → null，必须不报错）。
				InputMapping: map[string]string{
					"from_server": "server_pre.ready",
					"client_ev":   "input.client_evidence",
				}},
			{Name: "analyze", Type: definition.NodeTool, Config: map[string]any{"tool": "analyze_tool"}},
			{Name: "end", Type: definition.NodeEnd},
		},
		Edges: []definition.EdgeDefinition{
			{From: "start", To: "server_pre", Type: definition.EdgeCondition, Condition: "input.evidence_mode != 'client_evidence_only'"},
			{From: "start", To: "resolve", Type: definition.EdgeCondition, Condition: "input.evidence_mode == 'client_evidence_only'"},
			{From: "server_pre", To: "resolve", Type: definition.EdgeNormal},
			{From: "resolve", To: "analyze", Type: definition.EdgeNormal},
			{From: "analyze", To: "end", Type: definition.EdgeNormal},
		},
	}
	wf, err := builder.Build(def)
	require.NoError(t, err)
	return builder, wf
}

func runEvidenceMode(t *testing.T, inputJSON string) *nodes.Context {
	t.Helper()
	builder, wf := newEvidenceModeWorkflow(t)
	task := &domain.Task{ID: 7001, RootID: 7001, Status: domain.TaskPending, WorkflowVersionID: 1, InputJSON: []byte(inputJSON)}
	e := newEngineForTests(builder, newFakeTaskRepo(task), newFakeNodeRepo(), newFakeWorkflowVersionRepo(), newFakeWorkflowRepo())

	runCtx := e.newRunContext(context.Background(), task, wf)
	require.NoError(t, e.loadOrInitRuntime(runCtx, wf))
	result := e.executeTask(runCtx, wf, false)
	require.Equal(t, RunSuccess, result.Status, "evidence_mode workflow 应执行成功")
	return runCtx
}

// server_auto（默认，evidence_mode 缺省）：server_pre 跑通，resolve 等它汇聚，下游正常。
// 关键回归：start→resolve 的 client 条件边 false 不得误跳 resolve（canSkipNode 对
// 未决定父返回 false）。
func TestRunDAG_EvidenceMode_ServerAuto(t *testing.T) {
	rc := runEvidenceMode(t, `{}`)
	require.Equal(t, domain.NodeSuccess, rc.Runtime["server_pre"].State, "server_pre 应执行")
	require.Equal(t, domain.NodeSuccess, rc.Runtime["resolve"].State, "resolve 应在 server_pre 后执行(不被误跳)")
	require.Equal(t, domain.NodeSuccess, rc.Runtime["analyze"].State, "analyze 应执行")
}

// 显式 server_auto 同样走前置分支。
func TestRunDAG_EvidenceMode_ServerAutoExplicit(t *testing.T) {
	rc := runEvidenceMode(t, `{"evidence_mode":"server_auto"}`)
	require.Equal(t, domain.NodeSuccess, rc.Runtime["server_pre"].State)
	require.Equal(t, domain.NodeSuccess, rc.Runtime["resolve"].State)
	require.Equal(t, domain.NodeSuccess, rc.Runtime["analyze"].State)
}

// client_evidence_only：server 前置整支跳过，resolve 经 start 的 client 条件边触发，
// 且读取被跳过的 server_pre 输出为 null 不报错，下游正常。
func TestRunDAG_EvidenceMode_ClientEvidenceOnly(t *testing.T) {
	rc := runEvidenceMode(t, `{"evidence_mode":"client_evidence_only","client_evidence":{"x":1}}`)
	require.Equal(t, domain.NodeSkipped, rc.Runtime["server_pre"].State, "client 模式 server_pre 应被跳过")
	require.Equal(t, domain.NodeSuccess, rc.Runtime["resolve"].State, "resolve 应经 client 条件边触发")
	require.Equal(t, domain.NodeSuccess, rc.Runtime["analyze"].State, "下游应正常执行")
}
