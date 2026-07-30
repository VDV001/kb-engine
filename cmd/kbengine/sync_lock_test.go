package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lockWorkbook(t *testing.T, xlsx string) {
	t.Helper()
	lock := filepath.Join(filepath.Dir(xlsx), ".~lock."+filepath.Base(xlsx)+"#")
	if err := os.WriteFile(lock, []byte(",user,host,29.07.2026 20:00,"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

// --init already asks about the lock before promising a run. The ordinary sync
// does not, so a dry run announced a push into a book LibreOffice was holding
// and the real command then refused it. The asymmetry is the defect: a dry run
// is worth exactly as much as the questions it asks.
func TestRun_finSync_dryRunRefusesToPromiseAPushIntoALockedWorkbook(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	addToLedger(t, ledger) // the ledger now leads, so the sync would write the book
	lockWorkbook(t, xlsx)

	var out, errb bytes.Buffer
	code := run([]string{"fin", "sync", "--dry-run", "--from", xlsx, "--ledger", ledger}, &out, &errb)
	if code == 0 {
		t.Errorf("exit = 0 on a locked workbook; stdout = %q", out.String())
	}
	if !strings.Contains(strings.ToLower(errb.String()), "lock") {
		t.Errorf("stderr = %q, want it to name the lock", errb.String())
	}
}

// A sync that only reads the workbook is not blocked by the lock. Refusing
// every command while the book is open would make the lock a bigger problem
// than the one it prevents.
func TestRun_finSync_dryRunPullingFromALockedWorkbookIsFine(t *testing.T) {
	xlsx, ledger := pairedLedger(t)
	editWorkbook(t, xlsx, "Расходы", "F3", 999.99) // the workbook leads
	lockWorkbook(t, xlsx)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "sync", "--dry-run", "--from", xlsx, "--ledger", ledger}, &out, &errb); code != 0 {
		t.Errorf("exit = %d for a read-only direction; stderr = %s", code, errb.String())
	}
}
