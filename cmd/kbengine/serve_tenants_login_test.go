package main

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста (sync_test.go)
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// Как токен читается из запроса (#545): два заголовка не дают выбрать удобный,
// пустой X-KB-Token не уступает место Authorization, лишние символы не входят.
func TestTenantsTokenHeaderEdges(t *testing.T) {
	f := newTenants(t)
	cases := []struct {
		name    string
		header  http.Header
		query   string
		want    int
		onlySee string // чья метка в ответе; пусто — тело не проверяется
	}{
		{"X-KB-Token B wins over Bearer A", http.Header{"X-Kb-Token": {f.tokB}, "Authorization": {"Bearer " + f.tokA}}, "", 200, secretB},
		{"X-KB-Token A wins over Bearer B", http.Header{"X-Kb-Token": {f.tokA}, "Authorization": {"Bearer " + f.tokB}}, "", 200, secretA},
		{"empty X-KB-Token does not fall back to Bearer", http.Header{"X-Kb-Token": {""}, "Authorization": {"Bearer " + f.tokA}}, "", 401, ""},
		{"two X-KB-Token values: the first decides", http.Header{"X-Kb-Token": {f.tokA, f.tokB}}, "", 200, secretA},
		{"double space before the token", http.Header{"Authorization": {"Bearer  " + f.tokA}}, "", 401, ""},
		{"trailing space", http.Header{"X-Kb-Token": {f.tokA + " "}}, "", 401, ""},
		{"trailing newline", http.Header{"X-Kb-Token": {f.tokA + "\n"}}, "", 401, ""},
		{"truncated token", http.Header{"X-Kb-Token": {f.tokA[:len(f.tokA)-1]}}, "", 401, ""},
		{"Basic scheme", http.Header{"Authorization": {"Basic " + f.tokA}}, "", 401, ""},
		{"token in the query string", nil, "?token=" + f.tokA, 401, ""},
		{"lowercase scheme is accepted", http.Header{"Authorization": {"bearer " + f.tokA}}, "", 200, secretA},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/entries"+c.query, nil)
			maps.Copy(req.Header, c.header)
			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
			body := rec.Body.String()
			switch c.onlySee {
			case secretA:
				if !strings.Contains(body, secretA) || strings.Contains(body, secretB) {
					t.Errorf("want only A's data: %s", body)
				}
			case secretB:
				if !strings.Contains(body, secretB) || strings.Contains(body, secretA) {
					t.Errorf("want only B's data: %s", body)
				}
			}
		})
	}
}

// Отозванный человек не входит ни по токену, ни по токену своего телефона, а
// сосед не затронут (#545). Окно обновления реестра — userauth.RefreshEvery:
// проверка стоит после него, а не внутри.
func TestTenantsRevokedPersonAndPhoneAreRefused(t *testing.T) {
	f := newTenants(t)
	frozen(f)
	_, phone := addPhone(t, f, f.tokA)
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", phone); rec.Code != http.StatusOK {
		t.Fatalf("phone before revoke: %d", rec.Code)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	tn := f.h.(*tenants)
	tn.reg = userauth.NewRegistry(context.Background(), f.store, userauth.WithClock(clock))
	tn.phones = userauth.NewRegistry(context.Background(), f.store, userauth.WithKey(userauth.DevicesKey), userauth.WithClock(clock))
	if rec := do(f.h, http.MethodGet, "/api/entries", "", f.tokA); rec.Code != http.StatusOK {
		t.Fatalf("A before revoke: %d", rec.Code)
	}

	alpha, err := domain.NewUserID("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := userauth.Revoke(context.Background(), f.store, alpha); err != nil {
		t.Fatal(err)
	}
	now = now.Add(userauth.RefreshEvery + time.Second)

	if rec := do(f.h, http.MethodGet, "/api/entries", "", f.tokA); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked A's token: %d, want 401", rec.Code)
	}
	if rec := do(f.h, http.MethodGet, "/api/mail?after=0", "", phone); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked A's phone: %d, want 401", rec.Code)
	}
	rec := do(f.h, http.MethodGet, "/api/entries", "", f.tokB)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), secretB) || strings.Contains(rec.Body.String(), secretA) {
		t.Errorf("B after A's revoke: %d %s", rec.Code, rec.Body.String())
	}
}

// manyTenants registers people before the handler is built, so each one's
// base is built by the first request, as on a fresh container start.
func manyTenants(t *testing.T, names []string) (http.Handler, map[string]string) {
	t.Helper()
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	toks := map[string]string{}
	for _, name := range names {
		uid, err := domain.NewUserID(name)
		if err != nil {
			t.Fatal(err)
		}
		tok, err := userauth.Add(ctx, s, uid)
		if err != nil {
			t.Fatal(err)
		}
		cat := `{"entries":[{"id":1,"habr_id":1,"title":"` + marker(name) + `","url":"https://h/` + name +
			`","category":"golang","status":"keep","author":"x"}]}`
		cur, err := s.Get(ctx, userauth.CatalogKey(uid))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(ctx, userauth.CatalogKey(uid), []byte(cat), cur.ETag); err != nil {
			t.Fatal(err)
		}
		toks[name] = tok
	}
	var stderr bytes.Buffer
	return tenantsHandler(ctx, s, &stderr), toks
}

// Без < > &: JSON экранирует их, и поиск подстроки в ответе промахнулся бы
// и по своей метке, и по чужой. Ограничители не дают «a» быть началом «a-b».
func marker(name string) string { return "M_" + name + "_E" }

// Первые запросы разных людей одновременно (#545, запуск с -race): каждый
// собирает свою базу и видит только свою метку.
func TestTenantsFirstRequestsRaceStayApart(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("person-%d", i)
	}
	h, toks := manyTenants(t, names)
	var wg gosync.WaitGroup
	errs := make(chan string, len(names)*4)
	for _, name := range names {
		for range 4 { // и несколько запросов одного человека разом
			wg.Go(func() {
				rec := do(h, http.MethodGet, "/api/entries", "", toks[name])
				body := rec.Body.String()
				if rec.Code != http.StatusOK || !strings.Contains(body, marker(name)) {
					errs <- fmt.Sprintf("%s: %d, own marker missing", name, rec.Code)
				}
				for _, other := range names {
					if other != name && strings.Contains(body, marker(other)) {
						errs <- fmt.Sprintf("%s sees %s's data", name, other)
					}
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// Имена, из которых одно — начало другого, и дефис (#545): ключи users/a/ и
// users/a-b/ не пересекаются ни в каталоге, ни в файлах ящика.
func TestTenantsSharedNamePrefixesStayApart(t *testing.T) {
	names := []string{"a", "a-b", "a-bc", "ab", "a1"}
	h, toks := manyTenants(t, names)
	for _, name := range names {
		rec := do(h, http.MethodGet, "/api/entries", "", toks[name])
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, marker(name)) {
			t.Fatalf("%s: %d, own marker missing", name, rec.Code)
		}
		for _, other := range names {
			if other != name && strings.Contains(body, marker(other)) {
				t.Errorf("%s sees %s's data", name, other)
			}
		}
	}
}
