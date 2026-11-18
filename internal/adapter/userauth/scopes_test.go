package userauth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// flaky fails Get of the scopes file while down is set.
type flaky struct {
	blob.Store
	down bool
}

func (f *flaky) Get(ctx context.Context, key string) (blob.Object, error) {
	if f.down && key == userauth.ScopesKey {
		return blob.Object{}, errors.New("storage is down")
	}
	return f.Store.Get(ctx, key)
}

func (f *flaky) GetIfChanged(ctx context.Context, key, etag string) (blob.Object, error) {
	if f.down && key == userauth.ScopesKey {
		return blob.Object{}, errors.New("storage is down")
	}
	return f.Store.GetIfChanged(ctx, key, etag)
}

func clock(start time.Time) (func() time.Time, func(time.Duration)) {
	now := start
	return func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }
}

var t18 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// Без файла областей все пишут, как до #701; с записью «read» человек читает,
// но не пишет, а сосед не затронут.
func TestScopesReadOnlyPersonAndNeighbour(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cloud, anna := id(t, "cloud"), id(t, "anna")
	now, advance := clock(t18)
	sc := userauth.NewScopes(ctx, s, now)
	if !writable(sc, cloud) || !writable(sc, anna) {
		t.Fatal("with no scopes file everyone must be writable")
	}
	if err := userauth.SetScope(ctx, s, cloud, domain.ScopeRead); err != nil {
		t.Fatal(err)
	}
	advance(userauth.RefreshEvery + time.Second)
	if writable(sc, cloud) {
		t.Error("the read-only person is writable")
	}
	if !writable(sc, anna) {
		t.Error("the neighbour lost the write")
	}
	if err := userauth.SetScope(ctx, s, cloud, domain.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	advance(userauth.RefreshEvery + time.Second)
	if !writable(sc, cloud) {
		t.Error("clearing the scope did not give the write back")
	}
}

// Закрыто, а не открыто: файл, который нельзя прочитать, не делает
// ограниченного человека пишущим.
func TestScopesFailClosed(t *testing.T) {
	ctx := context.Background()
	base := newStore(t)
	cloud, anna := id(t, "cloud"), id(t, "anna")
	if err := userauth.SetScope(ctx, base, cloud, domain.ScopeRead); err != nil {
		t.Fatal(err)
	}
	t.Run("never read yet and the storage is down", func(t *testing.T) {
		now, _ := clock(t18)
		sc := userauth.NewScopes(ctx, &flaky{Store: base, down: true}, now)
		if writable(sc, cloud) || writable(sc, anna) {
			t.Error("writable while the scopes file cannot be read")
		}
	})
	t.Run("cached list lives MaxStale, then nobody writes", func(t *testing.T) {
		st := &flaky{Store: base}
		now, advance := clock(t18)
		sc := userauth.NewScopes(ctx, st, now)
		if writable(sc, cloud) || !writable(sc, anna) {
			t.Fatal("setup: cloud must be read-only, anna writable")
		}
		st.down = true
		advance(userauth.RefreshEvery + time.Second)
		if writable(sc, cloud) || !writable(sc, anna) {
			t.Error("within MaxStale the last good list must hold")
		}
		advance(userauth.MaxStale)
		if writable(sc, cloud) || writable(sc, anna) {
			t.Error("after MaxStale of failure nobody may write")
		}
		st.down = false
		advance(userauth.RefreshEvery + time.Second)
		if writable(sc, cloud) || !writable(sc, anna) {
			t.Error("after the storage is back the list must be read again")
		}
	})
	t.Run("a corrupt file closes writes for everyone", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Put(ctx, userauth.ScopesKey, []byte("{not json"), ""); err != nil {
			t.Fatal(err)
		}
		now, _ := clock(t18)
		sc := userauth.NewScopes(ctx, s, now)
		if writable(sc, cloud) || writable(sc, anna) {
			t.Error("a corrupt scopes file left someone writable")
		}
	})
}

// Повторная запись той же области и снятие несуществующей — не ошибка и не
// мусор в файле.
func TestSetScopeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cloud := id(t, "cloud")
	for range 2 {
		if err := userauth.SetScope(ctx, s, cloud, domain.ScopeRead); err != nil {
			t.Fatal(err)
		}
	}
	if err := userauth.SetScope(ctx, s, id(t, "ghost"), domain.ScopeWrite); err != nil {
		t.Fatalf("clearing a scope that is not there: %v", err)
	}
	o, err := s.Get(ctx, userauth.ScopesKey)
	if err != nil {
		t.Fatal(err)
	}
	if n := countOccurrences(string(o.Data), `"cloud"`); n != 1 {
		t.Errorf("cloud listed %d times: %s", n, o.Data)
	}
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

// Битый файл закрывает запись всем, и причина попадает в журнал: иначе человек,
// у которого вдруг перестала работать запись, не найдет, почему.
func TestScopesCorruptFileSaysWhy(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.Put(ctx, userauth.ScopesKey, []byte(`{"scopes":[{"id":"alpha","scope":"Read"}]}`), ""); err != nil {
		t.Fatal(err)
	}
	now, _ := clock(t18)
	sc := userauth.NewScopes(ctx, s, now)
	var logged []string
	sc.Logf = func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	if writable(sc, id(t, "alpha")) || writable(sc, id(t, "beta")) {
		t.Fatal("setup: a corrupt file must close writes for everyone")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], userauth.ScopesKey) || !strings.Contains(logged[0], "Read") {
		t.Errorf("the reason was not logged: %q", logged)
	}
}

// Опечатка в имени поля при ручной правке не делает человека пишущим молча:
// неизвестное поле - битый файл, а битый файл закрывает запись.
func TestScopesUnknownFieldClosesWrites(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.Put(ctx, userauth.ScopesKey, []byte(`{"scopes":[{"id":"cloud","scopes":"read"}]}`), ""); err != nil {
		t.Fatal(err)
	}
	now, _ := clock(t18)
	sc := userauth.NewScopes(ctx, s, now)
	if writable(sc, id(t, "cloud")) || writable(sc, id(t, "anna")) {
		t.Error("a misspelt field left someone writable")
	}
}

func writable(sc *userauth.Scopes, id domain.UserID) bool {
	s, ok := sc.Scope(id)
	return ok && s == domain.ScopeWrite
}

// Область append хранится и читается; кроме неё у человека все как у read для
// остальных, сосед пишет, а недоверенный файл не дает никому ничего, кроме чтения.
func TestScopesAppendIsStoredAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	base := newStore(t)
	cloud, anna := id(t, "cloud"), id(t, "anna")
	if err := userauth.SetScope(ctx, base, cloud, domain.ScopeAppend); err != nil {
		t.Fatal(err)
	}
	now, _ := clock(t18)
	sc := userauth.NewScopes(ctx, base, now)
	if got, ok := sc.Scope(cloud); !ok || got != domain.ScopeAppend {
		t.Errorf("Scope(cloud) = %v, %v; want append", got, ok)
	}
	if got, ok := sc.Scope(anna); !ok || got != domain.ScopeWrite {
		t.Errorf("Scope(anna) = %v, %v; want write", got, ok)
	}
	down := userauth.NewScopes(ctx, &flaky{Store: base, down: true}, now)
	if _, ok := down.Scope(cloud); ok {
		t.Error("an unreadable scopes file is trusted")
	}
	if err := userauth.SetScope(ctx, base, cloud, domain.ScopeRead); err != nil {
		t.Fatal(err)
	}
	later, advance := clock(t18)
	sc2 := userauth.NewScopes(ctx, base, later)
	if got, _ := sc2.Scope(cloud); got != domain.ScopeRead {
		t.Errorf("append replaced by read: %v", got)
	}
	_ = advance
}
