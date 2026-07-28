package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// workbook writes a miniature Учёт_финансов.xlsx: title row, header row, data
// from row 3. Two expenses and one income, one of them with kopecks.
func workbook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetSheetName("Sheet1", "Расходы"))
	must(f.SetCellValue("Расходы", "A2", "Дата"))
	must(f.SetCellValue("Расходы", "A3", time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Расходы", "B3", "Еда"))
	must(f.SetCellValue("Расходы", "F3", 202.45))
	must(f.SetCellValue("Расходы", "A4", time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Расходы", "B4", "Транспорт"))
	must(f.SetCellValue("Расходы", "F4", 1500))

	_, err := f.NewSheet("Доходы")
	must(err)
	must(f.SetCellValue("Доходы", "A2", "Дата"))
	must(f.SetCellValue("Доходы", "A3", time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)))
	must(f.SetCellValue("Доходы", "B3", "Зарплата"))
	must(f.SetCellValue("Доходы", "D3", 90000))

	path := filepath.Join(t.TempDir(), "Учёт_финансов.xlsx")
	must(f.SaveAs(path))
	return path
}

func finImport(t *testing.T, xlsx, ledger string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "import", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin import exit = %d, stderr = %s", code, errb.String())
	}
	return out.String()
}

func TestRun_finImport(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	out := finImport(t, workbook(t), ledger)

	// The counts are the reconciliation: they are what gets compared against the
	// previous build before the spreadsheet stops being the source of truth.
	for _, want := range []string{"2 expense", "1 income"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should report %q for reconciliation:\n%s", want, out)
		}
	}

	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ledger has %d lines, want 3:\n%s", len(lines), raw)
	}
	// Chronological, so the file reads like the ledger it replaces.
	if !strings.Contains(lines[0], `"date":"2026-03-29"`) || !strings.Contains(lines[2], `"date":"2026-04-05"`) {
		t.Errorf("ledger is not in date order:\n%s", raw)
	}
	if !strings.Contains(lines[0], `"amount":"202.45"`) {
		t.Errorf("kopecks did not survive the import:\n%s", lines[0])
	}
}

// Import is a one-time migration. Pointing it at a ledger that already exists
// is a mistake, and the cost of guessing wrong is the whole file.
func TestRun_finImport_refusesToOverwrite(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	xlsx := workbook(t)
	finImport(t, xlsx, ledger)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "import", "--from", xlsx, "--ledger", ledger}, &out, &errb); code == 0 {
		t.Error("second import over an existing ledger should fail")
	}
	if !strings.Contains(errb.String(), "already exists") {
		t.Errorf("error should say the ledger already exists, got: %s", errb.String())
	}
}

func TestRun_finAdd(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, workbook(t), ledger)

	var out, errb bytes.Buffer
	code := run([]string{
		"fin", "add", "--ledger", ledger,
		"--amount", "1 500,50", "--cat", "Еда", "--note", "продукты", "--date", "2026-04-06",
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("fin add exit = %d, stderr = %s", code, errb.String())
	}

	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("ledger has %d lines, want 4:\n%s", len(lines), raw)
	}
	added := lines[3]
	// Grouping spaces and a comma are how the amount is written by hand.
	if !strings.Contains(added, `"amount":"1500.50"`) {
		t.Errorf("amount was not parsed as typed:\n%s", added)
	}
	if !strings.Contains(added, `"description":"продукты"`) || !strings.Contains(added, `"category":"Еда"`) {
		t.Errorf("added line lost a field:\n%s", added)
	}
	if !strings.Contains(added, `"rev":1`) {
		t.Errorf("a new entry must be revision 1:\n%s", added)
	}
}

// An amount finer than a kopeck is a typo, and the ledger is the wrong place to
// round it away quietly.
func TestRun_finAdd_rejectsSubKopeckAmount(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, workbook(t), ledger)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--amount", "10.005", "--cat", "Еда"}, &out, &errb); code == 0 {
		t.Error("a sub-kopeck amount should be rejected")
	}
}

// Adding to a ledger that is not there must fail rather than create one: a
// mistyped path silently starting a second ledger is how transactions go
// missing.
func TestRun_finAdd_missingLedgerIsAnError(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "nope.jsonl")
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--amount", "100", "--cat", "Еда"}, &out, &errb); code == 0 {
		t.Error("adding to a missing ledger should fail")
	}
	if _, err := os.Stat(ledger); !os.IsNotExist(err) {
		t.Error("a failed add must not create the ledger")
	}
}

func TestRun_finList(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, workbook(t), ledger)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "list", "--ledger", ledger, "--year", "2026", "--month", "3"}, &out, &errb); code != 0 {
		t.Fatalf("fin list exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "202.45") || !strings.Contains(out.String(), "Еда") {
		t.Errorf("March expense missing from the listing:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Транспорт") {
		t.Errorf("April expense should be filtered out:\n%s", out.String())
	}
}

func TestRun_finReport(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, workbook(t), ledger)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "report", "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin report exit = %d, stderr = %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"1702.45", "90000.00", "88297.55", "Транспорт", "Еда"} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
}

func TestRun_fin_requiresLedger(t *testing.T) {
	for _, sub := range []string{"import", "add", "list", "report"} {
		t.Run(sub, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run([]string{"fin", sub}, &out, &errb); code != 2 {
				t.Errorf("%s without --ledger exit = %d, want 2", sub, code)
			}
		})
	}
}

func TestRun_fin_unknownSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "reconcile"}, &out, &errb); code != 2 {
		t.Errorf("exit = %d, want 2 for a subcommand that does not exist", code)
	}
}

// --init is what pairs the two files: every row gets an id, the workbook
// learns it, and the ledger is written from the same pass. Without that single
// pass the two sides have no way to recognize each other's rows.
func TestRun_finSyncInit(t *testing.T) {
	xlsx := workbook(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--init", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin sync --init exit = %d, stderr = %s", code, errb.String())
	}

	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ledger has %d lines, want 3:\n%s", len(lines), raw)
	}

	// The same ids on both sides is the entire point.
	ledgerIDs := map[string]bool{}
	for _, line := range lines {
		var rec struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if len(rec.ID) != 26 {
			t.Errorf("id %q is not a ULID", rec.ID)
		}
		ledgerIDs[rec.ID] = true
	}
	for _, ref := range []struct{ sheet, cell string }{
		{"Расходы", "H3"}, {"Расходы", "H4"}, {"Доходы", "E3"},
	} {
		got := sheetCell(t, xlsx, ref.sheet, ref.cell)
		if !ledgerIDs[got] {
			t.Errorf("%s!%s = %q, which is not one of the ledger ids", ref.sheet, ref.cell, got)
		}
	}
}

// Re-running after the ledger is removed must not mint new ids: the workbook
// already carries them, and reassigning would orphan every reference to the
// old ones.
func TestRun_finSyncInit_reusesIDsAlreadyInTheWorkbook(t *testing.T) {
	xlsx := workbook(t)
	dir := t.TempDir()
	ledger := filepath.Join(dir, "transactions.jsonl")

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--init", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("first init exit = %d, stderr = %s", code, errb.String())
	}
	first := sheetCell(t, xlsx, "Расходы", "H3")

	if err := os.Remove(ledger); err != nil {
		t.Fatalf("remove ledger: %v", err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"fin", "sync", "--init", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("second init exit = %d, stderr = %s", code, errb.String())
	}
	if second := sheetCell(t, xlsx, "Расходы", "H3"); second != first {
		t.Errorf("id changed between runs: %q → %q", first, second)
	}
}

// An existing ledger may hold entries made with fin add that the workbook has
// never seen. Re-pairing would silently drop them, so init refuses and leaves
// the decision to a person.
func TestRun_finSyncInit_refusesAnExistingLedger(t *testing.T) {
	xlsx := workbook(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, xlsx, ledger)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--init", "--from", xlsx, "--ledger", ledger}, &out, &errb); code == 0 {
		t.Error("init over an existing ledger should fail")
	}
	if !strings.Contains(errb.String(), "already exists") {
		t.Errorf("error should say the ledger already exists, got: %s", errb.String())
	}
}

// Two-way sync is the next piece of work. Until it exists, saying so beats
// pretending the flag is optional.
func TestRun_finSync_withoutInitIsNotImplemented(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--ledger", "x", "--from", "y"}, &out, &errb); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--init") {
		t.Errorf("error should point at --init, got: %s", errb.String())
	}
}

func sheetCell(t *testing.T, path, sheet, cell string) string {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	defer func() { _ = f.Close() }()
	v, err := f.GetCellValue(sheet, cell)
	if err != nil {
		t.Fatalf("read %s!%s: %v", sheet, cell, err)
	}
	return v
}
