package userauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

func pushOwners(t *testing.T, s blob.Store) []string {
	t.Helper()
	o, err := s.Get(context.Background(), userauth.PushKey)
	if errors.Is(err, blob.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Subs []struct {
			ID string `json:"id"`
		} `json:"subs"`
	}
	if err := json.Unmarshal(o.Data, &r); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range r.Subs {
		out = append(out, p.ID)
	}
	return out
}

func subscribe(t *testing.T, s blob.Store, who domain.UserID, device string) {
	t.Helper()
	key := append([]byte{4}, bytes.Repeat([]byte{7}, 64)...)
	sub, err := domain.NewPushSubscription("https://fcm.googleapis.com/fcm/send/"+who.String(), key, bytes.Repeat([]byte{9}, 16))
	if err != nil {
		t.Fatal(err)
	}
	if err := userauth.SetPush(context.Background(), s, who, device, sub); err != nil {
		t.Fatal(err)
	}
}

// Отзыв уносит и адреса push-уведомлений человека: раньше auth/push.json
// оставался с его записями после revoke.
func TestRevokeClearsPushSubscriptions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner, other := id(t, "owner"), id(t, "other")
	devices := map[domain.UserID]string{}
	for _, u := range []domain.UserID{owner, other} {
		if _, err := userauth.Add(ctx, s, u); err != nil {
			t.Fatal(err)
		}
		dev, _, err := userauth.AddDevice(ctx, s, u)
		if err != nil {
			t.Fatal(err)
		}
		devices[u] = dev
		subscribe(t, s, u, dev)
	}
	if err := userauth.Revoke(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	if got := pushOwners(t, s); len(got) != 1 || got[0] != "other" {
		t.Errorf("push subscriptions after revoke of owner: %v, want [other]", got)
	}
}

// personFixture is two people with data in every place Purge reaches.
func personFixture(t *testing.T) (blob.Store, domain.UserID, domain.UserID, map[string]string) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	a, ab := id(t, "a"), id(t, "a-b")
	toks := map[string]string{}
	for _, u := range []domain.UserID{a, ab} {
		tok, err := userauth.Add(ctx, s, u)
		if err != nil {
			t.Fatal(err)
		}
		toks[u.String()] = tok
		dev, _, err := userauth.AddDevice(ctx, s, u)
		if err != nil {
			t.Fatal(err)
		}
		subscribe(t, s, u, dev)
		v := &userauth.Visits{Store: s}
		if err := v.Visit(ctx, u, dev, "Phone", time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"users/" + u.String() + "/kb/notes/n.md", "mailfiles/users/" + u.String() + "/f1"} {
			if _, err := s.Put(ctx, key, []byte(key), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s, a, ab, toks
}

func exists(t *testing.T, s blob.Store, key string) bool {
	t.Helper()
	_, err := s.Get(context.Background(), key)
	if err != nil && !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get %s: %v", key, err)
	}
	return err == nil
}

// Purge снимает человека целиком — вход, телефоны, push, входы телефонов, базу и
// файлы ящика, — а сосед с похожим именем остается как был.
func TestPurgeRemovesEveryTraceOfOnePerson(t *testing.T) {
	ctx := context.Background()
	s, a, ab, toks := personFixture(t)
	rep, err := userauth.Purge(ctx, s, a)
	if err != nil {
		t.Fatal(err)
	}
	// Add кладет пустой каталог, плюс заметка; файл ящика один; вход один.
	if rep != (userauth.PurgeReport{Base: 2, Mail: 1, Seen: 1}) {
		t.Errorf("report = %+v, want {Base:2 Mail:1 Seen:1}", rep)
	}

	reg := userauth.NewRegistry(ctx, s)
	if _, ok := reg.Lookup(toks["a"]); ok {
		t.Error("a's token still logs in")
	}
	if got, ok := reg.Lookup(toks["a-b"]); !ok || got != ab {
		t.Error("the neighbour a-b lost the login")
	}
	if ds, err := userauth.Devices(ctx, s, a); err != nil || len(ds) != 0 {
		t.Errorf("a's phones: %v, %v", ds, err)
	}
	if ds, err := userauth.Devices(ctx, s, ab); err != nil || len(ds) != 1 {
		t.Errorf("a-b's phones: %v, %v", ds, err)
	}
	if got := pushOwners(t, s); len(got) != 1 || got[0] != "a-b" {
		t.Errorf("push owners: %v, want [a-b]", got)
	}
	for _, key := range []string{userauth.CatalogKey(a), "users/a/kb/notes/n.md", "mailfiles/users/a/f1", userauth.SeenKey(a)} {
		if exists(t, s, key) {
			t.Errorf("%s survived Purge", key)
		}
	}
	for _, key := range []string{userauth.CatalogKey(ab), "users/a-b/kb/notes/n.md", "mailfiles/users/a-b/f1", userauth.SeenKey(ab)} {
		if !exists(t, s, key) {
			t.Errorf("%s of the neighbour was removed", key)
		}
	}
}

// Повтор безопасен, и человек, которого уже отозвали, тоже снимается: данные
// после revoke остаются, и purge должен их добрать.
func TestPurgeAfterRevokeAndAgain(t *testing.T) {
	ctx := context.Background()
	s, a, _, _ := personFixture(t)
	if err := userauth.Revoke(ctx, s, a); err != nil {
		t.Fatal(err)
	}
	if rep, err := userauth.Purge(ctx, s, a); err != nil || rep.Base == 0 {
		t.Fatalf("Purge after revoke = %+v, %v; want the data removed", rep, err)
	}
	if rep, err := userauth.Purge(ctx, s, a); err != nil || rep != (userauth.PurgeReport{}) {
		t.Fatalf("second Purge = %+v, %v; want zeros", rep, err)
	}
}

// Хранилище, которое не умеет снимать дерево, отказывает ДО отзыва: полдела не
// остается (человек не отозван, но и не удален).
func TestPurgeWithoutAPurgerChangesNothing(t *testing.T) {
	ctx := context.Background()
	full, a, _, toks := personFixture(t)
	s := struct{ blob.Store }{full} // скрывает Purge у blobfs
	if _, err := userauth.Purge(ctx, s, a); !errors.Is(err, userauth.ErrNoPurge) {
		t.Fatalf("Purge on a store without Purger: %v, want ErrNoPurge", err)
	}
	if _, ok := userauth.NewRegistry(ctx, full).Lookup(toks["a"]); !ok {
		t.Error("the person was revoked by a refused purge")
	}
	if !exists(t, full, "users/a/kb/notes/n.md") {
		t.Error("data was removed by a refused purge")
	}
}

// Отзыв, прошедший только до реестра (старый revoke без чистки push или сбой
// между записями), оставляет телефоны, push и входы человека; Revoke и Purge
// доделывают их и при повторе, а не выходят на ErrNoSuchUser.
func TestRevokeAndPurgeFinishWhatAHalfDoneRevokeLeft(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	anna := id(t, "anna")
	tok, err := userauth.Add(ctx, s, anna)
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := userauth.AddDevice(ctx, s, anna)
	if err != nil {
		t.Fatal(err)
	}
	subscribe(t, s, anna, dev)
	if err := (&userauth.Visits{Store: s}).Visit(ctx, anna, dev, "Phone", time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := userauth.RevokeToken(ctx, s, anna, tok); err != nil { // убрана только запись реестра
		t.Fatal(err)
	}
	if err := userauth.Revoke(ctx, s, anna); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Fatalf("Revoke of a person already out of the registry: %v, want ErrNoSuchUser", err)
	}
	if ds, _ := userauth.Devices(ctx, s, anna); len(ds) != 0 {
		t.Errorf("phones left after Revoke: %v", ds)
	}
	if got := pushOwners(t, s); len(got) != 0 {
		t.Errorf("push left after Revoke: %v", got)
	}
	if seen, err := (&userauth.Visits{Store: s}).Seen(ctx, anna); err != nil || len(seen) != 0 {
		t.Errorf("phone visits left after Revoke: %v, %v", seen, err)
	}
	// Purge такого человека не врет «удален»: файл входов уходит с остальным.
	if _, err := userauth.Purge(ctx, s, anna); err != nil {
		t.Fatal(err)
	}
	if exists(t, s, userauth.SeenKey(anna)) {
		t.Error("visits file survived Purge")
	}
}

// putCounter counts the writes by key.
type putCounter struct {
	blob.Store
	mu   sync.Mutex
	puts map[string]int
}

func (c *putCounter) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	c.mu.Lock()
	c.puts[key]++
	c.mu.Unlock()
	return c.Store.Put(ctx, key, data, ifMatch)
}

// Revoke по имени, у которого нечего убирать (опечатка), не переписывает общие
// файлы: лишняя запись — лишняя гонка за версию с другими экземплярами.
func TestRevokeOfAPersonWithNothingToCleanWritesNothing(t *testing.T) {
	ctx := context.Background()
	base := newStore(t)
	other := id(t, "other")
	if _, err := userauth.Add(ctx, base, other); err != nil {
		t.Fatal(err)
	}
	dev, _, err := userauth.AddDevice(ctx, base, other)
	if err != nil {
		t.Fatal(err)
	}
	subscribe(t, base, other, dev)
	s := &putCounter{Store: base, puts: map[string]int{}}
	if err := userauth.Revoke(ctx, s, id(t, "ghost")); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Fatalf("Revoke of a stranger: %v", err)
	}
	for _, key := range []string{userauth.RegistryKey, userauth.DevicesKey, userauth.PushKey} {
		if s.puts[key] != 0 {
			t.Errorf("Revoke of a stranger wrote %s %d times", key, s.puts[key])
		}
	}
}
