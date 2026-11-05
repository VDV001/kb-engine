package userauth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

func newStore(t *testing.T) blob.Store {
	t.Helper()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func id(t *testing.T, s string) domain.UserID {
	t.Helper()
	u, err := domain.NewUserID(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAddThenLookup(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	tok, err := userauth.Add(ctx, s, id(t, "owner"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(tok) < 32 {
		t.Fatalf("token %d chars, want ≥32", len(tok))
	}
	r := userauth.NewRegistry(ctx, s)
	got, ok := r.Lookup(tok)
	if !ok || got.String() != "owner" {
		t.Fatalf("Lookup = %v, %v; want owner", got, ok)
	}
	for _, bad := range []string{"", tok + "x", tok[:len(tok)-1], strings.ToUpper(tok)} {
		if _, ok := r.Lookup(bad); ok {
			t.Errorf("Lookup(%q) accepted", bad)
		}
	}
}

// В хранилище лежит только хэш: украденный файл реестра не дает войти.
func TestRegistryStoresNoPlainToken(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	tok, err := userauth.Add(ctx, s, id(t, "owner"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.Get(ctx, userauth.RegistryKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(o.Data), tok) {
		t.Fatal("the registry holds the token itself")
	}
}

// Новому человеку сразу заводится пустая база: сервер не падает на первом
// входе из-за отсутствующего каталога.
func TestAddCreatesAnEmptyCatalog(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := userauth.Add(ctx, s, id(t, "anna")); err != nil {
		t.Fatal(err)
	}
	o, err := s.Get(ctx, userauth.CatalogKey(id(t, "anna")))
	if err != nil {
		t.Fatalf("catalog of the new user: %v", err)
	}
	if !strings.Contains(string(o.Data), `"entries"`) {
		t.Fatalf("catalog = %s", o.Data)
	}
}

func TestAddTwiceIsRefused(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := userauth.Add(ctx, s, id(t, "owner")); err != nil {
		t.Fatal(err)
	}
	if _, err := userauth.Add(ctx, s, id(t, "owner")); !errors.Is(err, userauth.ErrUserExists) {
		t.Fatalf("second Add: err = %v, want ErrUserExists", err)
	}
}

// Два одновременных add не теряют друг друга: запись реестра условная.
func TestConcurrentAddsKeepBoth(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	names := []string{"a1", "a2", "a3", "a4"}
	toks := make([]string, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Go(func() {
			var err error
			toks[i], err = userauth.Add(ctx, s, id(t, n))
			if err != nil {
				t.Errorf("Add(%s): %v", n, err)
			}
		})
	}
	wg.Wait()
	r := userauth.NewRegistry(ctx, s)
	for i, n := range names {
		if got, ok := r.Lookup(toks[i]); !ok || got.String() != n {
			t.Errorf("token of %s: Lookup = %v, %v", n, got, ok)
		}
	}
}

// Отозванный токен перестает пускать не позже интервала перечитывания.
func TestRevokeTakesEffectAfterRefresh(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	tok, err := userauth.Add(ctx, s, id(t, "anna"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	r := userauth.NewRegistry(ctx, s, userauth.WithClock(func() time.Time { return now }))
	if _, ok := r.Lookup(tok); !ok {
		t.Fatal("fresh token refused")
	}
	if err := userauth.Revoke(ctx, s, id(t, "anna")); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	now = now.Add(userauth.RefreshEvery + time.Second)
	if _, ok := r.Lookup(tok); ok {
		t.Fatal("revoked token still accepted after the refresh interval")
	}
}

func TestRevokeUnknownUser(t *testing.T) {
	s := newStore(t)
	if err := userauth.Revoke(context.Background(), s, id(t, "ghost")); !errors.Is(err, userauth.ErrNoSuchUser) {
		t.Fatalf("err = %v, want ErrNoSuchUser", err)
	}
}

// Пустой или отсутствующий реестр не пускает никого.
func TestEmptyRegistryAdmitsNobody(t *testing.T) {
	r := userauth.NewRegistry(context.Background(), newStore(t))
	if _, ok := r.Lookup(strings.Repeat("x", 43)); ok {
		t.Fatal("a missing registry admitted a token")
	}
}

func TestCatalogKeyIsUnderTheUser(t *testing.T) {
	if got := userauth.CatalogKey(id(t, "anna")); got != "users/anna/kb/_data/catalog.json" {
		t.Fatalf("CatalogKey = %q", got)
	}
}

// Битый реестр (ручная правка, обрезанная запись) не пускает никого: закрыто,
// а не «как было до правки».
func TestBrokenRegistryAdmitsNobody(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	tok, err := userauth.Add(ctx, s, id(t, "owner"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	r := userauth.NewRegistry(ctx, s, userauth.WithClock(func() time.Time { return now }))
	if _, ok := r.Lookup(tok); !ok {
		t.Fatal("fresh token refused")
	}
	cur, err := s.Get(ctx, userauth.RegistryKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, userauth.RegistryKey, cur.Data[:len(cur.Data)/2], cur.ETag); err != nil {
		t.Fatal(err)
	}
	now = now.Add(userauth.RefreshEvery + time.Second)
	if _, ok := r.Lookup(tok); ok {
		t.Fatal("a broken registry still admitted the old token")
	}
}
