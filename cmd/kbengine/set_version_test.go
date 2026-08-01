package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func versionSetCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	doc := `{"entries":[
{"id":790,"title":"Brain Fry v5","url":"","category":"creations","status":"published","lifecycle":"active","file":"creations/habr/v5-final.md","version":5},
{"id":800,"title":"Someone else's","url":"","category":"ai-agents-tools","status":"keep","lifecycle":"active"}
]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func memberOf(t *testing.T, path string, id int, key string) (json.RawMessage, bool) {
	t.Helper()
	m := entryMembers(t, path, id)
	v, ok := m[key]
	return v, ok
}

// The migration refuses to guess a semver for an own artefact, and points at
// set. That command therefore has to be able to write one.
func TestSet_versionWritesSemver(t *testing.T) {
	path := versionSetCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"set", "--catalog", path, "--ids", "790", "--version", "5.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	got, ok := memberOf(t, path, 790, "version")
	if !ok {
		t.Fatal("entry 790 has no version member")
	}
	if string(got) != `"5.0.0"` {
		t.Fatalf("version = %s, want \"5.0.0\"", got)
	}
}

func TestSet_revisionWritesNumber(t *testing.T) {
	path := versionSetCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"set", "--catalog", path, "--ids", "800", "--revision", "2"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	got, ok := memberOf(t, path, 800, "revision")
	if !ok {
		t.Fatal("entry 800 has no revision member")
	}
	if string(got) != "2" {
		t.Fatalf("revision = %s, want 2", got)
	}
}

// Writing a version must clear a revision that was there, and the other way
// round. Leaving both is the state the domain refuses to load — set would be
// writing a catalog the engine can no longer read.
func TestSet_versionClearsRevisionAndBack(t *testing.T) {
	path := versionSetCatalog(t)

	var out, errb bytes.Buffer
	if code := run([]string{"set", "--catalog", path, "--ids", "800", "--revision", "2"}, &out, &errb); code != 0 {
		t.Fatalf("revision exit = %d, stderr = %s", code, errb.String())
	}
	if code := run([]string{"set", "--catalog", path, "--ids", "800", "--version", "1.0.0"}, &out, &errb); code != 0 {
		t.Fatalf("version exit = %d, stderr = %s", code, errb.String())
	}
	if _, ok := memberOf(t, path, 800, "revision"); ok {
		t.Error("revision survived next to a version")
	}

	if code := run([]string{"set", "--catalog", path, "--ids", "800", "--revision", "3"}, &out, &errb); code != 0 {
		t.Fatalf("revision exit = %d, stderr = %s", code, errb.String())
	}
	if _, ok := memberOf(t, path, 800, "version"); ok {
		t.Error("version survived next to a revision")
	}
}

func TestSet_rejectsInvalidVersionAndRevision(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "two-component version", args: []string{"--version", "1.0"}},
		{name: "not a version at all", args: []string{"--version", "latest"}},
		{name: "zero revision", args: []string{"--revision", "0"}},
		{name: "both at once", args: []string{"--version", "1.0.0", "--revision", "2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := versionSetCatalog(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			var out, errb bytes.Buffer
			args := append([]string{"set", "--catalog", path, "--ids", "800"}, c.args...)
			if code := run(args, &out, &errb); code == 0 {
				t.Fatalf("set accepted %v, want refusal", c.args)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("a refused set still wrote to the catalog")
			}
			if strings.TrimSpace(errb.String()) == "" {
				t.Error("refusal said nothing on stderr")
			}
		})
	}
}
