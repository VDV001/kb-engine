package kbserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	if err != nil || len(list) != 1 || list[0].Device != dev || list[0].At.IsZero() {
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
	if _, _, err := c.AddPhone(context.Background()); !errors.Is(err, kbserver.ErrNoMailbox) {
		t.Errorf("add: %v, want ErrNoMailbox", err)
	}
}

func TestClient_PhonesUnauthorized(t *testing.T) {
	srv := mailKB(t)
	c := kbserver.Client{URL: srv.URL, Token: "wrong", HTTP: srv.Client()}
	if _, _, err := c.AddPhone(context.Background()); !errors.Is(err, kbserver.ErrUnauthorized) {
		t.Errorf("add with a wrong token: %v", err)
	}
}
