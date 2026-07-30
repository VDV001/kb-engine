package filebackup_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filebackup"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(path), err)
	}
}

func backupsOf(t *testing.T, path string) []string {
	t.Helper()
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	found, err := filepath.Glob(filepath.Join(filepath.Dir(path), filebackup.DirName, stem+".*"+ext))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return found
}

func at(sec int) func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 30, 12, 0, sec, 0, time.UTC) }
}

// Two writes inside the same second are ordinary — `fin add` twice, a scripted
// loop, one skill logging several transactions — and the timestamp only carried
// seconds while copyFile opens with O_TRUNC. The second snapshot overwrote the
// first, so the state before the first write became unrecoverable while the
// directory still claimed to hold a history.
func TestSnapshot_keepsBothCopiesTakenInTheSameSecond(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transactions.jsonl")

	write(t, path, "state ONE\n")
	if err := filebackup.Snapshot(path, at(0), 10); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	write(t, path, "state TWO\n")
	if err := filebackup.Snapshot(path, at(0), 10); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	found := backupsOf(t, path)
	if len(found) != 2 {
		t.Fatalf("two writes in the same second produced %d backup(s): %v", len(found), found)
	}

	// Both states are present, and the older one sorts first — the rotation relies
	// on lexical order being chronological order.
	var contents []string
	for _, f := range found {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		contents = append(contents, string(b))
	}
	if contents[0] != "state ONE\n" || contents[1] != "state TWO\n" {
		t.Errorf("backups are %q in name order, want the older state first", contents)
	}
}

// Rotation is scoped to the file it protects, not to every file sharing an
// extension. The doc promised that pruning one kind must not delete another's
// history, and "kind" has to mean the file — a ledger and an archive are both
// .jsonl.
func TestSnapshot_rotationLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "transactions.jsonl")
	archive := filepath.Join(dir, "archive.jsonl")

	write(t, archive, "archive\n")
	for i := range 3 {
		if err := filebackup.Snapshot(archive, at(i), 10); err != nil {
			t.Fatalf("archive snapshot %d: %v", i, err)
		}
	}

	write(t, ledger, "ledger\n")
	for i := range 14 {
		if err := filebackup.Snapshot(ledger, at(i), 10); err != nil {
			t.Fatalf("ledger snapshot %d: %v", i, err)
		}
	}

	if got := len(backupsOf(t, ledger)); got != 10 {
		t.Errorf("ledger keeps %d backups, want 10", got)
	}
	if got := len(backupsOf(t, archive)); got != 3 {
		t.Errorf("rotating the ledger left %d of the archive's 3 backups", got)
	}
}

// A workbook and a ledger live in the same folder, and one must not rotate the
// other away.
func TestSnapshot_rotationIsPerExtensionToo(t *testing.T) {
	dir := t.TempDir()
	book := filepath.Join(dir, "Учёт_финансов.xlsx")
	ledger := filepath.Join(dir, "Учёт_финансов.jsonl")

	write(t, book, "book\n")
	for i := range 4 {
		if err := filebackup.Snapshot(book, at(i), 10); err != nil {
			t.Fatalf("book snapshot %d: %v", i, err)
		}
	}
	write(t, ledger, "ledger\n")
	for i := range 12 {
		if err := filebackup.Snapshot(ledger, at(i), 10); err != nil {
			t.Fatalf("ledger snapshot %d: %v", i, err)
		}
	}

	if got := len(backupsOf(t, book)); got != 4 {
		t.Errorf("the workbook kept %d of its 4 backups", got)
	}
	if got := len(backupsOf(t, ledger)); got != 10 {
		t.Errorf("ledger keeps %d backups, want 10", got)
	}
}

// Nothing written yet means nothing to protect, and refusing a first write would
// be an odd way to keep a new file safe.
func TestSnapshot_missingSourceIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transactions.jsonl")

	if err := filebackup.Snapshot(path, at(0), 10); err != nil {
		t.Fatalf("Snapshot on a missing file = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filebackup.DirName)); !os.IsNotExist(err) {
		t.Error("a snapshot of nothing created the backup directory")
	}
}

// The copy carries the contents exactly, and does not inherit a permissive mode
// from the process umask: these are personal records.
func TestSnapshot_copyIsExactAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transactions.jsonl")
	const content = "{\"id\":\"01A\",\"amount\":\"202.45\"}\n"
	write(t, path, content)

	if err := filebackup.Snapshot(path, at(0), 10); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	found := backupsOf(t, path)
	if len(found) != 1 {
		t.Fatalf("want 1 backup, got %v", found)
	}
	got, err := os.ReadFile(found[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != content {
		t.Errorf("backup holds %q, want %q", got, content)
	}
	info, err := os.Stat(found[0])
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("backup mode = %o, want 600", perm)
	}
}
