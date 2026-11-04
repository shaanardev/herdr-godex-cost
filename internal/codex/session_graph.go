package codex

import "slices"

type sessionGraph struct {
	children map[string]map[string]struct{}
}

func (graph *sessionGraph) addEdge(parentID, childID string) {
	if graph.children == nil {
		graph.children = make(map[string]map[string]struct{})
	}
	if graph.children[parentID] == nil {
		graph.children[parentID] = make(map[string]struct{})
	}
	graph.children[parentID][childID] = struct{}{}
}

func (graph sessionGraph) childrenOf(parentID string) []string {
	children := make([]string, 0, len(graph.children[parentID]))
	for childID := range graph.children[parentID] {
		children = append(children, childID)
	}
	slices.Sort(children)
	return children
}
