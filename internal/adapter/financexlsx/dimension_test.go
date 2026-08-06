package financexlsx_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/adapter/xlsxdim/xlsxdimtest"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

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
			if got := xlsxdimtest.File(t, path); len(got) != 0 {
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

	if got := xlsxdimtest.File(t, path); len(got) == 0 {
		t.Fatal("fixture is not stale — the test would pass without the fix")
	}
	if err := financexlsx.SetBalance(path, "Сбербанк", domain.NewMoney(1234500), writeClock); err != nil {
		t.Fatalf("SetBalance: %v", err)
	}
	if got := xlsxdimtest.File(t, path); len(got) != 0 {
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
