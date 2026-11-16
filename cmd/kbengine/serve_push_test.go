package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	gosync "sync"
	"testing"
)

// pushRecorder stands in for the push services: запросы не уходят в сеть.
type pushRecorder struct {
	mu   gosync.Mutex
	reqs []*http.Request
	code int
}

func (p *pushRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = io.Copy(io.Discard, r.Body)
	p.reqs = append(p.reqs, r)
	code := p.code
	if code == 0 {
		code = http.StatusCreated
	}
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func newPushTenants(t *testing.T, rec *pushRecorder) tenantFixture {
	t.Helper()
	f := newTenants(t)
	f.h = tenantsHandler(t.Context(), f.store, &strings.Builder{}, withPush("mailto:push@example.com", rec))
	return f
}

// subscription is what PushSubscription.toJSON() gives in the browser.
func subscription(t *testing.T, endpoint string) string {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	b, _ := json.Marshal(map[string]any{"endpoint": endpoint, "keys": map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(auth),
	}})
	return string(b)
}

// Телефон подписывается, компьютер после ответа зовет notify — сервис push
// получает одно сообщение по адресу подписки (#622).
func TestMailPush(t *testing.T) {
	rec := &pushRecorder{}
	f := newPushTenants(t, rec)
	_, phone := addPhone(t, f, f.tokA)
	_, phone2 := addPhone(t, f, f.tokA)

	r := do(f.h, http.MethodGet, "/api/mail/push/key", "", phone)
	var key struct{ Key string }
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &key) != nil || key.Key == "" {
		t.Fatalf("key: %d %s", r.Code, r.Body.String())
	}
	if b, err := base64.RawURLEncoding.DecodeString(key.Key); err != nil || len(b) != 65 {
		t.Fatalf("key %q is not an uncompressed P-256 point", key.Key)
	}

	const ep = "https://fcm.googleapis.com/fcm/send/phone-1"
	if r := do(f.h, http.MethodPut, "/api/mail/push", subscription(t, ep), f.tokA); r.Code != http.StatusForbidden {
		t.Errorf("desk subscribes: %d, want 403", r.Code)
	}
	if r := do(f.h, http.MethodPut, "/api/mail/push", subscription(t, "https://169.254.169.254/x"), phone); r.Code != http.StatusBadRequest {
		t.Errorf("subscription to the cloud's metadata: %d, want 400", r.Code)
	}
	if r := do(f.h, http.MethodPut, "/api/mail/push", `{"endpoint":"`+ep+`"}`, phone); r.Code != http.StatusBadRequest {
		t.Errorf("no keys: %d, want 400", r.Code)
	}
	if r := do(f.h, http.MethodPut, "/api/mail/push", subscription(t, ep), phone); r.Code != http.StatusNoContent {
		t.Fatalf("subscribe: %d %s", r.Code, r.Body.String())
	}

	if r := do(f.h, http.MethodPost, "/api/mail/notify", "", phone); r.Code != http.StatusForbidden {
		t.Errorf("phone notifies: %d, want 403", r.Code)
	}
	if r := do(f.h, http.MethodPost, "/api/mail/notify", "", f.tokB); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"sent":0`) {
		t.Errorf("B notifies A's phone: %d %s", r.Code, r.Body.String())
	}
	if len(rec.reqs) != 0 {
		t.Fatalf("push went out before A notified: %d", len(rec.reqs))
	}
	r = do(f.h, http.MethodPost, "/api/mail/notify", "", f.tokA)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"sent":1`) {
		t.Fatalf("notify: %d %s", r.Code, r.Body.String())
	}
	if len(rec.reqs) != 1 || rec.reqs[0].URL.String() != ep || !strings.HasPrefix(rec.reqs[0].Header.Get("Authorization"), "vapid t=") {
		t.Fatalf("push requests: %+v", rec.reqs)
	}

	// Второй телефон не подписан — отписка без подписки не ошибка; подписанный отписывается.
	if r := do(f.h, http.MethodDelete, "/api/mail/push", "", phone2); r.Code != http.StatusNoContent {
		t.Errorf("unsubscribe without subscription: %d", r.Code)
	}
	if r := do(f.h, http.MethodDelete, "/api/mail/push", "", phone); r.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe: %d %s", r.Code, r.Body.String())
	}
	if r := do(f.h, http.MethodPost, "/api/mail/notify", "", f.tokA); !strings.Contains(r.Body.String(), `"sent":0`) || len(rec.reqs) != 1 {
		t.Errorf("after unsubscribe: %s, %d requests", r.Body.String(), len(rec.reqs))
	}
}

// Сервис push забыл подписку (410) — сервер ее стирает и больше туда не ходит.
func TestMailPushGone(t *testing.T) {
	rec := &pushRecorder{code: http.StatusGone}
	f := newPushTenants(t, rec)
	_, phone := addPhone(t, f, f.tokA)
	if r := do(f.h, http.MethodPut, "/api/mail/push", subscription(t, "https://web.push.apple.com/x"), phone); r.Code != http.StatusNoContent {
		t.Fatalf("subscribe: %d %s", r.Code, r.Body.String())
	}
	for range 2 {
		do(f.h, http.MethodPost, "/api/mail/notify", "", f.tokA)
	}
	if len(rec.reqs) != 1 {
		t.Fatalf("requests to a gone subscription: %d, want 1", len(rec.reqs))
	}
}

// Без KB_PUSH_CONTACT push выключен: ключа нет, подписка и рассылка — 404.
func TestMailPushOff(t *testing.T) {
	f := newTenants(t)
	_, phone := addPhone(t, f, f.tokA)
	for _, c := range []struct{ method, path, token string }{
		{http.MethodGet, "/api/mail/push/key", phone},
		{http.MethodPut, "/api/mail/push", phone},
		{http.MethodPost, "/api/mail/notify", f.tokA},
	} {
		body := ""
		if c.method == http.MethodPut {
			body = subscription(t, "https://fcm.googleapis.com/fcm/send/x")
		}
		if r := do(f.h, c.method, c.path, body, c.token); r.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", c.method, c.path, r.Code)
		}
	}
}
