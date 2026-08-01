package analytics

import (
	"cmp"
	"maps"
	"slices"

	"github.com/daniil/kb-engine/internal/domain"
)

// GraphNode is one category in the knowledge graph, sized by entry count.
type GraphNode struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// GraphEdge links two categories that share tags; Weight is how many distinct
// tags they share. Each pair appears once, From < To alphabetically.
type GraphEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Weight int    `json:"weight"`
	// Labels are what the owner wrote about this connection. A list, not a
	// string: seven pairs in the live config carry two or three meanings each.
	Labels []string `json:"labels,omitempty"`
}

// Graph is the knowledge topology: categories connected through shared tags.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	// Labeled and Unlabeled let the view state how much of the topology was
	// drawn by hand. Without them a few labelled edges read as a curated graph.
	Labeled   int `json:"labeled"`
	Unlabeled int `json:"unlabeled"`
	// LabelCount is how many labels there are in total — larger than Labeled
	// whenever a connection carries more than one meaning.
	LabelCount int `json:"label_count"`
	// UnplacedLinks are the owner's connections that no computed edge matched.
	UnplacedLinks []CuratedLink `json:"unplaced_links,omitempty"`
}

// TagGraph computes the graph from the catalog alone. Nothing is hand-drawn:
// a new entry whose tags bridge two categories reshapes the graph on the next
// read, which is the property the dashboard's version was asked to have.
//
// Nodes come largest-first so the view can put the biggest category in the
// centre; edges come heaviest-first so trimming to the strongest N is a slice.
func TagGraph(c *domain.Catalog) Graph {
	counts := map[string]int{}
	tagCats := map[string]map[string]struct{}{}
	for _, e := range c.Entries() {
		cat := e.Category().String()
		counts[cat]++
		for _, tag := range e.Tags() {
			if tagCats[tag] == nil {
				tagCats[tag] = map[string]struct{}{}
			}
			tagCats[tag][cat] = struct{}{}
		}
	}

	var g Graph
	for cat, n := range counts {
		g.Nodes = append(g.Nodes, GraphNode{Category: cat, Count: n})
	}
	slices.SortFunc(g.Nodes, func(a, b GraphNode) int {
		return cmp.Or(b.Count-a.Count, cmp.Compare(a.Category, b.Category))
	})

	type pair struct{ a, b string }
	weights := map[pair]int{}
	for _, cats := range tagCats {
		list := slices.Sorted(maps.Keys(cats))
		for i, a := range list {
			for _, b := range list[i+1:] {
				weights[pair{a, b}]++
			}
		}
	}
	for p, w := range weights {
		g.Edges = append(g.Edges, GraphEdge{From: p.a, To: p.b, Weight: w})
	}
	slices.SortFunc(g.Edges, func(a, b GraphEdge) int {
		return cmp.Or(b.Weight-a.Weight, cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return g
}
