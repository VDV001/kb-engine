package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// legacyPairedWorkbook is a book as an earlier version of this engine left it:
// ids in the eighth column, which is where the account goes when Источник is
// recording how the row was captured.
func legacyPairedWorkbook(t *testing.T) string {
	t.Helper()
	path := workbook(t)
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	for cell, v := range map[string]string{"H2": "id", "H3": "01A", "H4": "01B"} {
		if err := f.SetCellStr("Расходы", cell, v); err != nil {
			t.Fatalf("set %s: %v", cell, err)
		}
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	_ = f.Close()
	return path
}

func cellOf(t *testing.T, path, sheet, cell string) string {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	v, err := f.GetCellValue(sheet, cell)
	if err != nil {
		t.Fatalf("read %s!%s: %v", sheet, cell, err)
	}
	return v
}

// The command the refusal points at has to exist and has to end the state it
// names. An error naming a command that does nothing is worse than no error.
func TestFinSync_migrateIDsMovesThemOffTheAccountColumn(t *testing.T) {
	path := legacyPairedWorkbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--migrate-ids", "--from", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	for cell, want := range map[string]string{"I2": "id", "I3": "01A", "H3": "", "H4": ""} {
		if got := cellOf(t, path, "Расходы", cell); got != want {
			t.Errorf("Расходы!%s = %q, want %q", cell, got, want)
		}
	}
	if !strings.Contains(out.String(), "I") {
		t.Errorf("stdout %q does not say where the ids went", out.String())
	}
}

// Nothing to migrate is a normal outcome, not a failure: the owner runs this
// after reading an error, and a book someone else already fixed must not look
// broken.
func TestFinSync_migrateIDsOnAWellPlacedBookSaysSo(t *testing.T) {
	path := workbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--migrate-ids", "--from", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "нечего") && !strings.Contains(out.String(), "nothing") {
		t.Errorf("stdout %q does not report that there was nothing to do", out.String())
	}
}

// --migrate-ids repairs the workbook alone, so it must not demand the ledger
// flag every other sync mode needs.
func TestFinSync_migrateIDsDoesNotRequireTheLedger(t *testing.T) {
	path := legacyPairedWorkbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--migrate-ids", "--from", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
}
