package planner

// DAGPlanner 是类型 B（StaticPlanSource / dataflow DAG）的前端：
// LLM **一次性**生成一张完整的执行 DAG（不走"走一步看一步"），经 schema 校验后
// 交给 kernel 用 StaticSource 确定性执行。
//
// 与类型 A（LLMPlanner / control loop）的区别就是 requirements FR3 说的那一点：
// 这里 Next 不反馈驱动——整图一次给出（done=true）。其余（kernel/Invoker/ExecState）全共享。
//
// FR5：LLM 生成的图可能引用不存在的工具/坏依赖/有环/缺必填参数，必须**执行前校验**；
// 校验失败把错误喂回 LLM 重生（generate → validate → repair，规划期循环）。
//
// 本版范围（诚实标注）：节点参数为**具体值**，暂不支持"引用上游节点输出"的数据流接线
// （即 workflow input_mapping 那种 node_x.output.y）。这让首个类型 B 证明聚焦在
// "LLM 生成带依赖/并行的 DAG + 校验 + kernel 调度"，数据流接线留待后续。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"flux/model"
	"flux/runtime"
	"flux/tool"
)

type DAGPlanner struct {
	Provider   *model.OpenAICompatibleProvider
	Model      string
	Goal       string
	System     string
	MaxRepairs int // FR5/FR6：generate→validate→repair 的最大修复轮数（默认 3）

	registry *tool.Registry
}

func NewDAGPlanner(provider *model.OpenAICompatibleProvider, modelName, goal string, reg *tool.Registry) *DAGPlanner {
	return &DAGPlanner{
		Provider:   provider,
		Model:      modelName,
		Goal:       goal,
		MaxRepairs: 3,
		registry:   reg,
	}
}

// nodeSpec / planSpec 是 LLM 通过 submit_plan 提交的计划结构。
type nodeSpec struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	DependsOn []string       `json:"depends_on"`
}

type planSpec struct {
	Nodes []nodeSpec `json:"nodes"`
}

const dagSystemPrompt = `You are a planner. Given a goal and a catalog of tools, produce ONE complete
execution plan as a directed acyclic graph by calling submit_plan exactly once.

Rules:
- Each node is exactly one tool call with concrete arguments.
- Use depends_on to express ordering: a node runs only after ALL nodes it lists have succeeded.
- Independent nodes (empty depends_on) may run in parallel — express real parallelism this way.
- Arguments must be concrete values (you cannot reference other nodes' outputs in this version).
- Do NOT call the tools yourself. Call ONLY submit_plan, once, with the whole plan.`

// Generate 让 LLM 产出一张校验通过的 DAG，返回可执行的 runtime.Plan。
// 调用方：sched.Run(ctx, runtime.NewStaticSource(plan), state)。
func (p *DAGPlanner) Generate(ctx context.Context) (*runtime.Plan, error) {
	sys := p.System
	if sys == "" {
		sys = dagSystemPrompt
	}
	messages := []model.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: p.Goal + "\n\n可用工具：\n" + toolCatalog(p.registry)},
	}

	for attempt := 0; attempt <= p.MaxRepairs; attempt++ {
		resp, err := p.Provider.Complete(ctx, model.Request{
			Model:      p.Model,
			Messages:   messages,
			Tools:      []model.ToolDefinition{submitPlanTool},
			ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "submit_plan"}},
		})
		if err != nil {
			return nil, fmt.Errorf("dag planner llm call: %w", err)
		}
		messages = append(messages, model.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})

		call := findCall(resp, "submit_plan")
		if call == nil {
			// 没按要求调 submit_plan：让它重来。
			messages = append(messages, model.Message{Role: "user", Content: "You must produce the plan by calling submit_plan. Try again."})
			continue
		}

		var spec planSpec
		if err := json.Unmarshal([]byte(call.Function.Arguments), &spec); err != nil {
			messages = append(messages, toolMsg(call.ID, fmt.Sprintf(`{"error":"submit_plan arguments are not valid JSON: %s"}`, err.Error())))
			continue
		}

		// FR5：执行前校验
		if errs := validatePlan(spec, p.registry); len(errs) > 0 {
			messages = append(messages, toolMsg(call.ID, fmt.Sprintf(`{"validation_errors":%s}`, jsonList(errs))))
			continue
		}

		return buildPlan(spec), nil
	}
	return nil, fmt.Errorf("dag planner: plan did not validate within %d repair rounds", p.MaxRepairs)
}

// validatePlan 是 FR5 的核心：工具存在 / 依赖合法 / 无环 / 必填参数齐全。
// （类型/枚举等完整 JSON Schema 校验需要 schema 校验库，本版不引入，诚实留作后续。）
func validatePlan(spec planSpec, reg *tool.Registry) []string {
	var errs []string
	if len(spec.Nodes) == 0 {
		return []string{"plan has no nodes"}
	}

	ids := map[string]bool{}
	for _, n := range spec.Nodes {
		if n.ID == "" {
			errs = append(errs, "a node has an empty id")
			continue
		}
		if ids[n.ID] {
			errs = append(errs, "duplicate node id: "+n.ID)
		}
		ids[n.ID] = true
	}

	for _, n := range spec.Nodes {
		t, ok := reg.Get(n.Tool)
		if !ok {
			errs = append(errs, fmt.Sprintf("node %q: tool %q does not exist", n.ID, n.Tool))
		}
		for _, d := range n.DependsOn {
			if !ids[d] {
				errs = append(errs, fmt.Sprintf("node %q: depends_on references unknown node %q", n.ID, d))
			}
		}
		if ok {
			for _, req := range requiredFields(tool.DefinitionOf(t).InputSchema) {
				if _, present := n.Arguments[req]; !present {
					errs = append(errs, fmt.Sprintf("node %q: missing required argument %q for tool %q", n.ID, req, n.Tool))
				}
			}
		}
	}

	if cyclic(spec) {
		errs = append(errs, "plan has a dependency cycle")
	}
	return errs
}

func buildPlan(spec planSpec) *runtime.Plan {
	plan := &runtime.Plan{Nodes: make(map[string]*runtime.PlanNode, len(spec.Nodes))}
	for _, n := range spec.Nodes {
		args := n.Arguments
		if args == nil {
			args = map[string]any{}
		}
		captured := args
		plan.Nodes[n.ID] = &runtime.PlanNode{
			Name:      n.ID,
			ToolName:  n.Tool,
			DependsOn: n.DependsOn,
			Join:      runtime.JoinAll,
			Resolve: func(_ context.Context, _ runtime.ExecState) (map[string]any, error) {
				return captured, nil // 类型 B 本版：具体值，无上游引用
			},
		}
	}
	return plan
}

// cyclic 用 DFS 三色法检测依赖环。
func cyclic(spec planSpec) bool {
	deps := map[string][]string{}
	for _, n := range spec.Nodes {
		deps[n.ID] = n.DependsOn
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		color[id] = gray
		for _, d := range deps[id] {
			switch color[d] {
			case gray:
				return true // 回边 → 环
			case white:
				if visit(d) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for _, n := range spec.Nodes {
		if color[n.ID] == white {
			if visit(n.ID) {
				return true
			}
		}
	}
	return false
}

func requiredFields(schema json.RawMessage) []string {
	if len(schema) == 0 {
		return nil
	}
	var s struct {
		Required []string `json:"required"`
	}
	_ = json.Unmarshal(schema, &s)
	return s.Required
}

func toolCatalog(reg *tool.Registry) string {
	var b strings.Builder
	for _, t := range reg.List() {
		d := tool.DefinitionOf(t)
		b.WriteString(fmt.Sprintf("- %s: %s\n  input schema: %s\n", d.Name, d.Description, string(d.InputSchema)))
	}
	return b.String()
}

func findCall(resp model.Response, name string) *model.ToolCall {
	for i := range resp.ToolCalls {
		if resp.ToolCalls[i].Function.Name == name {
			return &resp.ToolCalls[i]
		}
	}
	return nil
}

func toolMsg(callID, content string) model.Message {
	return model.Message{Role: "tool", ToolCallID: callID, Content: content}
}

func jsonList(items []string) string {
	b, _ := json.Marshal(items)
	return string(b)
}

var submitPlanTool = model.ToolDefinition{
	Type: "function",
	Function: model.FunctionSchema{
		Name:        "submit_plan",
		Description: "Submit the complete execution plan as a DAG of tool-call nodes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nodes": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":         map[string]any{"type": "string", "description": "unique node id"},
							"tool":       map[string]any{"type": "string", "description": "name of the tool to call"},
							"arguments":  map[string]any{"type": "object", "description": "concrete arguments for the tool"},
							"depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "ids of nodes that must succeed before this one"},
						},
						"required": []string{"id", "tool"},
					},
				},
			},
			"required": []string{"nodes"},
		},
	},
}
