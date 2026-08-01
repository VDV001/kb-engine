package analytics_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/analytics"
)

// The catalog computes which categories are connected; the owner's config says
// what a connection MEANS. Neither replaces the other, and until now only the
// first reached the screen: 32 hand-written labels such as «MCP/протоколы» were
// read by nothing.
func TestLabelEdges(t *testing.T) {
	g := analytics.Graph{
		Nodes: []analytics.GraphNode{{Category: "a", Count: 3}, {Category: "b", Count: 2}},
		Edges: []analytics.GraphEdge{
			{From: "a", To: "b", Weight: 9},
			{From: "b", To: "c", Weight: 4},
		},
	}
	labels := []analytics.CuratedLink{
		{From: "a", To: "b", Label: "MCP/протоколы"},
	}

	got := analytics.LabelEdges(g, labels)

	if got.Edges[0].Label != "MCP/протоколы" {
		t.Errorf("edge a→b label = %q, want %q", got.Edges[0].Label, "MCP/протоколы")
	}
	if got.Edges[1].Label != "" {
		t.Errorf("edge b→c label = %q, want empty", got.Edges[1].Label)
	}
	// The counts are what keeps the screen honest: a graph showing two labels
	// out of 245 edges must be able to say so, or the reader assumes the whole
	// topology is curated.
	if got.Labeled != 1 || got.Unlabeled != 1 {
		t.Errorf("Labeled/Unlabeled = %d/%d, want 1/1", got.Labeled, got.Unlabeled)
	}
}

// The config writes a direction; the computed graph orders each pair
// alphabetically. A label written b→a must still find the edge a→b, or a third
// of the labels silently miss.
func TestLabelEdges_matchesRegardlessOfDirection(t *testing.T) {
	g := analytics.Graph{Edges: []analytics.GraphEdge{{From: "a", To: "b", Weight: 1}}}
	labels := []analytics.CuratedLink{{From: "b", To: "a", Label: "обратная"}}

	got := analytics.LabelEdges(g, labels)
	if got.Edges[0].Label != "обратная" {
		t.Fatalf("label = %q, want it matched regardless of direction", got.Edges[0].Label)
	}
}

// A curated link whose pair is absent from the computed graph must be reported,
// not dropped: it is a claim by the owner that the engine could not place.
func TestLabelEdges_reportsUnplacedLinks(t *testing.T) {
	g := analytics.Graph{Edges: []analytics.GraphEdge{{From: "a", To: "b", Weight: 1}}}
	labels := []analytics.CuratedLink{
		{From: "a", To: "b", Label: "есть"},
		{From: "x", To: "y", Label: "негде разместить"},
	}

	got := analytics.LabelEdges(g, labels)
	if len(got.UnplacedLinks) != 1 || got.UnplacedLinks[0].Label != "негде разместить" {
		t.Fatalf("UnplacedLinks = %+v, want the one link with no matching edge", got.UnplacedLinks)
	}
}
