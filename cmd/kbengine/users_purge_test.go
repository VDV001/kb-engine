package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
)

// purgeFixture is a dir store with anna and anna-b, each with a base and a
// mailbox file.
func purgeFixture(t *testing.T) (dir string, tokens map[string]string) {
	t.Helper()
	dir = t.TempDir()
	tokens = map[string]string{"anna": addToFile(t, dir, "anna"), "anna-b": addToFile(t, dir, "anna-b")}
	s, err := blobfs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"anna", "anna-b"} {
		for _, key := range []string{"users/" + who + "/kb/notes/n.md", "mailfiles/users/" + who + "/f1"} {
			if _, err := s.Put(context.Background(), key, []byte("x"), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir, tokens
}

func present(t *testing.T, dir, key string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
	return err == nil
}

// purge runs the command with --yes: так тесты проходят предпросмотр.
func purge(args ...string) (int, string, string) {
	return purgeRaw(append(args, "--yes")...)
}

func purgeRaw(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(append([]string{"users", "purge"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// Полное удаление: вход закрыт, база и файлы ящика ушли, сосед anna-b цел;
// в терминал ничего, кроме отчета на stderr.
func TestUsersPurgeRemovesThePerson(t *testing.T) {
	dir, toks := purgeFixture(t)
	code, out, errb := purge("--tenants", dir, "--id", "anna", "--confirm", "anna", "--wait", "0s")
	if code != 0 || out != "" {
		t.Fatalf("purge: exit %d, stdout %q, stderr %s", code, out, errb)
	}
	if _, ok := lookup(t, dir, toks["anna"]); ok {
		t.Error("purged token still admitted")
	}
	if _, ok := lookup(t, dir, toks["anna-b"]); !ok {
		t.Error("the neighbour lost the login")
	}
	for _, key := range []string{"users/anna/kb/notes/n.md", "mailfiles/users/anna/f1"} {
		if present(t, dir, key) {
			t.Errorf("%s survived", key)
		}
	}
	for _, key := range []string{"users/anna-b/kb/notes/n.md", "mailfiles/users/anna-b/f1"} {
		if !present(t, dir, key) {
			t.Errorf("%s of the neighbour was removed", key)
		}
	}
	for _, want := range []string{"anna", "30"} { // имя и честная оговорка про 30 дней версий реестров
		if !strings.Contains(errb, want) {
			t.Errorf("the report does not mention %q: %s", want, errb)
		}
	}
}

// Удаление необратимо: имя печатается второй раз, и опечатка не удаляет ничего.
func TestUsersPurgeNeedsTheNameTwice(t *testing.T) {
	dir, toks := purgeFixture(t)
	cases := []struct {
		name string
		args []string
		says string
	}{
		{"no confirm", []string{"--tenants", dir, "--id", "anna", "--wait", "0s"}, "--confirm"},
		{"another name", []string{"--tenants", dir, "--id", "anna", "--confirm", "anna-b", "--wait", "0s"}, "--confirm"},
		{"empty confirm", []string{"--tenants", dir, "--id", "anna", "--confirm", "", "--wait", "0s"}, "--confirm"},
	}
	for _, c := range cases {
		code, _, errb := purge(c.args...)
		if code != 2 || !strings.Contains(errb, c.says) {
			t.Errorf("%s: exit %d, stderr %q; want 2 naming %q", c.name, code, errb, c.says)
		}
	}
	if _, ok := lookup(t, dir, toks["anna"]); !ok || !present(t, dir, "users/anna/kb/notes/n.md") {
		t.Error("a refused purge changed something")
	}
}

// Имя, которого нет ни в реестре, ни в хранилище, — опечатка, а не успех.
func TestUsersPurgeOfAnUnknownNameFails(t *testing.T) {
	dir, _ := purgeFixture(t)
	code, _, errb := purge("--tenants", dir, "--id", "ghost", "--confirm", "ghost", "--wait", "0s")
	if code != 1 || !strings.Contains(errb, "ghost") {
		t.Errorf("exit %d, stderr %q; want 1 naming ghost", code, errb)
	}
}

// Сначала закрывается вход, потом пауза, пока другие экземпляры сервера узнают
// (userauth.RefreshEvery), и только потом удаление: человек с еще живым
// токеном иначе записал бы в базу после удаления. Пауза по умолчанию не короче окна.
func TestUsersPurgeClosesTheDoorThenWaitsThenRemoves(t *testing.T) {
	dir, toks := purgeFixture(t)
	var slept time.Duration
	var doorClosed, dataThere bool
	prev := usersPurgeSleep
	usersPurgeSleep = func(_ context.Context, d time.Duration) error {
		slept = d
		_, ok := lookup(t, dir, toks["anna"])
		doorClosed = !ok
		dataThere = present(t, dir, "users/anna/kb/notes/n.md")
		return nil
	}
	t.Cleanup(func() { usersPurgeSleep = prev })
	if code, _, errb := purge("--tenants", dir, "--id", "anna", "--confirm", "anna"); code != 0 {
		t.Fatalf("purge: exit %d, %s", code, errb)
	}
	if slept <= userauth.MaxStale {
		t.Errorf("default wait %v, want longer than MaxStale %v: a failed registry read keeps the old list that long", slept, userauth.MaxStale)
	}
	if !doorClosed {
		t.Error("the token still admitted while waiting")
	}
	if !dataThere {
		t.Error("data was already removed before the wait")
	}
	if present(t, dir, "users/anna/kb/notes/n.md") {
		t.Error("data survived after the wait")
	}
}

// Прерванная пауза: вход закрыт, данные целы, повтор команды доводит дело.
func TestUsersPurgeInterruptedWaitKeepsTheData(t *testing.T) {
	dir, toks := purgeFixture(t)
	prev := usersPurgeSleep
	usersPurgeSleep = func(ctx context.Context, _ time.Duration) error { return context.Canceled }
	t.Cleanup(func() { usersPurgeSleep = prev })
	code, _, errb := purge("--tenants", dir, "--id", "anna", "--confirm", "anna")
	if code != 1 || !strings.Contains(errb, "users purge") {
		t.Errorf("exit %d, stderr %q; want 1", code, errb)
	}
	if _, ok := lookup(t, dir, toks["anna"]); ok {
		t.Error("the door stayed open")
	}
	if !present(t, dir, "users/anna/kb/notes/n.md") {
		t.Error("data was removed by an interrupted purge")
	}
}

// Хранилище без Purge отказывает до отзыва: вход цел.
func TestUsersPurgeOnAStoreWithoutPurgeChangesNothing(t *testing.T) {
	dir, toks := purgeFixture(t)
	prev := usersOpenStore
	usersOpenStore = func(loc, _ string) (blob.Store, error) {
		s, err := blobfs.New(loc)
		return struct{ blob.Store }{s}, err
	}
	t.Cleanup(func() { usersOpenStore = prev })
	code, _, errb := purge("--tenants", dir, "--id", "anna", "--confirm", "anna", "--wait", "0s")
	if code != 1 || !strings.Contains(errb, "purge") {
		t.Errorf("exit %d, stderr %q; want 1", code, errb)
	}
	if _, ok := lookup(t, dir, toks["anna"]); !ok {
		t.Error("the person was revoked by a refused purge")
	}
}

// Повтор после прерванной паузы ждет снова: отозвать человека во второй раз
// нечем, но другие экземпляры могли еще не узнать об отзыве. То же, если
// владелец сделал users revoke и сразу users purge.
func TestUsersPurgeRepeatAfterInterruptionStillWaits(t *testing.T) {
	dir, _ := purgeFixture(t)
	calls := 0
	prev := usersPurgeSleep
	usersPurgeSleep = func(context.Context, time.Duration) error {
		calls++
		if calls == 1 {
			return context.Canceled
		}
		return nil
	}
	t.Cleanup(func() { usersPurgeSleep = prev })
	if code, _, _ := purge("--tenants", dir, "--id", "anna", "--confirm", "anna"); code != 1 {
		t.Fatalf("interrupted run: exit %d", code)
	}
	if code, _, errb := purge("--tenants", dir, "--id", "anna", "--confirm", "anna"); code != 0 {
		t.Fatalf("repeat: exit %d, %s", code, errb)
	}
	if calls != 2 {
		t.Errorf("the wait ran %d times over two runs, want 2", calls)
	}
}

func TestUsersPurgeRefusesANegativeWait(t *testing.T) {
	dir, _ := purgeFixture(t)
	code, _, errb := purge("--tenants", dir, "--id", "anna", "--confirm", "anna", "--wait", "-1s")
	if code != 2 || !strings.Contains(errb, "--wait") {
		t.Errorf("exit %d, stderr %q; want 2 naming --wait", code, errb)
	}
}

// lateWriter writes under the person's base right after the first purge of it,
// as a request admitted before the revoke would.
type lateWriter struct {
	*blobfs.Store
	done bool
}

func (l *lateWriter) Purge(ctx context.Context, prefix string) (int, error) {
	n, err := l.Store.Purge(ctx, prefix)
	if prefix == "users/anna/" && !l.done {
		l.done = true
		_, _ = l.Put(ctx, "users/anna/kb/late.md", []byte("late"), "")
	}
	return n, err
}

// Запись, пришедшая после удаления, не остается молча: второй проход находит ее,
// убирает и говорит вслух, что отчет «удален» был рано.
func TestUsersPurgeReportsAWriteAfterTheRemoval(t *testing.T) {
	dir, _ := purgeFixture(t)
	prev := usersOpenStore
	usersOpenStore = func(loc, _ string) (blob.Store, error) {
		s, err := blobfs.New(loc)
		return &lateWriter{Store: s}, err
	}
	t.Cleanup(func() { usersOpenStore = prev })
	code, _, errb := purge("--tenants", dir, "--id", "anna", "--confirm", "anna", "--wait", "0s")
	if code != 1 || !strings.Contains(errb, "после удаления") {
		t.Errorf("exit %d, stderr %q; want 1 naming a write after the removal", code, errb)
	}
	if present(t, dir, "users/anna/kb/late.md") {
		t.Error("the late write survived the second pass")
	}
}

// Без --yes команда ничего не меняет: печатает, что есть у человека, и просит
// повторить. Имя, набранное дважды верно, но не про того человека, так не
// удаляет молча.
func TestUsersPurgeShowsWhatItWillRemoveAndNeedsYes(t *testing.T) {
	dir, toks := purgeFixture(t)
	code, out, errb := purgeRaw("--tenants", dir, "--id", "anna", "--confirm", "anna", "--wait", "0s")
	if code != 2 || out != "" {
		t.Fatalf("exit %d, stdout %q; want 2 and nothing on stdout", code, out)
	}
	for _, want := range []string{"--yes", "anna", "в реестре: да", "база: есть"} {
		if !strings.Contains(errb, want) {
			t.Errorf("the preview lacks %q: %s", want, errb)
		}
	}
	if _, ok := lookup(t, dir, toks["anna"]); !ok || !present(t, dir, "users/anna/kb/notes/n.md") {
		t.Error("the preview changed something")
	}
}

func TestUsersPurgePreviewOfAnUnknownName(t *testing.T) {
	dir, _ := purgeFixture(t)
	code, _, errb := purgeRaw("--tenants", dir, "--id", "ghost", "--confirm", "ghost", "--wait", "0s")
	if code != 2 || !strings.Contains(errb, "в реестре: нет") || !strings.Contains(errb, "база: нет") {
		t.Errorf("exit %d, stderr %q; want 2 saying the name is nowhere", code, errb)
	}
}
