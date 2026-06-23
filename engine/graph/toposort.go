package graph

import "fmt"

// TopoSort 拓普排序
func TopoSort(g *Graph) ([]string, error) {

	inDegree := map[string]int{}

	for n := range g.Nodes {
		inDegree[n] = 0
	}

	// 统计parent 入度
	for child, parents := range g.Parents {
		inDegree[child] = len(parents)
	}

	queue := []string{}

	for n, d := range inDegree {
		if d == 0 {
			queue = append(queue, n)
		}
	}

	var result []string

	for len(queue) > 0 {

		n := queue[0]
		queue = queue[1:]

		result = append(result, n)

		for _, child := range g.Children[n] {

			inDegree[child]--

			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	if len(result) != len(g.Nodes) {
		return nil, fmt.Errorf("toposort failed")
	}

	return result, nil
}
