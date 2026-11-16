package userauth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
)

// putCounter counts writes of SeenKey.
type putCounter struct {
	blob.Store
	mu   sync.Mutex
	seen int
}

func (p *putCounter) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	if key == userauth.SeenKey {
		p.mu.Lock()
		p.seen++
		p.mu.Unlock()
	}
	return p.Store.Put(ctx, key, data, ifMatch)
}

// Окно видит, что телефон вошел и как он себя зовет; запись — не на каждый
// опрос, а раз в VisitEvery и при смене имени (#622).
func TestVisits(t *testing.T) {
	ctx := context.Background()
	s := &putCounter{Store: newStore(t)}
	owner := id(t, "owner")
	if _, err := userauth.Add(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	dev, _, _ := userauth.AddDevice(ctx, s, owner)
	quiet, _, _ := userauth.AddDevice(ctx, s, owner)
	phones := userauth.Phones{Store: s}
	v := &userauth.Visits{Store: s}
	t0 := time.Date(2026, 11, 16, 12, 6, 0, 0, time.UTC)

	if err := v.Visit(ctx, owner, dev, "iPhone", t0); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Duration{10 * time.Second, time.Minute} {
		if err := v.Visit(ctx, owner, dev, "iPhone", t0.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	if s.seen != 1 {
		t.Fatalf("SeenKey written %d times within VisitEvery, want 1", s.seen)
	}
	_ = v.Visit(ctx, owner, dev, "iPad", t0.Add(2*time.Minute)) // имя сменилось — сразу
	_ = v.Visit(ctx, owner, dev, "iPad", t0.Add(userauth.VisitEvery+3*time.Minute))
	if s.seen != 3 {
		t.Fatalf("SeenKey written %d times, want 3 (first, new name, after VisitEvery)", s.seen)
	}
	list, err := phones.List(ctx, owner)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	for _, p := range list {
		switch p.Device {
		case dev:
			if p.Name != "iPad" || !p.Seen.Equal(t0.Add(userauth.VisitEvery+3*time.Minute)) {
				t.Errorf("visited phone %+v", p)
			}
		case quiet:
			if p.Name != "" || !p.Seen.IsZero() {
				t.Errorf("phone that never came in %+v", p)
			}
		}
	}
	if err := userauth.RevokeDevice(ctx, s, owner, dev); err != nil {
		t.Fatal(err)
	}
	if _, _, err := userauth.AddDevice(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	if list, _ := phones.List(ctx, owner); len(list) != 2 || list[0].Device == dev || list[1].Device == dev {
		t.Fatalf("after revoke: %+v", list)
	}
}
