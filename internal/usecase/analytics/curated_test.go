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

	if len(got.Edges[0].Labels) != 1 || got.Edges[0].Labels[0] != "MCP/протоколы" {
		t.Errorf("edge a→b labels = %v, want [MCP/протоколы]", got.Edges[0].Labels)
	}
	if len(got.Edges[1].Labels) != 0 {
		t.Errorf("edge b→c labels = %v, want none", got.Edges[1].Labels)
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
	if len(got.Edges[0].Labels) != 1 || got.Edges[0].Labels[0] != "обратная" {
		t.Fatalf("labels = %v, want the link matched regardless of direction", got.Edges[0].Labels)
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

// Seven pairs in the live config carry two or three labels each: one connection
// described from several angles. ai-agents-tools ↔ security is written three
// times — prompt injection, the AI-SAFE levels, OAuth for MCP. Keeping one
// label per pair would drop nine of the thirty-two without a word.
func TestLabelEdges_keepsEveryLabelOfAPair(t *testing.T) {
	g := analytics.Graph{Edges: []analytics.GraphEdge{{From: "ai-agents-tools", To: "security", Weight: 5}}}
	labels := []analytics.CuratedLink{
		{From: "ai-agents-tools", To: "security", Label: "prompt injection"},
		{From: "ai-agents-tools", To: "security", Label: "AI-SAFE 5 уровней"},
		{From: "security", To: "ai-agents-tools", Label: "OAuth для MCP"},
	}

	got := analytics.LabelEdges(g, labels)

	if len(got.Edges[0].Labels) != 3 {
		t.Fatalf("labels = %v, want all three kept", got.Edges[0].Labels)
	}
	if len(got.UnplacedLinks) != 0 {
		t.Fatalf("UnplacedLinks = %+v, want none — every label found its edge", got.UnplacedLinks)
	}
	// Labeled counts edges, not labels; both numbers matter to a reader.
	if got.Labeled != 1 {
		t.Errorf("Labeled = %d, want 1 edge", got.Labeled)
	}
	if got.LabelCount != 3 {
		t.Errorf("LabelCount = %d, want 3 labels", got.LabelCount)
	}
}
