package analytics

// pairKey orders a pair so a link and an edge describing the same connection
// hash to the same key regardless of the direction each was written in.
func pairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// CuratedLink is a connection the owner wrote down by hand, together with what
// it means. The catalog can say that two categories are connected; only this
// says why.
type CuratedLink struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

// LabelEdges puts the owner's labels onto the computed graph and reports what
// it could not place.
//
// The two counts are not decoration. The live graph has 245 computed edges and
// 32 curated labels; without saying so, a reader seeing a few labelled edges
// assumes the whole topology was drawn deliberately.
func LabelEdges(g Graph, links []CuratedLink) Graph {
	// Keyed on the unordered pair: the config writes a direction, TagGraph
	// orders each pair alphabetically, and matching only one way would lose
	// every label written the other.
	byPair := make(map[[2]string][]string, len(links))
	for _, l := range links {
		byPair[pairKey(l.From, l.To)] = append(byPair[pairKey(l.From, l.To)], l.Label)
	}

	placed := make(map[[2]string]bool, len(links))
	for i, e := range g.Edges {
		key := pairKey(e.From, e.To)
		labels, ok := byPair[key]
		if !ok {
			g.Unlabeled++
			continue
		}
		g.Edges[i].Labels = labels
		g.Labeled++
		g.LabelCount += len(labels)
		placed[key] = true
	}

	for _, l := range links {
		if !placed[pairKey(l.From, l.To)] {
			g.UnplacedLinks = append(g.UnplacedLinks, l)
		}
	}
	return g
}
