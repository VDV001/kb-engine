package catalogjson_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// versionedJSON builds a one-entry catalog carrying the given raw version and
// revision members verbatim, so the loader is tested against the shapes the
// live catalog actually stores.
func versionedJSON(members string) string {
	return `{"entries":[{"id":1,"title":"T","url":"","category":"ai-agents-tools",` +
		`"status":"keep","lifecycle":"active"` + members + `}]}`
}

// The loader is the anti-corruption layer: the on-disk shape decides which
// field the value lands in. A bare number was how the legacy catalog spelled a
// revision — 182 entries stored version:1 — so it must keep loading after the
// migration, not fail the whole catalog.
func TestDecode_versionAndRevision(t *testing.T) {
	tests := []struct {
		name         string
		members      string
		wantVersion  string // "" = none
		wantRevision int    // 0 = none
	}{
		{name: "absent", members: ""},
		{name: "semver string is an owner artefact version", members: `,"version":"1.5.1"`, wantVersion: "1.5.1"},
		{name: "explicit revision", members: `,"revision":2`, wantRevision: 2},
		{name: "legacy bare number is a revision", members: `,"version":1`, wantRevision: 1},
		{name: "legacy numeric string is a revision", members: `,"version":"5"`, wantRevision: 5},
		{name: "legacy two-component default is a revision", members: `,"version":"1.0"`, wantRevision: 1},
		{name: "null version", members: `,"version":null`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalogjson.Decode(strings.NewReader(versionedJSON(tt.members)))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			e := c.Entries()[0]

			got := ""
			if v := e.Version(); v != nil {
				got = v.String()
			}
			if got != tt.wantVersion {
				t.Fatalf("Version() = %q, want %q", got, tt.wantVersion)
			}

			gotRev := 0
			if r := e.Revision(); r != nil {
				gotRev = *r
			}
			if gotRev != tt.wantRevision {
				t.Fatalf("Revision() = %d, want %d", gotRev, tt.wantRevision)
			}
		})
	}
}

// Both fields at once is rejected by the domain. The loader must surface that
// as a load error naming the entry, not silently drop one of them — a catalog
// that quietly loses a field is worse than one that refuses to load.
func TestDecode_versionAndRevisionTogetherIsAnError(t *testing.T) {
	_, err := catalogjson.Decode(strings.NewReader(versionedJSON(`,"version":"1.0.0","revision":2`)))
	if err == nil {
		t.Fatal("Decode accepted both version and revision, want error")
	}
	if !strings.Contains(err.Error(), "id=1") {
		t.Fatalf("error %v does not name the offending entry", err)
	}
}
