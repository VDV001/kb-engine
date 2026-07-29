package financexlsx_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/xuri/excelize/v2"
)

// paired builds the fixture workbook and gives every row an id, which is the
// state every sync starts from.
func paired(t *testing.T) string {
	t.Helper()
	path := workbookWithExtraColumn(t)
	if err := financexlsx.AssignIDs(path, map[string]string{
		"expense-r3": "01A", "expense-r4": "01B", "income-r3": "01C",
	}, writeClock); err != nil {
		t.Fatalf("AssignIDs: %v", err)
	}
	return path
}

func tx(t *testing.T, id, kind string, date time.Time, kopecks int64, category, note string) domain.Transaction {
	t.Helper()
	out, err := domain.NewTransaction(domain.TransactionParams{
		ID:          id,
		Kind:        kind,
		Date:        date,
		Amount:      domain.NewMoney(kopecks),
		Category:    category,
		Description: note,
		Now:         writeClock,
	})
	if err != nil {
		t.Fatalf("build transaction: %v", err)
	}
	return out
}

func readBack(t *testing.T, path string) map[string]domain.Transaction {
	t.Helper()
	led, err := financexlsx.Read(path, writeClock)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	out := map[string]domain.Transaction{}
	for _, tx := range led.Transactions {
		out[tx.ID()] = tx
	}
	return out
}

func TestApplyRows_updatesInPlace(t *testing.T) {
	path := paired(t)
	changed := tx(t, "01A", domain.KindExpense,
		time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), 99900, "Еда", "переписано")

	if err := financexlsx.ApplyRows(path, []domain.Transaction{changed}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	got := readBack(t, path)
	if len(got) != 3 {
		t.Fatalf("workbook has %d rows, want 3 — an update must not add one", len(got))
	}
	if a := got["01A"]; a.Amount().Kopecks() != 99900 || a.Description() != "переписано" {
		t.Errorf("row 01A = %s / %q, want 999.00 / переписано", a.Amount(), a.Description())
	}
	// The undocumented bank column belongs to the owner, not to the sync.
	if v := cellValue(t, path, "Расходы", "H4"); v != "Сбербанк" {
		t.Errorf("column H = %q, want it untouched", v)
	}
}

func TestApplyRows_appendsNewRows(t *testing.T) {
	path := paired(t)
	added := tx(t, "01D", domain.KindExpense,
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), 33300, "Подписки", "новая строка")

	if err := financexlsx.ApplyRows(path, []domain.Transaction{added}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	got := readBack(t, path)
	if len(got) != 4 {
		t.Fatalf("workbook has %d rows, want 4", len(got))
	}
	a, ok := got["01D"]
	if !ok {
		t.Fatal("the appended row did not come back")
	}
	if a.Amount().Kopecks() != 33300 || a.Category() != "Подписки" {
		t.Errorf("appended row = %s / %q", a.Amount(), a.Category())
	}
	if !a.Date().Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("appended date = %s, want 2026-05-01", a.Date().Format(time.DateOnly))
	}
}

// A new row inherits the formatting of the rows above it. Without that the
// owner opens the sheet to a date rendered as 46110 and an amount with no
// currency, which reads as the tool having broken the file.
func TestApplyRows_appendedRowInheritsFormatting(t *testing.T) {
	path := paired(t)
	added := tx(t, "01D", domain.KindExpense,
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), 33300, "Подписки", "")
	if err := financexlsx.ApplyRows(path, []domain.Transaction{added}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()

	for _, col := range []string{"A", "F"} {
		want, err := f.GetCellStyle("Расходы", col+"4")
		if err != nil {
			t.Fatalf("style of %s4: %v", col, err)
		}
		got, err := f.GetCellStyle("Расходы", col+"5")
		if err != nil {
			t.Fatalf("style of %s5: %v", col, err)
		}
		if got != want {
			t.Errorf("column %s: appended row style = %d, want %d (inherited from the row above)", col, got, want)
		}
	}
}

// Formatting is inherited from the last row that has data, not from the last
// row of the sheet.
//
// The live ledger has blank rows after the data — 1156 rows for 507 records —
// so taking the style from the row immediately above lands on an unformatted
// one, and the appended amount comes out as 1234.56 with no currency. That is
// what a person sees when they open the file and conclude the tool broke it.
func TestApplyRows_inheritsFormattingAcrossTrailingBlankRows(t *testing.T) {
	path := paired(t)
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Two rows that carry formatting but no value — which is exactly why the live
	// sheet reports 1156 rows for 507 records.
	blank, err := f.NewStyle(&excelize.Style{Border: []excelize.Border{{Type: "bottom", Style: 1}}})
	if err != nil {
		t.Fatalf("style: %v", err)
	}
	if err := f.SetCellStyle("Расходы", "A5", "I6", blank); err != nil {
		t.Fatalf("pad: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = f.Close()

	added := tx(t, "01D", domain.KindExpense,
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), 33300, "Подписки", "")
	if err := financexlsx.ApplyRows(path, []domain.Transaction{added}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	g, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = g.Close() }()

	want, err := g.GetCellStyle("Расходы", "F4")
	if err != nil {
		t.Fatalf("style of F4: %v", err)
	}
	rows, err := g.GetRows("Расходы")
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	appended, err := excelize.CoordinatesToCellName(6, len(rows))
	if err != nil {
		t.Fatalf("cell name: %v", err)
	}
	got, err := g.GetCellStyle("Расходы", appended)
	if err != nil {
		t.Fatalf("style of %s: %v", appended, err)
	}
	if got != want {
		t.Errorf("appended amount style = %d, want %d (from the last row with data)", got, want)
	}
}

// Removal clears the row instead of deleting it. Deleting shifts every row
// below, which moves data out from under the formulas on Сводка; a blank row
// mid-sheet is something this ledger already contains and the reader skips.
func TestApplyRows_removalClearsWithoutShifting(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path, nil, []string{"01A"}, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}

	got := readBack(t, path)
	if _, still := got["01A"]; still {
		t.Error("removed row is still readable")
	}
	if len(got) != 2 {
		t.Errorf("workbook has %d rows, want 2", len(got))
	}
	// 01B stayed where it was: nothing shifted up into row 3.
	if v := cellValue(t, path, "Расходы", "B4"); v != "Транспорт" {
		t.Errorf("Расходы!B4 = %q, want Транспорт — rows shifted", v)
	}
	if v := cellValue(t, path, "Расходы", "I3"); v != "" {
		t.Errorf("Расходы!I3 = %q, want the id cleared along with the row", v)
	}
}

func TestApplyRows_refusesALockedWorkbook(t *testing.T) {
	path := paired(t)
	lock := filepath.Join(filepath.Dir(path), ".~lock."+filepath.Base(path)+"#")
	if err := os.WriteFile(lock, []byte("daniil"), 0o600); err != nil {
		t.Fatalf("create lock: %v", err)
	}
	changed := tx(t, "01A", domain.KindExpense,
		time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), 99900, "Еда", "")
	if err := financexlsx.ApplyRows(path, []domain.Transaction{changed}, nil, writeClock); !errors.Is(err, financexlsx.ErrWorkbookLocked) {
		t.Errorf("ApplyRows() error = %v, want ErrWorkbookLocked", err)
	}
}

func TestApplyRows_backsUpBeforeWriting(t *testing.T) {
	path := paired(t)
	changed := tx(t, "01A", domain.KindExpense,
		time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), 99900, "Еда", "")
	if err := financexlsx.ApplyRows(path, []domain.Transaction{changed}, nil, writeClock); err != nil {
		t.Fatalf("ApplyRows: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".backup", "*.xlsx"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("no backup was taken (err %v)", err)
	}
}

// Without an id column there is no way to tell which row an update refers to,
// and guessing by position is what the ids exist to replace.
func TestApplyRows_refusesAWorkbookWithoutIDs(t *testing.T) {
	path := workbookWithExtraColumn(t)
	changed := tx(t, "01A", domain.KindExpense,
		time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), 99900, "Еда", "")
	if err := financexlsx.ApplyRows(path, []domain.Transaction{changed}, nil, writeClock); err == nil {
		t.Error("expected a refusal on a workbook that has never been paired")
	}
}

// Removing an id the workbook does not have means the two sides disagree about
// what exists. Carrying on would write half the change.
func TestApplyRows_refusesAnUnknownRemoval(t *testing.T) {
	path := paired(t)
	if err := financexlsx.ApplyRows(path, nil, []string{"01Z"}, writeClock); err == nil {
		t.Error("expected a refusal for an id that is not in the workbook")
	}
	if v := cellValue(t, path, "Расходы", "I3"); v != "01A" {
		t.Error("a refused write must leave the workbook untouched")
	}
}
