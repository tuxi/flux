package handler

import (
	"flux/definition"
	"flux/dto"
	"flux/workflow"
	"fmt"
	"sort"
)

// expansionGraphBuilder expansion graph builder 临时结构
type expansionGraphBuilder struct {
	nodes      map[string]dto.RunNodeExpansionNodeDTO
	edges      map[string]dto.RunNodeExpansionEdgeDTO
	groupNodes map[string][]string
}

func newExpansionGraphBuilder() *expansionGraphBuilder {
	return &expansionGraphBuilder{
		nodes:      make(map[string]dto.RunNodeExpansionNodeDTO),
		edges:      make(map[string]dto.RunNodeExpansionEdgeDTO),
		groupNodes: make(map[string][]string),
	}
}

func (b *expansionGraphBuilder) addNode(node dto.RunNodeExpansionNodeDTO) {
	b.nodes[node.ID] = node
}

func (b *expansionGraphBuilder) addEdge(edge dto.RunNodeExpansionEdgeDTO) {
	b.edges[edge.ID] = edge
}

func (b *expansionGraphBuilder) appendGroupNode(groupID, nodeID string) {
	b.groupNodes[groupID] = append(b.groupNodes[groupID], nodeID)
}

func (b *expansionGraphBuilder) nodeSlice() []dto.RunNodeExpansionNodeDTO {
	out := make([]dto.RunNodeExpansionNodeDTO, 0, len(b.nodes))
	for _, n := range b.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *expansionGraphBuilder) edgeSlice() []dto.RunNodeExpansionEdgeDTO {
	out := make([]dto.RunNodeExpansionEdgeDTO, 0, len(b.edges))
	for _, e := range b.edges {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type workflowAdjacency struct {
	outgoing map[string][]definition.EdgeDefinition
	inDegree map[string]int
}

func buildWorkflowAdjacency(wf workflow.Workflow) workflowAdjacency {
	outgoing := make(map[string][]definition.EdgeDefinition)
	inDegree := make(map[string]int)

	for _, from := range wf.Order() {
		for _, e := range wf.Graph().Edges[from] {
			outgoing[from] = append(outgoing[from], e)
			inDegree[e.To]++
			if _, ok := inDegree[from]; !ok {
				inDegree[from] = inDegree[from]
			}
		}
	}

	for _, name := range wf.Order() {
		if _, ok := outgoing[name]; !ok {
			outgoing[name] = nil
		}
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
	}

	return workflowAdjacency{
		outgoing: outgoing,
		inDegree: inDegree,
	}
}

func findWorkflowEntryNodes(wf workflow.Workflow, adj workflowAdjacency) []string {
	var out []string
	for _, name := range wf.Order() {
		if adj.inDegree[name] == 0 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func isBranchFork(adj workflowAdjacency, nodeName string) bool {
	edges := adj.outgoing[nodeName]
	if len(edges) < 2 {
		return false
	}

	condCount := 0
	for _, e := range edges {
		if e.Condition != "" || e.CaseKey != "" || e.Type == definition.EdgeCondition {
			condCount++
		}
	}
	return condCount >= 2
}

// 从 fork 的每个 branch 起点往后搜，找到 第一个所有 branch 都能到达的公共节点。
func findMergeBoundary(
	wf workflow.Workflow,
	adj workflowAdjacency,
	forkNode string,
) string {
	branchEdges := adj.outgoing[forkNode]
	if len(branchEdges) < 2 {
		return ""
	}

	reachSets := make([]map[string]struct{}, 0, len(branchEdges))
	for _, e := range branchEdges {
		reachSets = append(reachSets, collectReachableNodes(adj, e.To))
	}

	if len(reachSets) == 0 {
		return ""
	}

	common := make(map[string]int)
	for _, set := range reachSets {
		for node := range set {
			common[node]++
		}
	}

	candidates := make([]string, 0)
	for node, cnt := range common {
		if cnt == len(reachSets) {
			candidates = append(candidates, node)
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	levels := computeWorkflowLevels(wf, adj)
	sort.Slice(candidates, func(i, j int) bool {
		li := levels[candidates[i]]
		lj := levels[candidates[j]]
		if li != lj {
			return li < lj
		}
		return candidates[i] < candidates[j]
	})

	return candidates[0]
}

// 收集可达节点
func collectReachableNodes(adj workflowAdjacency, start string) map[string]struct{} {
	visited := make(map[string]struct{})
	queue := []string{start}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if _, ok := visited[cur]; ok {
			continue
		}
		visited[cur] = struct{}{}

		for _, e := range adj.outgoing[cur] {
			queue = append(queue, e.To)
		}
	}

	return visited
}

// 计算 definition level，用于 merge 候选排序
func computeWorkflowLevels(wf workflow.Workflow, adj workflowAdjacency) map[string]int {
	level := make(map[string]int)
	inDegree := make(map[string]int, len(adj.inDegree))
	for k, v := range adj.inDegree {
		inDegree[k] = v
	}

	queue := make([]string, 0)
	for _, name := range wf.Order() {
		if inDegree[name] == 0 {
			queue = append(queue, name)
			level[name] = 0
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for _, e := range adj.outgoing[cur] {
			if level[e.To] < level[cur]+1 {
				level[e.To] = level[cur] + 1
			}
			inDegree[e.To]--
			if inDegree[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}

	return level
}

func buildExpansionCloneID(
	parentNodeName string,
	itemIndex int,
	branchScope string,
	sourceNodeName string,
) string {
	if branchScope != "" {
		return fmt.Sprintf("%s::item::%d::branch::%s::%s", parentNodeName, itemIndex, branchScope, sourceNodeName)
	}
	return fmt.Sprintf("%s::item::%d::%s", parentNodeName, itemIndex, sourceNodeName)
}
