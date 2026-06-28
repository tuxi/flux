package planner

import (
	"crypto/sha256"
	"fmt"

	"flux/definition"
)

// SpecToWorkflow 将 DAGPlanner 生成的 planSpec 转换为 v1 engine 可执行的 WorkflowDefinition。
//
// 这是 v3 AI 规划 + v1 可靠执行的关键桥接：
//   DAGPlanner → planSpec → SpecToWorkflow → WorkflowDefinition → v1 engine.RunDAG()
//
// v1 engine 提供：
//   - Task/Node 持久化（task_nodes 表）
//   - Async AwaitBinding + Poll Worker
//   - 分布式锁 + Crash Recovery
//   - Task Events 事件流
func SpecToWorkflow(spec planSpec, goal string) *definition.WorkflowDefinition {
	name := sanitizeName(goal)
	nodes := make([]definition.NodeDefinition, 0, len(spec.Nodes)+2)
	edges := make([]definition.EdgeDefinition, 0, len(spec.Nodes)+len(spec.Nodes))

	// 1. 开始节点
	nodes = append(nodes, definition.NodeDefinition{
		Name:  "start",
		Label: "开始",
		Type:  definition.NodeStart,
	})

	// 2. 转换每个 planSpec 节点 → NodeDefinition
	nodeIDs := map[string]bool{}
	for _, n := range spec.Nodes {
		nodeIDs[n.ID] = true

		inputMapping := convertArguments(n.Arguments, n.ID)
		nd := definition.NodeDefinition{
			Name:         n.ID,
			Label:        n.ID,
			Type:         definition.NodeTool,
			Weight:       0.05,
			Config:       map[string]any{"tool": n.Tool},
			InputMapping: inputMapping,
		}
		nodes = append(nodes, nd)

		// depends_on → edges
		if len(n.DependsOn) == 0 {
			// 无依赖 → 从 start 出发
			edges = append(edges, definition.EdgeDefinition{
				From: "start",
				To:   n.ID,
				Type: definition.EdgeNormal,
			})
		} else {
			for _, dep := range n.DependsOn {
				edges = append(edges, definition.EdgeDefinition{
					From: dep,
					To:   n.ID,
					Type: definition.EdgeNormal,
				})
			}
		}
	}

	// 3. 结束节点
	nodes = append(nodes, definition.NodeDefinition{
		Name:  "end",
		Label: "结束",
		Type:  definition.NodeEnd,
	})

	// 4. 找出没有下游的节点 → 连到 end
	hasDownstream := map[string]bool{}
	for _, e := range edges {
		hasDownstream[e.From] = true
	}
	for _, n := range spec.Nodes {
		if !hasDownstream[n.ID] {
			edges = append(edges, definition.EdgeDefinition{
				From: n.ID,
				To:   "end",
				Type: definition.EdgeNormal,
			})
		}
	}

	return &definition.WorkflowDefinition{
		Name:  name,
		Desc:  "AI 自主规划: " + truncate(goal, 100),
		Nodes: nodes,
		Edges: edges,
		Output: buildOutput(spec),
	}
}

// buildOutput 从 planSpec 的 output_mapping 构建 OutputDefinition。
// 当 LLM 指定了 output_mapping 时使用；否则退回到默认泛型输出。
func buildOutput(spec planSpec) definition.OutputDefinition {
	resultType := spec.ResultType
	if resultType == "" {
		resultType = "generic"
	}

	// 从 output_mapping 提取 PrimaryFileUrl
	primaryFileUrl := ""
	if url, ok := spec.OutputMapping["primary_file_url"]; ok {
		primaryFileUrl = fmt.Sprintf("nodes.%s", url)
	}

	extras := make(map[string]string)
	for k, v := range spec.OutputMapping {
		if k == "primary_file_url" {
			continue
		}
		extras[k] = fmt.Sprintf("nodes.%s", v)
	}

	return definition.OutputDefinition{
		ResultType:     resultType,
		PrimaryFileUrl: primaryFileUrl,
		Extras:         extras,
	}
}

// convertArguments 将 planSpec 节点的 arguments 转为 InputMapping。
//
// 规则：
//   - {"$from": "node_id", "field": "field_name"} → "node_id.field_name"
//   - 普通字符串值 → "'value'"（expr 字符串字面量）
func convertArguments(args map[string]any, nodeID string) map[string]string {
	if len(args) == 0 {
		return nil
	}
	mapping := make(map[string]string, len(args))
	for key, val := range args {
		switch v := val.(type) {
		case map[string]any:
			if from, ok := v["$from"].(string); ok {
				field, _ := v["field"].(string)
				if field != "" {
					mapping[key] = fmt.Sprintf("%s.%s", from, field)
				} else {
					mapping[key] = from
				}
				continue
			}
			// 复杂嵌套对象 → 跳过（expr 不支持）
		case string:
			mapping[key] = fmt.Sprintf("'%s'", v)
		case float64:
			mapping[key] = fmt.Sprintf("%v", v)
		case bool:
			mapping[key] = fmt.Sprintf("%t", v)
		default:
			// 跳过不支持的类型
		}
	}
	return mapping
}

func sanitizeName(goal string) string {
	// 用 goal 的 SHA256 前 8 位 + 固定前缀，保证 ASCII-safe 且在 DB 中唯一
	h := sha256.Sum256([]byte(goal))
	return fmt.Sprintf("ai_gen_%x", h[:4])
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
