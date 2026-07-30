package financejsonl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// savedAt is the clock a write is stamped with — the one that names the backup
// it leaves behind.
var savedAt = func() time.Time { return writtenAt }

// backupsIn lists the ledger backups next to path, newest last.
func backupsIn(t *testing.T, path string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".backup", "*.jsonl"))
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	return found
}

// The workbook has ten rotating copies behind every write. The ledger is the
// side a bad sync overwrites, and until this test it had none: an atomic
// rename protects against a crash halfway through, not against replacing the
// file with content that is wrong. There is no other copy — the folder is not
// under git.
func TestSave_keepsACopyOfWhatItReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transactions.jsonl")
	before := []finance.Record{expense(t, "01A"), expense(t, "01B")}
	if err := financejsonl.Save(path, before, savedAt); err != nil {
		t.Fatalf("first save: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	// The shape of a sync that resolves the wrong way: two rows in, one row out.
	if err := financejsonl.Save(path, []finance.Record{expense(t, "01A")}, savedAt); err != nil {
		t.Fatalf("second save: %v", err)
	}

	found := backupsIn(t, path)
	if len(found) != 1 {
		t.Fatalf("want 1 backup after the replacing write, got %d: %v", len(found), found)
	}
	saved, err := os.ReadFile(found[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(saved) != string(original) {
		t.Errorf("backup does not hold what was replaced:\n got %q\nwant %q", saved, original)
	}
	if n := strings.Count(string(saved), "\n"); n != 2 {
		t.Errorf("backup holds %d lines, want the 2 that were there before", n)
	}
}

// A file that does not exist yet has nothing to protect, and refusing the first
// write of a new ledger would be an odd way to keep it safe.
func TestSave_firstWriteHasNothingToBackUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transactions.jsonl")

	if err := financejsonl.Save(path, []finance.Record{expense(t, "01A")}, savedAt); err != nil {
		t.Fatalf("save: %v", err)
	}

	if found := backupsIn(t, path); len(found) != 0 {
		t.Errorf("want no backups for a first write, got %v", found)
	}
}

// Enough history to undo a bad afternoon, few enough that the directory stays
// readable — the same bound the workbook keeps.
func TestSave_keepsTheLastTenBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transactions.jsonl")

	for i := range 14 {
		// Distinct timestamps, or the names collide and the count means nothing.
		at := writtenAt.Add(time.Duration(i) * time.Minute)
		if err := financejsonl.Save(path, []finance.Record{expense(t, "01A")}, func() time.Time { return at }); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	if found := backupsIn(t, path); len(found) != 10 {
		t.Errorf("want 10 backups kept, got %d", len(found))
	}
}
