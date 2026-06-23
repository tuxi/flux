package graph

import "fmt"

// DetectCycle 检查环
func DetectCycle(g *Graph) error {

	visited := map[string]bool{}
	stack := map[string]bool{}

	for node := range g.Nodes {

		if !visited[node] {

			if dfs(node, g, visited, stack) {
				return fmt.Errorf("cycle detected")
			}
		}
	}

	return nil
}

func dfs(node string, g *Graph, visited map[string]bool, stack map[string]bool) bool {

	visited[node] = true
	stack[node] = true

	for _, child := range g.Children[node] {

		if !visited[child] {
			if dfs(child, g, visited, stack) {
				return true
			}
		} else if stack[child] {
			return true
		}
	}

	stack[node] = false

	return false
}
