package main

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// handWrittenRow appends a row the way the owner does: straight into the cells,
// with no id. The sync then pulls it into the ledger, and because the cell is
// empty the record lands there under a positional id.
//
// This is how the state arises in practice — not from a book that was never
// paired, but from one that was, and then grew a row beside the tool.
func handWrittenRow(t *testing.T, path string, row int, amount float64) {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	set := func(cell string, v any) {
		t.Helper()
		if err := f.SetCellValue("Расходы", cell, v); err != nil {
			t.Fatalf("set %s: %v", cell, err)
		}
	}
	n := strconv.Itoa(row)
	set("A"+n, time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	set("B"+n, "Еда")
	set("F"+n, amount)
	if err := f.Save(); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	_ = f.Close()
}

// ledgerIDs returns the ids the ledger holds, in file order.
func ledgerIDs(t *testing.T, ledger string) []string {
	t.Helper()
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec struct{ ID string }
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("parse ledger line: %v", err)
		}
		out = append(out, rec.ID)
	}
	return out
}

// positionallyIdentified sets up the live shape: a paired book that then grew a
// hand-written row, so one record carries a positional id and its cell is empty.
func positionallyIdentified(t *testing.T) (xlsx, ledger string) {
	t.Helper()
	xlsx, ledger = pairedLedger(t)
	handWrittenRow(t, xlsx, 5, 480)

	if code, _, errb := sync(t, xlsx, ledger); code != 0 {
		t.Fatalf("pulling the hand-written row: exit = %d, stderr = %s", code, errb)
	}
	if got := ledgerIDs(t, ledger); !hasPositional(got) {
		t.Fatalf("fixture is not in the shape under test: ledger ids = %v", got)
	}
	return xlsx, ledger
}

func hasPositional(ids []string) bool {
	return slices.ContainsFunc(ids, func(id string) bool { return strings.Contains(id, "-r") })
}

// --backfill-ids stores the id of every row the workbook identifies only by
// position, so the identity stops depending on where the row sits.
//
// Without it the fix for the append defect retires positional ids one row at a
// time, as each row happens to be edited, and a book can carry them for years.
func TestFinSync_backfillIDsStoresThemInTheCells(t *testing.T) {
	xlsx, ledger := positionallyIdentified(t)
	before := ledgerIDs(t, ledger)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "sync", "--backfill-ids", "--from", xlsx, "--ledger", ledger}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "1") {
		t.Errorf("stdout = %q, want it to say how many ids were stored", out.String())
	}
	if v := storedID(t, xlsx, "Расходы", 5); v == "" {
		t.Errorf("id cell for the hand-written row is still empty")
	}

	// The ledger is untouched. Its ids are what the cells now carry, so rewriting
	// them would be a second change to reconcile, not a repair.
	if after := ledgerIDs(t, ledger); !slices.Equal(before, after) {
		t.Errorf("ledger ids changed:\n was %v\n now %v", before, after)
	}
}

// After the backfill the two sides still agree. A repair that leaves a sync
// pending has moved the problem rather than fixed it.
func TestFinSync_backfillIDsLeavesTheSidesInStep(t *testing.T) {
	xlsx, ledger := positionallyIdentified(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--backfill-ids", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}

	code, stdout, stderr := sync(t, xlsx, ledger, "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "none") {
		t.Errorf("after a backfill the sides must agree, got %q", stdout)
	}
}

// A book where every row already carries an id is a normal outcome, and saying
// nothing about it would leave the caller wondering whether the command ran.
func TestFinSync_backfillIDsOnAFullyPairedBookSaysSo(t *testing.T) {
	xlsx, ledger := pairedLedger(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--backfill-ids", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "nothing") {
		t.Errorf("stdout = %q, want it to say there was nothing to store", out.String())
	}
}
