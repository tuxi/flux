package workflow

// compile.go 是"编排前端 #1"：把人写的 WorkflowDefinition 编译成 runtime.Plan。
// 所有 DSL 语义（input_mapping 表达式、edge condition、start/end 系统节点、
// map/loop/subworkflow 展开）都在这里解析掉。expr-lang 只活在本包，runtime 底座一无所知。
//
// 这是依赖反转的落点：engine 过去 import definition；现在底座(runtime) 不依赖 definition，
// 由本编译器把 definition 翻译成 runtime 原语。

import (
	"context"
	"fmt"

	"github.com/expr-lang/expr"

	"github.com/tuxi/flux/definition"
	"github.com/tuxi/flux/runtime"
)

// Compile 把 WorkflowDefinition 翻译为 runtime.Plan（依赖反转的关键一步）。
//
//	definition.Edge                    → PlanNode.DependsOn（纯拓扑，丢掉 condition）
//	definition.NodeStart/NodeEnd       → 编译期吸收（不进 plan，也不作为依赖）
//	definition.NodeAwait / async tool  → PlanNode.Async = true
//	NodeDefinition.InputMapping(expr)  → PlanNode.Resolve（expr 求值闭包）
//
// asyncTool 由调用方注入（通常是 tool.Registry 的 "该工具是否 async" 判定）。
func Compile(def *definition.WorkflowDefinition, asyncTool func(toolName string) bool) (*runtime.Plan, error) {
	plan := &runtime.Plan{Nodes: make(map[string]*runtime.PlanNode, len(def.Nodes))}

	// start/end 是系统节点：既不进 plan，也不作为依赖 —— 这样它们的直接下游成为 DAG 的根。
	system := map[string]bool{}
	for i := range def.Nodes {
		nd := &def.Nodes[i]
		if nd.Type == definition.NodeStart || nd.Type == definition.NodeEnd {
			system[nd.Name] = true
		}
	}

	// 1) 依赖边：edges → DependsOn（控制流 condition 在编译期决定，运行时只剩依赖）
	deps := map[string][]string{}
	for _, e := range def.Edges {
		if system[e.From] || system[e.To] {
			continue
		}
		deps[e.To] = append(deps[e.To], e.From)
	}

	// 2) 节点
	for i := range def.Nodes {
		nd := &def.Nodes[i]
		if system[nd.Name] {
			continue
		}

		toolName := resolveToolName(nd)
		mapping := nd.InputMapping // 闭包捕获，避免 range 变量复用

		plan.Nodes[nd.Name] = &runtime.PlanNode{
			Name:      nd.Name,
			ToolName:  toolName,
			DependsOn: deps[nd.Name],
			Async:     nd.Type == definition.NodeAwait || asyncTool(toolName),
			Join:      runtime.JoinAll,
			Retry:     resolveRetry(nd.Config),
			// InputResolver：把 input_mapping 表达式在运行时对实时上游产出求值。
			// 这正是现在 nodes.Context.eval / buildExprEnv 的逻辑，挪到前端闭包里。
			Resolve: func(_ context.Context, state runtime.ExecState) (map[string]any, error) {
				return evalInputMapping(mapping, state)
			},
		}
	}
	return plan, nil
}

// evalInputMapping 用 state 里的上游产出求 expr —— 等价于现有 Context.buildExprEnv + eval，
// 但读的是 runtime.ExecState（nodes.Context 实现它即可对接，memState 也行）。
func evalInputMapping(mapping map[string]string, state runtime.ExecState) (map[string]any, error) {
	out := map[string]any{}
	if len(mapping) == 0 {
		return out, nil
	}

	// env = { input, nodes:{n:{output}}, n:output（兼容 upload_result.url 这种纯净写法） }
	env := map[string]any{"input": state.Input()}
	nodesEnv := map[string]any{}
	for _, n := range state.Nodes() {
		o := state.Output(n)
		nodesEnv[n] = map[string]any{"output": o}
		env[n] = o
	}
	env["nodes"] = nodesEnv

	for field, exprStr := range mapping {
		v, err := expr.Eval(exprStr, env)
		if err != nil {
			return nil, fmt.Errorf("eval input_mapping %q=%q: %w", field, exprStr, err)
		}
		out[field] = v
	}
	return out, nil
}

// resolveToolName 从节点定义解析出要调用的工具名（config["tool"] 或按 type 映射）。
func resolveToolName(nd *definition.NodeDefinition) string {
	if nd.Config != nil {
		if t, ok := nd.Config["tool"].(string); ok && t != "" {
			return t
		}
	}
	return string(nd.Type)
}

// resolveRetry 解析重试策略（语义同 ToolStepAdapter.RetryPolicy）。
//
// TODO: 复用 ToolStepAdapter.RetryPolicy 的完整 config 解析。
func resolveRetry(cfg map[string]any) runtime.RetryPolicy {
	_ = cfg
	return runtime.RetryPolicy{MaxRetries: 2}
}
