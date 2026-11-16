package kbserver_test

import (
	"context"
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

type countPusher struct{ n int }

func (p *countPusher) Send(context.Context, domain.PushSubscription, []byte) error {
	p.n++
	return nil
}

func (p *countPusher) PublicKey(context.Context) (string, error) { return "K", nil }

func TestClient_Notify(t *testing.T) {
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := domain.NewUserID("alpha")
	ctx := context.Background()
	if _, err := userauth.Add(ctx, s, id); err != nil {
		t.Fatal(err)
	}
	dev, _, err := userauth.AddDevice(ctx, s, id)
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := domain.NewPushSubscription("https://fcm.googleapis.com/fcm/send/a", append([]byte{4}, make([]byte, 64)...), make([]byte, 16))
	if err := userauth.SetPush(ctx, s, id, dev, sub); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		push mail.Pusher
		want int
	}{{"push on", &countPusher{}, 1}, {"push off (404)", nil, 0}} {
		svc := mail.Service{Box: mailbox.Store{Blob: s}, Phones: userauth.Phones{Store: s}, Subs: userauth.Phones{Store: s}, Now: time.Now}
		if c.push != nil {
			svc.Push = c.push
		}
		h := httpapi.MailHandler(svc, func(string, ...any) {})
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(httpapi.WithMailCaller(r.Context(), id, domain.MailDesk)))
		}))
		n, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Notify(context.Background())
		srv.Close()
		if n != c.want || err != nil {
			t.Errorf("%s: Notify = %d, %v", c.name, n, err)
		}
	}
}
