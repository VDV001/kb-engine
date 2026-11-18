package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewScope(t *testing.T) {
	cases := []struct {
		in    string
		want  domain.Scope
		write bool
		bad   bool
	}{
		{"read", domain.ScopeRead, false, false},
		{"write", domain.ScopeWrite, true, false},
		{"append", domain.ScopeAppend, false, false},
		{"", domain.ScopeWrite, true, false},
		{"Read", domain.Scope{}, false, true},
		{"readonly", domain.Scope{}, false, true},
		{"admin", domain.Scope{}, false, true},
		{" read", domain.Scope{}, false, true},
	}
	for _, c := range cases {
		got, err := domain.NewScope(c.in)
		if c.bad {
			if !errors.Is(err, domain.ErrInvalidScope) {
				t.Errorf("NewScope(%q) = %v, want ErrInvalidScope", c.in, err)
			}
			continue
		}
		if err != nil || got != c.want || got.AllowsWrite() != c.write {
			t.Errorf("NewScope(%q) = %v, %v; want %v, write=%v", c.in, got, err, c.want, c.write)
		}
	}
	if domain.ScopeRead.String() != "read" || domain.ScopeWrite.String() != "write" {
		t.Errorf("String: %q %q", domain.ScopeRead, domain.ScopeWrite)
	}
}

// Что разрешает каждая область: write - всё; read - только чтение; append - чтение
// и добавление записи (POST ровно на /api/entries), больше ничего.
func TestScopeAllows(t *testing.T) {
	type req struct{ method, path string }
	read := []req{{"GET", "/api/entries"}, {"HEAD", "/api/search"}, {"GET", "/api/mail"}}
	add := req{"POST", "/api/entries"}
	changes := []req{
		{"PATCH", "/api/entries/1"}, {"DELETE", "/api/entries/1"}, {"PUT", "/api/portrait"},
		{"POST", "/api/finances/transactions"}, {"POST", "/api/mail/c-1"}, {"POST", "/api/mail/files"},
		{"POST", "/api/entries/"}, {"POST", "/api/entries/1"}, {"POST", "//api/entries"}, {"POST", "/api/entriesx"},
		{"PUT", "/api/entries"}, {"DELETE", "/api/entries"}, {"PATCH", "/api/entries"}, {"POST", "/api/finances/export"},
		{"TRACE", "/api/entries"}, {"post", "/api/entries"}, {"POST", ""},
	}
	for _, r := range read {
		for _, sc := range []domain.Scope{domain.ScopeRead, domain.ScopeAppend, domain.ScopeWrite} {
			if !sc.Allows(r.method, r.path) {
				t.Errorf("%v refuses %s %s", sc, r.method, r.path)
			}
		}
	}
	if domain.ScopeRead.Allows(add.method, add.path) {
		t.Error("read may add")
	}
	if !domain.ScopeAppend.Allows(add.method, add.path) || !domain.ScopeWrite.Allows(add.method, add.path) {
		t.Error("append and write must add")
	}
	for _, r := range changes {
		if domain.ScopeRead.Allows(r.method, r.path) || domain.ScopeAppend.Allows(r.method, r.path) {
			t.Errorf("read or append allows %s %s", r.method, r.path)
		}
		if !domain.ScopeWrite.Allows(r.method, r.path) {
			t.Errorf("write refuses %s %s", r.method, r.path)
		}
	}
}
