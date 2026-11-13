package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/tablefile"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func writeNiches(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ниши.csv")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadNicheFile(t *testing.T) {
	p := writeNiches(t, "Ниша;Продажи, шт;Рост к году, %;Остатки\nКуртки зимние мужские;182 000;12%;51000\nГирлянды;много;5%;1\n")
	tb, err := readNicheFile(p, nil)
	if err != nil {
		t.Fatalf("readNicheFile: %v", err)
	}
	if tb.File != "ниши.csv" || tb.Path != p || tb.NeedName {
		t.Errorf("file = %q, path = %q, needName = %v", tb.File, tb.Path, tb.NeedName)
	}
	if len(tb.Fields) != 7 || tb.Fields[0].ID != "name" || tb.Fields[0].Label == "" {
		t.Errorf("Fields = %+v, want every niche column with a label", tb.Fields)
	}
	if len(tb.Columns) != 4 || tb.Columns[3].Field != "" || tb.Columns[1].Field != "sales" {
		t.Errorf("Columns = %+v", tb.Columns)
	}
	if len(tb.Niches) != 1 || tb.Niches[0].Name != "Куртки зимние мужские" {
		t.Fatalf("Niches = %+v", tb.Niches)
	}
	if v := tb.Niches[0].Values; v["sales"] != 182000 || v["growth"] != 0.12 || len(v) != 2 {
		t.Errorf("values = %v, want only sales and growth", v)
	}
	if len(tb.Problems) != 1 || tb.Problems[0].Row != 3 {
		t.Errorf("Problems = %+v, want row 3", tb.Problems)
	}
}

func TestReadNicheFile_asksForName(t *testing.T) {
	p := writeNiches(t, "Товарная группа;Продажи;Продавцы\nВарежки вязаные;58000;520\n")
	tb, err := readNicheFile(p, nil)
	if err != nil {
		t.Fatalf("missing name column is a question, not an error: %v", err)
	}
	if !tb.NeedName || len(tb.Columns) != 3 || len(tb.Niches) != 0 {
		t.Fatalf("needName = %v, columns = %d, niches = %d", tb.NeedName, len(tb.Columns), len(tb.Niches))
	}
	tb, err = readNicheFile(p, []nichePick{{Index: 0, Field: "name"}, {Index: 2, Field: ""}})
	if err != nil {
		t.Fatalf("with picks: %v", err)
	}
	if tb.NeedName || len(tb.Niches) != 1 || tb.Columns[2].Field != "" {
		t.Errorf("picks not applied: needName = %v, niches = %d, columns = %+v", tb.NeedName, len(tb.Niches), tb.Columns)
	}
	if _, err := readNicheFile(p, []nichePick{{Index: 0, Field: "stock"}}); err == nil {
		t.Error("unknown field picked without error")
	}
	if _, err := readNicheFile(p, []nichePick{{Index: 0, Field: "name"}, {Index: 1, Field: "name"}}); !errors.Is(err, markets.ErrDuplicatePick) {
		t.Errorf("duplicate pick: err = %v", err)
	}
}

func TestReadNicheFile_refuses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ниши.pdf")
	if err := os.WriteFile(p, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readNicheFile(p, nil); !errors.Is(err, tablefile.ErrUnsupported) {
		t.Errorf("pdf: err = %v, want ErrUnsupported", err)
	}
}

func TestKnownModules_haveMarkets(t *testing.T) {
	if !slices.Contains(knownModules, desktopconfig.ModuleMarkets) {
		t.Error("window does not know the markets module")
	}
}
