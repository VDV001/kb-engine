package appdir

import (
	"path/filepath"
	"testing"
)

func TestDirFor(t *testing.T) {
	if got, want := dirFor("darwin", "/Users/a"), filepath.Join("/Users/a", "Library", "Application Support", "kb-desktop"); got != want {
		t.Fatalf("darwin: %q", got)
	}
	if got, want := dirFor("windows", "/Users/a"), filepath.Join("/Users/a", "AppData", "Roaming", "kb-desktop"); got != want {
		t.Fatalf("windows: %q", got)
	}
}
