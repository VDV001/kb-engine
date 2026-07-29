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

func TestSaveLoadState_roundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	want := finance.SyncState{
		SyncedAt: time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC),
		Rows:     map[string]string{"01A": "deadbeef", "01B": "cafebabe"},
	}
	if err := financejsonl.SaveState(path, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := financejsonl.LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !got.SyncedAt.Equal(want.SyncedAt) {
		t.Errorf("SyncedAt = %s, want %s", got.SyncedAt, want.SyncedAt)
	}
	if len(got.Rows) != len(want.Rows) {
		t.Fatalf("Rows = %v, want %v", got.Rows, want.Rows)
	}
	for id, fp := range want.Rows {
		if got.Rows[id] != fp {
			t.Errorf("Rows[%s] = %q, want %q", id, got.Rows[id], fp)
		}
	}
}

// A missing state file is not an error, and this is the one place the finances
// package treats absence as legitimate. The ledger going missing means data
// vanished; a baseline going missing means the two sides have never been
// synced, and Diff already refuses to move anything until they agree.
func TestLoadState_missingFileIsAnEmptyBaseline(t *testing.T) {
	got, err := financejsonl.LoadState(filepath.Join(t.TempDir(), ".sync-state.json"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(got.Rows) != 0 {
		t.Errorf("Rows = %v, want empty", got.Rows)
	}
}

// A state file that cannot be read is different from one that is not there: it
// means something wrote nonsense where the baseline should be, and continuing
// would silently treat that as "never synced".
func TestLoadState_malformedIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := financejsonl.LoadState(path); err == nil {
		t.Error("a malformed state file must be an error, not an empty baseline")
	}
}

// Same rule as the ledger: replace through a temp file and a rename, and leave
// no debris.
func TestSaveState_replacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".sync-state.json")
	first := finance.SyncState{SyncedAt: writtenAt, Rows: map[string]string{"01A": "one"}}
	second := finance.SyncState{SyncedAt: writtenAt, Rows: map[string]string{"01B": "two"}}
	if err := financejsonl.SaveState(path, first); err != nil {
		t.Fatalf("first SaveState: %v", err)
	}
	if err := financejsonl.SaveState(path, second); err != nil {
		t.Fatalf("second SaveState: %v", err)
	}

	got, err := financejsonl.LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, stale := got.Rows["01A"]; stale {
		t.Error("the second save did not fully replace the first")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp files left behind: %v", names)
	}
}

// The file is small and gets read by a person when a sync goes sideways.
func TestSaveState_isReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sync-state.json")
	if err := financejsonl.SaveState(path, finance.SyncState{
		SyncedAt: time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC),
		Rows:     map[string]string{"01A": "deadbeef"},
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, want := range []string{"\n", "  ", `"synced_at": "2026-07-29T04:00:00Z"`, `"01A": "deadbeef"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("state file is missing %q:\n%s", want, raw)
		}
	}
}
