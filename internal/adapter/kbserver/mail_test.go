package kbserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/mailbox"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

// mailKB is the real mailbox handler (httpapi.MailHandler на настоящем
// хранилище), а не подделка ответов: формы JSON и коды берутся у сервера.
// Человека по токену узнает обертка, как serve --tenants.
func mailKB(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewUserID("alpha")
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.MailHandler(mail.Service{Box: mailbox.Store{Blob: s}, Phones: userauth.Phones{Store: s}, Now: time.Now},
		func(string, ...any) {})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(kbserver.TokenHeader) != token {
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r.WithContext(httpapi.WithMailCaller(r.Context(), id, domain.MailDesk)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_Phones(t *testing.T) {
	srv := mailKB(t)
	c := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}
	ctx := context.Background()
	if list, err := c.Phones(ctx); err != nil || len(list) != 0 {
		t.Fatalf("no phones yet: %+v, %v", list, err)
	}
	dev, tok, err := c.AddPhone(ctx)
	if err != nil || dev == "" || tok == "" || tok == token {
		t.Fatalf("add: %q %q %v", dev, tok, err)
	}
	list, err := c.Phones(ctx)
	if err != nil || len(list) != 1 || list[0].Device != dev || list[0].At.IsZero() || !list[0].Seen.IsZero() || list[0].Name != "" {
		t.Fatalf("after add: %+v, %v", list, err)
	}
	if err := c.RevokePhone(ctx, dev); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if list, err := c.Phones(ctx); err != nil || len(list) != 0 {
		t.Fatalf("after revoke: %+v, %v", list, err)
	}
	if err := c.RevokePhone(ctx, dev); !errors.Is(err, kbserver.ErrRejected) && !errors.Is(err, kbserver.ErrNotKB) {
		t.Errorf("revoke twice: %v, want a refusal", err)
	}
}

// kb-сервер без ящика (одна база, не --tenants, или еще не обновлен) — своя
// ошибка: окно скажет «на сервере нет ящика», а не «это не kb-сервер».
func TestClient_PhonesWithoutMailbox(t *testing.T) {
	srv := fakeKB(t)
	c := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}
	if _, err := c.Phones(context.Background()); !errors.Is(err, kbserver.ErrNoMailbox) {
		t.Errorf("phones: %v, want ErrNoMailbox", err)
	}
	_, _, err := c.AddPhone(context.Background())
	if !errors.Is(err, kbserver.ErrNoMailbox) {
		t.Errorf("add: %v, want ErrNoMailbox", err)
	}
	// Слова — про ящик, а не «нет связи»: адрес и токен верные, сервер не тот.
	if d := kbserver.Describe(err); !strings.Contains(d, "ящик") || strings.Contains(d, "нет связи") {
		t.Errorf("Describe = %q, want it to name the mailbox", d)
	}
}

func TestClient_PhonesUnauthorized(t *testing.T) {
	srv := mailKB(t)
	c := kbserver.Client{URL: srv.URL, Token: "wrong", HTTP: srv.Client()}
	if _, _, err := c.AddPhone(context.Background()); !errors.Is(err, kbserver.ErrUnauthorized) {
		t.Errorf("add with a wrong token: %v", err)
	}
}

// Отказ ящика — слова по делу: «отвяжите телефон», а не «обновите окно или
// сервер до одной версии» (ревью #633).
func TestClient_DescribeMailRefusals(t *testing.T) {
	srv := mailKB(t)
	c := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}
	var err error
	for range 20 {
		if _, _, err = c.AddPhone(context.Background()); err != nil {
			break
		}
	}
	if !errors.Is(err, kbserver.ErrRejected) {
		t.Fatalf("over the limit: %v, want ErrRejected", err)
	}
	if d := kbserver.Describe(err); !strings.Contains(d, "отвяжите") || strings.Contains(d, "версии") {
		t.Errorf("too many phones: %q", d)
	}
	if d := kbserver.Describe(c.RevokePhone(context.Background(), "p-none")); !strings.Contains(d, "телефона нет") || strings.Contains(d, "версии") {
		t.Errorf("no such phone: %q", d)
	}
}

// Ящик: положить шифротекст в чат и забрать новое после номера — настоящим
// обработчиком; сторону называет токен (здесь — компьютер).
func TestClient_MailPostAndSince(t *testing.T) {
	srv := mailKB(t)
	c := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}
	ctx := context.Background()
	if p, err := c.MailSince(ctx, 0); err != nil || len(p.Messages) != 0 || p.More {
		t.Fatalf("empty box: %+v, %v", p, err)
	}
	for i, body := range [][]byte{[]byte("sealed-1"), []byte("sealed-2")} {
		seq, err := c.MailPost(ctx, "c-1", body)
		if err != nil || seq != int64(i+1) {
			t.Fatalf("post %d: seq %d, %v", i, seq, err)
		}
	}
	p, err := c.MailSince(ctx, 1)
	if err != nil || len(p.Messages) != 1 {
		t.Fatalf("since 1: %+v, %v", p, err)
	}
	m := p.Messages[0]
	if m.Seq != 2 || m.Chat != "c-1" || m.From != "desk" || string(m.Sealed) != "sealed-2" || m.At.IsZero() || p.First != 1 {
		t.Errorf("message: %+v, first %d", m, p.First)
	}
	if _, err := c.MailPost(ctx, "Bad Chat", []byte("x")); !errors.Is(err, kbserver.ErrRejected) {
		t.Errorf("bad chat: %v, want ErrRejected", err)
	}
}
