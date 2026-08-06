package financexlsx_test

import (
	"archive/zip"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// A worksheet declares the range it uses in <dimension ref="…">. Readers that
// stream the file — openpyxl's read_only mode is the one the owner reaches for —
// trust that declaration instead of scanning the cells, so a stale ref makes
// rows past it invisible without any error anywhere.
//
// The live workbook was in exactly that state: the sheet declared A1:C7 while
// holding 8 rows, and the account row the engine had appended did not exist for
// a reader in that mode. The engine was told the debt was not in the book.
//
// The XML is read here rather than asked of excelize on purpose. excelize
// ignores the declaration on read, so a check that went through it would agree
// with itself and pass on a file no one else can read whole.
func declaredCovers(t *testing.T, path string) []string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s as zip: %v", path, err)
	}
	defer func() { _ = zr.Close() }()

	dimRe := regexp.MustCompile(`<dimension ref="([^"]+)"`)
	rowRe := regexp.MustCompile(`<row r="(\d+)"`)
	cellRe := regexp.MustCompile(`<c r="([A-Z]+)(\d+)"`)

	var complaints []string
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}

		// The furthest cell the sheet actually holds.
		var maxRow, maxCol int
		for _, m := range cellRe.FindAllStringSubmatch(string(body), -1) {
			col, err := excelize.ColumnNameToNumber(m[1])
			if err != nil {
				t.Fatalf("%s: column %q: %v", f.Name, m[1], err)
			}
			row := 0
			for _, ch := range m[2] {
				row = row*10 + int(ch-'0')
			}
			maxCol = max(maxCol, col)
			maxRow = max(maxRow, row)
		}
		if maxRow == 0 {
			continue // an empty sheet declares nothing worth checking
		}
		if rows := rowRe.FindAllStringSubmatch(string(body), -1); len(rows) == 0 {
			continue
		}

		decl := dimRe.FindStringSubmatch(string(body))
		if decl == nil {
			complaints = append(complaints, f.Name+": no <dimension> at all")
			continue
		}
		_, end, ok := strings.Cut(decl[1], ":")
		if !ok {
			end = decl[1]
		}
		endCol, endRow, err := excelize.CellNameToCoordinates(end)
		if err != nil {
			t.Fatalf("%s: dimension %q: %v", f.Name, decl[1], err)
		}
		if endRow < maxRow || endCol < maxCol {
			complaints = append(complaints, fmt.Sprintf(
				"%s: declares %s but holds cells out to row %d, column %d",
				f.Name, decl[1], maxRow, maxCol))
		}
	}
	return complaints
}

// Every writer goes through saveAtomically, so every writer is checked here.
// A guard that only covers the path someone happened to test is a guard with an
// undocumented bypass — the next command added would quietly skip it.
func TestWriters_declareTheRangeTheyWrote(t *testing.T) {
	appended := tx(t, "01D", domain.KindExpense,
		time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC), 41800, "Транспорт", "такси")

	for _, tc := range []struct {
		name string
		// setup builds the state this writer needs; nil means the plain fixture.
		setup func(t *testing.T) string
		write func(t *testing.T, path string) error
	}{
		{
			name:  "ApplyRows appends a row",
			setup: paired,
			write: func(_ *testing.T, path string) error {
				return financexlsx.ApplyRows(path, []domain.Transaction{appended}, nil, writeClock)
			},
		},
		{
			name: "AddAccount appends to Счета",
			write: func(_ *testing.T, path string) error {
				return financexlsx.AddAccount(path, "Долг → Отец", domain.NewMoney(300000), writeClock)
			},
		},
		{
			name: "SetBalance updates an existing account",
			write: func(_ *testing.T, path string) error {
				return financexlsx.SetBalance(path, "Сбербанк", domain.NewMoney(1234500), writeClock)
			},
		},
		{
			name: "AssignIDs adds the id column",
			write: func(_ *testing.T, path string) error {
				return financexlsx.AssignIDs(path, map[string]string{"expense-r3": "01A"}, writeClock)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := tc.setup
			if setup == nil {
				setup = workbookWithExtraColumn
			}
			path := setup(t)
			if err := tc.write(t, path); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := declaredCovers(t, path); len(got) != 0 {
				t.Errorf("a streaming reader loses rows:\n  %s", strings.Join(got, "\n  "))
			}
		})
	}
}

// A book whose declaration was already stale before the engine touched it gets
// fixed by the write, not preserved. The live workbook arrived in that state —
// 1178 declared against 1197 rows held — and it is not the owner's job to know
// which reader mode is safe on which day.
func TestWriters_repairADeclarationThatWasAlreadyStale(t *testing.T) {
	path := workbookWithExtraColumn(t)
	staleTo(t, path, "Расходы", "A1:B2")

	if got := declaredCovers(t, path); len(got) == 0 {
		t.Fatal("fixture is not stale — the test would pass without the fix")
	}
	if err := financexlsx.SetBalance(path, "Сбербанк", domain.NewMoney(1234500), writeClock); err != nil {
		t.Fatalf("SetBalance: %v", err)
	}
	if got := declaredCovers(t, path); len(got) != 0 {
		t.Errorf("stale declaration survived a write:\n  %s", strings.Join(got, "\n  "))
	}
}

// staleTo rewrites a sheet's declaration to a range that no longer covers it,
// reproducing the state the live workbook was found in.
func staleTo(t *testing.T, path, sheet, ref string) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if err := f.SetSheetDimension(sheet, ref); err != nil {
		t.Fatalf("set dimension: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
