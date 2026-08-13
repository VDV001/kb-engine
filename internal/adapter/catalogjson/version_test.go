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

// Both fields at once is rejected by the domain. Запись пропускается и
// называется — молча потерять одно из полей хуже, чем не прочитать запись
// целиком, и хуже же было ронять из-за неё весь каталог.
func TestDecode_versionAndRevisionTogetherIsNamed(t *testing.T) {
	c, err := catalogjson.Decode(strings.NewReader(versionedJSON(`,"version":"1.0.0","revision":2`)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	bad := c.Unreadable()
	if len(bad) != 1 {
		t.Fatalf("непрочитанных названо %d, ожидалась одна", len(bad))
	}
	if bad[0].ID != 1 || !strings.Contains(bad[0].Reason, "id=1") {
		t.Fatalf("непрочитанная запись не названа: %+v", bad[0])
	}
}
