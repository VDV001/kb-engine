package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// pairedLedger sets up the state every sync starts from: a workbook and a
// ledger that know each other, with a baseline recorded.
func pairedLedger(t *testing.T) (xlsx, ledger string) {
	t.Helper()
	xlsx = workbook(t)
	ledger = filepath.Join(filepath.Dir(xlsx), "transactions.jsonl")

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--init", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin sync --init exit = %d, stderr = %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ledger), ".sync-state.json")); err != nil {
		t.Fatalf("--init must record the baseline: %v", err)
	}
	return xlsx, ledger
}

func sync(t *testing.T, xlsx, ledger string, extra ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"fin", "sync", "--from", xlsx, "--ledger", ledger}, extra...)
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// editWorkbook changes an amount by hand, the way the owner would.
func editWorkbook(t *testing.T, path, sheet, cell string, amount float64) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	if err := f.SetCellValue(sheet, cell, amount); err != nil {
		t.Fatalf("edit %s!%s: %v", sheet, cell, err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	_ = f.Close()
}

func addToLedger(t *testing.T, ledger string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{
		"fin", "add", "--ledger", ledger, "--amount", "777", "--cat", "Прочее",
		"--note", "из терминала", "--date", "2026-05-01",
	}, &out, &errb); code != 0 {
		t.Fatalf("fin add exit = %d, stderr = %s", code, errb.String())
	}
}

func TestRun_finSync_nothingToDo(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	code, stdout, stderr := sync(t, xlsx, ledger)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "nothing to do") {
		t.Errorf("output = %q, want it to say there is nothing to do", stdout)
	}
}

func TestRun_finSync_ledgerWins(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger)

	code, stdout, stderr := sync(t, xlsx, ledger)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "ledger → workbook") {
		t.Errorf("output = %q, want the direction reported", stdout)
	}
	// The new row reached the sheet and reads back.
	led := readLedgerFile(t, ledger)
	if len(led) != 4 {
		t.Fatalf("ledger has %d rows, want 4", len(led))
	}
	if !workbookHas(t, xlsx, "из терминала") {
		t.Error("the appended row did not reach the workbook")
	}
	// A second run has nothing left to do — the baseline was updated.
	if code, stdout, _ := sync(t, xlsx, ledger); code != 0 || !strings.Contains(stdout, "nothing to do") {
		t.Errorf("second run: exit %d, output %q — the baseline was not recorded", code, stdout)
	}
}

func TestRun_finSync_workbookWins(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	editWorkbook(t, xlsx, "Расходы", "F3", 999.99)

	code, stdout, stderr := sync(t, xlsx, ledger)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "workbook → ledger") {
		t.Errorf("output = %q, want the direction reported", stdout)
	}
	if !ledgerFileHas(t, ledger, `"amount":"999.99"`) {
		t.Error("the edited amount did not reach the ledger")
	}
	// An edited row advances a revision; that is what the next sync reads.
	if !ledgerFileHas(t, ledger, `"rev":2`) {
		t.Error("the edited row did not advance a revision")
	}
}

// Both sides moved. Nothing is written, a report is left behind, and the exit
// code says so — the engine can propose a resolution but must not pick one.
func TestRun_finSync_conflictStopsAndReports(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger)
	editWorkbook(t, xlsx, "Расходы", "F3", 999.99)

	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	code, _, stderr := sync(t, xlsx, ledger)
	if code == 0 {
		t.Fatal("a conflict must not exit 0")
	}
	if !strings.Contains(stderr, "conflict") {
		t.Errorf("stderr = %q, want it to name the conflict", stderr)
	}

	after, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a conflict must leave the ledger untouched")
	}

	reports, err := filepath.Glob(filepath.Join(filepath.Dir(ledger), ".conflict-*.md"))
	if err != nil || len(reports) != 1 {
		t.Fatalf("got %d conflict reports, want 1 (err %v)", len(reports), err)
	}
	body, err := os.ReadFile(reports[0])
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	// The report has to name the rows, or it is just a longer way of saying no.
	for _, want := range []string{"ledger", "workbook", "modified", "added"} {
		if !strings.Contains(strings.ToLower(string(body)), want) {
			t.Errorf("report does not mention %q:\n%s", want, body)
		}
	}
}

func TestRun_finSync_resolveWorkbook(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger)
	editWorkbook(t, xlsx, "Расходы", "F3", 999.99)

	code, stdout, stderr := sync(t, xlsx, ledger, "--resolve", "xlsx")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "workbook → ledger") {
		t.Errorf("output = %q, want the forced direction reported", stdout)
	}
	if !ledgerFileHas(t, ledger, `"amount":"999.99"`) {
		t.Error("the workbook's edit did not win")
	}
	// The terminal entry the workbook never saw is gone, which is what choosing
	// the workbook means.
	if ledgerFileHas(t, ledger, "из терминала") {
		t.Error("resolving to the workbook must drop what only the ledger had")
	}
}

func TestRun_finSync_resolveLedger(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger)
	editWorkbook(t, xlsx, "Расходы", "F3", 999.99)

	code, stdout, stderr := sync(t, xlsx, ledger, "--resolve", "jsonl")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "ledger → workbook") {
		t.Errorf("output = %q, want the forced direction reported", stdout)
	}
	if !workbookHas(t, xlsx, "из терминала") {
		t.Error("the ledger's entry did not reach the workbook")
	}
}

func TestRun_finSync_rejectsAnUnknownResolution(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	if code, _, stderr := sync(t, xlsx, ledger, "--resolve", "whichever"); code != 2 {
		t.Errorf("exit = %d, want 2; stderr = %s", code, stderr)
	}
}

// A dry run answers "what would this do" without doing it, which is the
// question worth asking before the first sync of a four-year ledger.
func TestRun_finSync_dryRunChangesNothing(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger)

	before, err := os.ReadFile(xlsx)
	if err != nil {
		t.Fatalf("read workbook: %v", err)
	}
	code, stdout, stderr := sync(t, xlsx, ledger, "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "ledger → workbook") {
		t.Errorf("output = %q, want the direction reported", stdout)
	}
	after, err := os.ReadFile(xlsx)
	if err != nil {
		t.Fatalf("read workbook: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a dry run must not touch the workbook")
	}
}

func readLedgerFile(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func ledgerFileHas(t *testing.T, path, want string) bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return strings.Contains(string(raw), want)
}

func workbookHas(t *testing.T, path, want string) bool {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	defer func() { _ = f.Close() }()
	for _, sheet := range []string{"Расходы", "Доходы"} {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for _, row := range rows {
			for _, v := range row {
				if strings.Contains(v, want) {
					return true
				}
			}
		}
	}
	return false
}
