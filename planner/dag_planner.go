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
// 数据流接线：节点参数里可放**引用对象** {"$from":"<上游节点id>","field":"<输出字段>"}，
// 运行时 Resolve 从 ExecState 取上游产出替换（field 省略则取整个 output）。
// FR5 强约束：被 $from 引用的节点必须在该节点 depends_on 里——数据依赖必须是图的边。
//
// 本版范围（诚实标注）：引用是**整值替换**（一个参数值整体来自某上游输出），暂不支持
// "把上游值插进字符串模板"的字符串内插，也不支持引用里再做表达式运算。够干常见活
// （把上游产出的 url/id/path 喂给下游），更复杂的接线留待后续。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tuxi/flux/definition"
	"github.com/tuxi/flux/model"
	"github.com/tuxi/flux/runtime"
	"github.com/tuxi/flux/tool"
)

type DAGPlanner struct {
	Provider   *model.OpenAICompatibleProvider
	Model      string
	Goal       string
	System     string
	MaxRepairs int // FR5/FR6：generate→validate→repair 的最大修复轮数（默认 3）

	registry *tool.Registry
	lastSpec planSpec // 最近一次校验通过的计划（供检视/调试）
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
	Nodes         []nodeSpec       `json:"nodes"`
	OutputMapping map[string]string `json:"output_mapping,omitempty"` // "primary_file_url": "upload_result.url"
	ResultType    string           `json:"result_type,omitempty"`     // "image", "video", "generic"
}

const dagSystemPrompt = `You are a planner. Given a goal and a catalog of tools, produce ONE complete
execution plan as a directed acyclic graph by calling submit_plan exactly once.

Rules:
- Each node is exactly one tool call. Use descriptive IDs (e.g. "validate_params", "normalize_image").
- Use depends_on to express ordering: a node runs only after ALL nodes it lists have succeeded.
- Independent nodes (empty depends_on) may run in parallel — express real parallelism this way.
- Tool arguments may be literals OR $from references: {"$from": "<node id>", "field": "<output field>"}.
  Omit "field" to reference the entire output object.
  A node referenced via $from MUST appear in depends_on (data dependency = graph edge).

CRITICAL — $from field correctness:
- Each tool's output fields are listed as "输出: field1(type), field2(type)".
- ONLY reference fields that appear in the source tool's output list.
- Example: if "generate_script" outputs "video_script, voiceover_text",
  use {"$from": "generate_script", "field": "video_script"} — NOT "script_content".
- If unsure which node outputs a field, check the tool catalog output descriptions.

- Specify result_type based on the goal: "image" for image generation, "video" for video, "generic" otherwise.
- Specify output_mapping to expose key results, e.g. {"primary_file_url": "upload.url", "width": "postprocess.width"}. Use <node_id>.<field> syntax.
- Do NOT call the tools yourself. Call ONLY submit_plan once with the complete plan.
- If validation_errors are returned, carefully fix ALL listed errors before retrying.`

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
			ToolChoice: "auto",
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
			messages = append(messages, toolMsg(call.ID, fmt.Sprintf(`{"validation_errors":%s,"hint":"Study the tool catalog output fields carefully. Each $from field MUST match an output field listed for that tool. Check that every $from target is also in depends_on."}`, jsonList(errs))))
			continue
		}

		p.lastSpec = spec
		return buildPlan(spec), nil
	}
	return nil, fmt.Errorf("dag planner: plan did not validate within %d repair rounds", p.MaxRepairs)
}

// GenerateWorkflow 生成一张校验通过的 DAG，返回 v1 engine 可直接执行的 WorkflowDefinition。
// 这是 v3 AI 规划 + v1 可靠执行的关键桥接。
func (p *DAGPlanner) GenerateWorkflow(ctx context.Context, workflowTools map[string]string) (*definition.WorkflowDefinition, error) {
	if _, err := p.Generate(ctx); err != nil {
		return nil, err
	}
	return SpecToWorkflow(p.lastSpec, p.Goal, workflowTools), nil
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
		// 数据流引用（FR5）：每个 $from 目标必须存在，且必须是本节点的 depends_on 边，
		// 否则 kernel 可能在上游产出前就跑本节点 —— 引用无法解析。
		depSet := map[string]bool{}
		for _, d := range n.DependsOn {
			depSet[d] = true
		}
		for _, ref := range referencedNodes(n.Arguments) {
			switch {
			case !ids[ref]:
				errs = append(errs, fmt.Sprintf("node %q: argument references unknown node %q", n.ID, ref))
			case !depSet[ref]:
				errs = append(errs, fmt.Sprintf("node %q: references %q but it is not in depends_on (data dependency must be a graph edge)", n.ID, ref))
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
			Resolve: func(_ context.Context, state runtime.ExecState) (map[string]any, error) {
				resolved, err := resolveRefs(captured, state)
				if err != nil {
					return nil, err
				}
				m, _ := resolved.(map[string]any)
				if m == nil {
					m = map[string]any{}
				}
				return m, nil
			},
		}
	}
	return plan
}

// resolveRefs 递归地把参数里的引用对象 {"$from":id,"field":f} 替换成上游节点的实际产出。
// 上游 output 由 kernel 保证已存在（引用必是 depends_on 边，调度已等齐）。
func resolveRefs(v any, state runtime.ExecState) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		if from, ok := x["$from"].(string); ok {
			out := state.Output(from)
			if out == nil {
				return nil, fmt.Errorf("reference to node %q: no output available", from)
			}
			if f, ok := x["field"].(string); ok && f != "" {
				val, present := out[f]
				if !present {
					return nil, fmt.Errorf("reference %q.%q: field not in output", from, f)
				}
				return val, nil
			}
			return out, nil
		}
		m := make(map[string]any, len(x))
		for k, vv := range x {
			r, err := resolveRefs(vv, state)
			if err != nil {
				return nil, err
			}
			m[k] = r
		}
		return m, nil
	case []any:
		arr := make([]any, len(x))
		for i, e := range x {
			r, err := resolveRefs(e, state)
			if err != nil {
				return nil, err
			}
			arr[i] = r
		}
		return arr, nil
	default:
		return v, nil
	}
}

// referencedNodes 收集参数树里所有 $from 引用的节点 id（用于 FR5 校验）。
func referencedNodes(args map[string]any) []string {
	var refs []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if from, ok := x["$from"].(string); ok {
				refs = append(refs, from)
				return // 引用对象本身不再深入
			}
			for _, vv := range x {
				walk(vv)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(args)
	return refs
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
	b.WriteString("Available tools (name: description | 输入: ... | 输出: ...):\n")
	for _, t := range reg.List() {
		d := tool.DefinitionOf(t)
		b.WriteString(fmt.Sprintf("- %s: %s\n", d.Name, d.Description))
		b.WriteString(fmt.Sprintf("  输入: %s\n", toolInputSummary(d.InputSchema)))
		b.WriteString(fmt.Sprintf("  输出: %s\n", toolOutputSummary(t)))
	}
	return b.String()
}

// toolInputSummary returns a compact, human-readable list of input fields with types.
func toolInputSummary(schema json.RawMessage) string {
	if len(schema) == 0 {
		return "(any)"
	}
	var s struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil || len(s.Properties) == 0 {
		return "(any)"
	}
	parts := make([]string, 0, len(s.Properties))
	for name, prop := range s.Properties {
		t := prop.Type
		if t == "" {
			t = "string"
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", name, t))
	}
	return strings.Join(parts, ", ")
}

// toolOutputSummary returns the output fields declared by the tool.
func toolOutputSummary(t tool.Tool) string {
	schema := tool.DefinitionOf(t).OutputSchema
	if len(schema) == 0 {
		// Fallback: check if it's a mock tool (no output schema defined)
		return "(echoes input)"
	}
	return toolInputSummary(schema)
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
		Description: "Submit the complete execution plan as a DAG of tool-call nodes with output mapping.",
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
				"result_type": map[string]any{
					"type":        "string",
					"description": "Result type: 'image', 'video', 'audio', or 'generic'",
				},
				"output_mapping": map[string]any{
					"type":        "object",
					"description": "Map result fields to node outputs, e.g. {\"primary_file_url\": \"upload_result.url\", \"width\": \"final_node.width\"}. Use 'nodes.<node_id>.output.<field>' format with expr-like syntax.",
				},
			},
			"required": []string{"nodes", "result_type"},
		},
	},
}
