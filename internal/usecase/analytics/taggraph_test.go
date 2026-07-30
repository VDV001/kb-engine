package analytics_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
)

func graphCatalog(t *testing.T) *domain.Catalog {
	t.Helper()
	mk := func(id int, category string, tags ...string) domain.Entry {
		t.Helper()
		cat, err := domain.NewCategory(category)
		if err != nil {
			t.Fatal(err)
		}
		lc, _ := domain.NewLifecycle("active")
		rs, _ := domain.NewReadState("read")
		e, err := domain.NewEntry(domain.EntryParams{
			ID: id, Kind: "article", Title: "t", Category: cat, Lifecycle: lc, Tags: tags, ReadState: &rs,
		})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	c, err := domain.NewCatalog([]domain.Entry{
		// go и agents делят два тега, go и meta — один, agents и meta — ни одного.
		mk(1, "golang", "concurrency", "tooling"),
		mk(2, "golang", "stdlib"),
		mk(3, "ai-agents-tools", "tooling", "concurrency", "mcp"),
		mk(4, "meta", "stdlib"),
		mk(5, "no-tags"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The knowledge graph is algorithmic: nodes are categories, an edge exists
// where two categories share tags, and its weight is how many distinct tags
// they share. Nothing is hand-drawn, so a new entry with a bridging tag
// reshapes the graph on the next read.
func TestTagGraph(t *testing.T) {
	g := analytics.TagGraph(graphCatalog(t))

	if len(g.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4 (categories, including the tagless one)", len(g.Nodes))
	}
	// Крупнейшая категория первой — так дашборд ставит её в центр.
	if g.Nodes[0].Category != "golang" || g.Nodes[0].Count != 2 {
		t.Errorf("nodes[0] = %+v, want golang/2", g.Nodes[0])
	}

	find := func(a, b string) int {
		t.Helper()
		for _, e := range g.Edges {
			if (e.From == a && e.To == b) || (e.From == b && e.To == a) {
				return e.Weight
			}
		}
		return 0
	}
	if w := find("golang", "ai-agents-tools"); w != 2 {
		t.Errorf("golang↔ai-agents-tools weight = %d, want 2 (concurrency, tooling)", w)
	}
	if w := find("golang", "meta"); w != 1 {
		t.Errorf("golang↔meta weight = %d, want 1 (stdlib)", w)
	}
	if w := find("ai-agents-tools", "meta"); w != 0 {
		t.Errorf("ai-agents-tools↔meta weight = %d, want no edge", w)
	}
	// Каждая пара — одним ребром, не двумя направлениями.
	if len(g.Edges) != 2 {
		t.Errorf("edges = %d, want 2", len(g.Edges))
	}
}
