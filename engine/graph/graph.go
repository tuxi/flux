package graph

import (
	"flux/definition"
	"fmt"
)

// Graph DAG 的基础结构
type Graph struct {
	Nodes map[string]*definition.NodeDefinition

	Parents  map[string][]string
	Children map[string][]string

	Edges map[string][]definition.EdgeDefinition
}

func Build(def *definition.WorkflowDefinition) (*Graph, error) {

	g := &Graph{
		Nodes:    map[string]*definition.NodeDefinition{},
		Parents:  map[string][]string{},
		Children: map[string][]string{},
		Edges:    map[string][]definition.EdgeDefinition{},
	}

	// nodes
	for i := range def.Nodes {
		n := &def.Nodes[i]

		if _, ok := g.Nodes[n.Name]; ok {
			return nil, fmt.Errorf("duplicate node: %s", n.Name)
		}

		g.Nodes[n.Name] = n
	}

	// edges
	for _, e := range def.Edges {

		if _, ok := g.Nodes[e.From]; !ok {
			return nil, fmt.Errorf("edge from node not exist: %s", e.From)
		}

		if _, ok := g.Nodes[e.To]; !ok {
			return nil, fmt.Errorf("edge to node not exist: %s", e.To)
		}

		g.Children[e.From] = append(g.Children[e.From], e.To)
		g.Parents[e.To] = append(g.Parents[e.To], e.From)

		g.Edges[e.From] = append(g.Edges[e.From], e)
	}

	return g, nil
}
