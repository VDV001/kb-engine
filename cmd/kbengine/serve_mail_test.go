package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// sealed is a stand-in ciphertext: серверу все равно, что внутри, он хранит байты.
var sealed = base64.StdEncoding.EncodeToString([]byte("not-a-plaintext-0001"))

type mailList struct {
	Messages []struct {
		Seq    int64  `json:"seq"`
		Chat   string `json:"chat"`
		From   string `json:"from"`
		Sealed string `json:"sealed"`
	} `json:"messages"`
}

func mailOf(t *testing.T, f tenantFixture, token string, after string) mailList {
	t.Helper()
	rec := do(f.h, http.MethodGet, "/api/mail?after="+after, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/mail: %d %s", rec.Code, rec.Body.String())
	}
	var l mailList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// addPhone is the window pairing a phone: токен человека выпускает токен телефона.
func addPhone(t *testing.T, f tenantFixture, token string) (device, phone string) {
	t.Helper()
	rec := do(f.h, http.MethodPost, "/api/mail/devices", "", token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add phone: %d %s", rec.Code, rec.Body.String())
	}
	var d struct{ Device, Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || d.Token == "" {
		t.Fatalf("add phone: %s, %v", rec.Body.String(), err)
	}
	return d.Device, d.Token
}

// Телефон пишет, компьютер читает и отвечает; сторону называет токен, а не тело запроса.
func TestMailPhoneAndDesk(t *testing.T) {
	f := newTenants(t)
	_, phone := addPhone(t, f, f.tokA)
	rec := do(f.h, http.MethodPost, "/api/mail/c-1", `{"sealed":"`+sealed+`","from":"desk"}`, phone)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"seq":1`) {
		t.Fatalf("phone post: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodPost, "/api/mail/c-1", `{"sealed":"`+sealed+`"}`, f.tokA); rec.Code != http.StatusCreated {
		t.Fatalf("desk post: %d %s", rec.Code, rec.Body.String())
	}
	l := mailOf(t, f, f.tokA, "0")
	if len(l.Messages) != 2 || l.Messages[0].From != "phone" || l.Messages[1].From != "desk" || l.Messages[0].Sealed != sealed || l.Messages[0].Chat != "c-1" {
		t.Fatalf("desk reads %+v; want phone then desk, the body's «from» ignored", l.Messages)
	}
	if l := mailOf(t, f, phone, "1"); len(l.Messages) != 1 || l.Messages[0].Seq != 2 {
		t.Errorf("phone after 1: %+v", l.Messages)
	}
	if l := mailOf(t, f, f.tokB, "0"); len(l.Messages) != 0 {
		t.Errorf("B reads A's box: %+v", l.Messages)
	}
}

// Токен телефона открывает только ящик: ни базы, ни финансов, ни выпуска телефонов.
func TestMailPhoneTokenOpensOnlyTheBox(t *testing.T) {
	f := newTenants(t)
	_, phone := addPhone(t, f, f.tokA)
	for _, p := range []string{"/api/stats", "/api/entries", "/api/finances", "/kb/notes/n.md", "/"} {
		if rec := get(f.h, p, phone); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), secretA) {
			t.Errorf("phone on %s: %d", p, rec.Code)
		}
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "/api/mail/devices"}, {http.MethodGet, "/api/mail/devices"}, {http.MethodDelete, "/api/mail/devices/p-1"}} {
		if rec := do(f.h, c.method, c.path, "", phone); rec.Code != http.StatusForbidden {
			t.Errorf("phone %s %s: %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", "nope"); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token on the box: %d", rec.Code)
	}
}

// Отвязанный телефон больше не входит, а список телефонов видит только их хозяин.
func TestMailRevokePhone(t *testing.T) {
	f := newTenants(t)
	dev, phone := addPhone(t, f, f.tokA)
	rec := do(f.h, http.MethodGet, "/api/mail/devices", "", f.tokA)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), dev) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodGet, "/api/mail/devices", "", f.tokB); strings.Contains(rec.Body.String(), dev) {
		t.Error("B sees A's phones")
	}
	if rec := do(f.h, http.MethodDelete, "/api/mail/devices/"+dev, "", f.tokB); rec.Code != http.StatusNotFound {
		t.Errorf("B revokes A's phone: %d, want 404", rec.Code)
	}
	if rec := do(f.h, http.MethodDelete, "/api/mail/devices/"+dev, "", f.tokA); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	f.h = tenantsHandler(t.Context(), f.store, &strings.Builder{}) // реестр перечитывается раз в минуту — новый сервер читает сразу
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", phone); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked phone: %d, want 401", rec.Code)
	}
}

// Что нельзя положить в ящик.
func TestMailRefusals(t *testing.T) {
	f := newTenants(t)
	big := base64.StdEncoding.EncodeToString(make([]byte, domain.MaxMailSealed+1))
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/mail/Bad", `{"sealed":"` + sealed + `"}`, http.StatusBadRequest},
		{"/api/mail/c", `{"sealed":""}`, http.StatusBadRequest},
		{"/api/mail/c", `{"sealed":"не base64"}`, http.StatusBadRequest},
		{"/api/mail/c", `{"sealed":"` + big + `"}`, http.StatusRequestEntityTooLarge},
		{"/api/mail/c", `not json`, http.StatusBadRequest},
	} {
		if rec := do(f.h, http.MethodPost, c.path, c.body, f.tokA); rec.Code != c.want {
			t.Errorf("%s %.40s: %d, want %d", c.path, c.body, rec.Code, c.want)
		}
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=x", "", f.tokA); rec.Code != http.StatusBadRequest {
		t.Errorf("after=x: %d", rec.Code)
	}
}

// Привязка и отвязка действуют сразу, без минуты кэша реестра (ревью #630).
func TestMailPhonePairingIsImmediate(t *testing.T) {
	f := newTenants(t)
	_, first := addPhone(t, f, f.tokA)
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", first); rec.Code != http.StatusOK {
		t.Fatalf("first phone: %d", rec.Code)
	}
	dev, second := addPhone(t, f, f.tokA) // реестр уже прочитан первым телефоном
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", second); rec.Code != http.StatusOK {
		t.Errorf("a just paired phone: %d, want 200 at once", rec.Code)
	}
	if rec := do(f.h, http.MethodDelete, "/api/mail/devices/"+dev, "", f.tokA); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", second); rec.Code != http.StatusUnauthorized {
		t.Errorf("an unpaired phone: %d, want 401 at once", rec.Code)
	}
}

// Телефон человека, которого отозвали, в ящик не входит (ревью #630).
func TestMailPhoneOfRevokedPerson(t *testing.T) {
	f := newTenants(t)
	_, phone := addPhone(t, f, f.tokA)
	alpha, _ := domain.NewUserID("alpha")
	if err := revokeOnlyPerson(t, f.store, alpha); err != nil {
		t.Fatal(err)
	}
	f.h = tenantsHandler(t.Context(), f.store, &strings.Builder{})
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", phone); rec.Code != http.StatusUnauthorized {
		t.Errorf("phone of a revoked person: %d, want 401", rec.Code)
	}
}

// Тело больше предела — 413, а не «неверный формат» (ревью #630).
func TestMailBodyTooLarge(t *testing.T) {
	f := newTenants(t)
	huge := base64.StdEncoding.EncodeToString(make([]byte, 3*domain.MaxMailSealed))
	if rec := do(f.h, http.MethodPost, "/api/mail/c", `{"sealed":"`+huge+`"}`, f.tokA); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("huge body: %d, want 413", rec.Code)
	}
	if rec := do(f.h, http.MethodPost, "/api/mail/c", `{"sealed":"`+sealed+`","extra":1}`, f.tokA); rec.Code != http.StatusCreated {
		t.Errorf("unknown field: %d; the old window may send «from», it is ignored", rec.Code)
	}
}

// revokeOnlyPerson removes the person from the people registry only — так, как
// сделал бы старый `users revoke` или правка реестра руками: телефоны остаются
// в своем реестре, и сервер должен не пустить их сам.
func revokeOnlyPerson(t *testing.T, s blob.Store, id domain.UserID) error {
	t.Helper()
	o, err := s.Get(t.Context(), userauth.RegistryKey)
	if err != nil {
		return err
	}
	var r struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(o.Data, &r); err != nil {
		return err
	}
	r.Users = slices.DeleteFunc(r.Users, func(u map[string]any) bool { return u["id"] == id.String() })
	b, _ := json.Marshal(r)
	_, err = s.Put(t.Context(), userauth.RegistryKey, b, o.ETag)
	return err
}

// frozen stops the limiter's clock; the returned pointer moves it.
func frozen(f tenantFixture) *time.Time {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	f.h.(*tenants).now = func() time.Time { return now }
	return &now
}

// Предел опроса: 12 подряд проходят (10 страниц истории и запас), 13-й — 429
// с честным Retry-After. Отправка, другой телефон того же человека и сосед
// предел опроса не делят (ревью #631).
func TestMailRateLimit(t *testing.T) {
	f := newTenants(t)
	frozen(f)
	_, phone := addPhone(t, f, f.tokA)
	for i := range 12 {
		if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", f.tokA); rec.Code != http.StatusOK {
			t.Fatalf("poll %d: %d", i+1, rec.Code)
		}
	}
	rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", f.tokA)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("poll 13: %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After %q, want 5 (one poll per 5 s)", got)
	}
	if rec := do(f.h, http.MethodPost, "/api/mail/c-1", `{"sealed":"`+sealed+`"}`, f.tokA); rec.Code != http.StatusCreated {
		t.Errorf("send limited by polls: %d", rec.Code)
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", phone); rec.Code != http.StatusOK {
		t.Errorf("A's phone limited by A's desk: %d", rec.Code)
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", f.tokB); rec.Code != http.StatusOK {
		t.Errorf("B limited by A: %d", rec.Code)
	}
}

// Опрос раз в 10 с — договорной темп клиента — не получает 429 никогда.
func TestMailHonestPollIsNeverLimited(t *testing.T) {
	f := newTenants(t)
	now := frozen(f)
	for i := range 100 {
		*now = now.Add(10 * time.Second)
		if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", f.tokA); rec.Code != http.StatusOK {
			t.Fatalf("poll %d every 10 s: %d", i+1, rec.Code)
		}
	}
}

// Карта пределов не растет без конца: полные (простаивающие) корзины
// выбрасываются — новая корзина начинается такой же полной (ревью #631).
func TestMailLimitsForgetIdle(t *testing.T) {
	f := newTenants(t)
	now := frozen(f)
	tn := f.h.(*tenants)
	for i := range 5000 {
		tn.allowMail(mailKey{sum: sha256.Sum256([]byte(strconv.Itoa(i)))})
	}
	*now = now.Add(time.Hour)
	tn.allowMail(mailKey{poll: true})
	if n := len(tn.limits); n > 1 {
		t.Errorf("%d limiters kept after an idle hour, want only the fresh one", n)
	}
}

// Страница телефона — без токена (статика без данных), но только она: соседние
// пути по-прежнему за токеном (#622, этап 4).
func TestTenantsServeThePhonePage(t *testing.T) {
	f := newTenants(t)
	if rec := do(f.h, http.MethodGet, "/m/", "", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app.js") {
		t.Fatalf("/m/ without a token: %d %.80s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodGet, "/m/crypto.js", "", ""); rec.Code != http.StatusOK {
		t.Errorf("/m/crypto.js: %d", rec.Code)
	}
	for _, p := range []string{"/mail", "/m2/", "/api/mail"} {
		if rec := do(f.h, http.MethodGet, p, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d, want 401", p, rec.Code)
		}
	}
}

// assetlinks.json приложения Android — без токена, из KB_ANDROID_APP; не задано — 404.
func TestTenantsServeAssetLinks(t *testing.T) {
	fp := "D1:32:A9:ED:B6:87:63:E7:0A:97:F5:54:3D:DE:0F:27:AB:42:BE:21:B7:52:64:32:E8:B4:73:11:DD:24:EA:9E"
	t.Setenv("KB_ANDROID_APP", "ru.daniilvdovin.dvk:"+fp)
	f := newTenants(t)
	if rec := do(f.h, http.MethodGet, "/.well-known/assetlinks.json", "", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fp) {
		t.Fatalf("assetlinks: %d %.120s", rec.Code, rec.Body.String())
	}
	t.Setenv("KB_ANDROID_APP", "")
	if rec := do(newTenants(t).h, http.MethodGet, "/.well-known/assetlinks.json", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("без KB_ANDROID_APP: %d", rec.Code)
	}
}

// Окно видит, вошел ли привязанный телефон и как он зовется (#622): QR
// скрывается сам, а в списке не «p-9a7ffa8f», а «iPhone».
func TestMailPhoneVisitAndName(t *testing.T) {
	f := newTenants(t)
	dev, phone := addPhone(t, f, f.tokA)
	quiet, _ := addPhone(t, f, f.tokA)
	req := httptest.NewRequest(http.MethodGet, "/api/mail?after=0", nil)
	req.Header.Set("Authorization", "Bearer "+phone)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) Safari/604.1")
	if rec := httptest.NewRecorder(); func() int { f.h.ServeHTTP(rec, req); return rec.Code }() != http.StatusOK {
		t.Fatal("phone poll failed")
	}
	rec := do(f.h, http.MethodGet, "/api/mail/devices", "", f.tokA)
	var out struct {
		Devices []struct {
			Device, Name string
			Seen         *time.Time
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Devices {
		switch d.Device {
		case dev:
			if d.Name != "iPhone" || d.Seen == nil {
				t.Errorf("visited phone: %+v", d)
			}
		case quiet:
			if d.Name != "" || d.Seen != nil {
				t.Errorf("phone that never came in: %+v", d)
			}
		}
	}
}
