package artefactfs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/artefactfs"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestVersionOf(t *testing.T) {
	root := t.TempDir()
	write(t, root, "standards/x/v1.md", "---\nname: x\nversion: 1.5.1\n---\n\n# X\n")
	write(t, root, "standards/quoted/v1.md", "---\nversion: \"2.0.0\"\n---\n")
	write(t, root, "creations/habr/v5-final.md", "# Заголовок\n\nТекст без front matter.\n")
	write(t, root, "standards/odd/v1.md", "---\nversion: latest\n---\n")

	cases := []struct {
		name    string
		file    string
		want    string
		wantHas bool
	}{
		{name: "front matter version", file: "standards/x/v1.md", want: "1.5.1", wantHas: true},
		{name: "quoted value", file: "standards/quoted/v1.md", want: "2.0.0", wantHas: true},
		{name: "no front matter", file: "creations/habr/v5-final.md"},
		{name: "non-semver value says nothing", file: "standards/odd/v1.md"},
		// The catalog outlived several directory layouts and still points at
		// paths that are gone. That is a different defect from a stale version.
		{name: "missing file is not an error", file: "standards/gone/v1.md"},
		{name: "empty path", file: ""},
		// A path escaping the root would read files the catalog never named.
		{name: "traversal is contained", file: "../../etc/passwd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := artefactfs.Reader{Root: root}
			v, ok, err := r.VersionOf(c.file)
			if err != nil {
				t.Fatalf("VersionOf(%q): %v", c.file, err)
			}
			if ok != c.wantHas {
				t.Fatalf("VersionOf(%q) ok = %v, want %v", c.file, ok, c.wantHas)
			}
			if ok && v.String() != c.want {
				t.Fatalf("VersionOf(%q) = %s, want %s", c.file, v, c.want)
			}
		})
	}
}
