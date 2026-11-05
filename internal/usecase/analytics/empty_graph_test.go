package analytics_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
)

// Граф пустой базы и базы без общих тегов — пустые списки, а не null: экран
// перебирает nodes и edges, и null ронял его целиком («e.edges is not
// iterable»). У нового человека на сервере база пустая — окно не открылось бы.
func TestGraphListsAreNeverNull(t *testing.T) {
	empty, err := domain.NewCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := domain.NewCategory("golang")
	if err != nil {
		t.Fatal(err)
	}
	lc, _ := domain.NewLifecycle("active")
	rs, _ := domain.NewReadState("read")
	lone, err := domain.NewEntry(domain.EntryParams{ID: 1, Kind: "article", Title: "t", Category: cat, Lifecycle: lc, Tags: []string{"go"}, ReadState: &rs})
	if err != nil {
		t.Fatal(err)
	}
	single, err := domain.NewCatalog([]domain.Entry{lone})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*domain.Catalog{"empty base": empty, "no shared tags": single} {
		t.Run(name, func(t *testing.T) {
			for stage, g := range map[string]analytics.Graph{
				"TagGraph":   analytics.TagGraph(c),
				"LabelEdges": analytics.LabelEdges(analytics.TagGraph(c), nil),
			} {
				raw, err := json.Marshal(g)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), ":null") {
					t.Errorf("%s: %s", stage, raw)
				}
			}
		})
	}
}
