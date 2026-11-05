package kbserver_test

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
)

const token = "k3Jx9_secret-token-0123456789abcdefXYZ"

// fakeKB — kbengine serve в миниатюре за Serverless Container: /api/engine и
// /api/stats по HTTPS за токеном в X-KB-Token. Как платформа Yandex Cloud,
// запрос с Authorization он отбивает 403: Bearer там проверяется как IAM.
func fakeKB(t *testing.T) *httptest.Server {
	t.Helper()
	want := sha256.Sum256([]byte(token))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/engine", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.29.0","commit":"abc"}`))
	})
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total":2431,"by_kind":{"article":2000}}`))
	})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, `{"errorCode":403,"errorMessage":"Forbidden: Not authorized"}`, http.StatusForbidden)
			return
		}
		got := sha256.Sum256([]byte(r.Header.Get("X-KB-Token")))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_Info(t *testing.T) {
	srv := fakeKB(t)
	c := kbserver.Client{URL: srv.URL + "/", Token: token, HTTP: srv.Client()}
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.29.0" || info.Entries != 2431 {
		t.Fatalf("info = %+v", info)
	}
}

func TestClient_InfoErrors(t *testing.T) {
	srv := fakeKB(t)
	notKB := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>это сайт, а не kb</html>"))
	}))
	t.Cleanup(notKB.Close)
	down := httptest.NewTLSServer(http.NotFoundHandler())
	downURL, downClient := down.URL, down.Client()
	down.Close()
	tests := []struct {
		name string
		c    kbserver.Client
		want error
	}{
		{"без токена", kbserver.Client{URL: srv.URL, HTTP: srv.Client()}, kbserver.ErrUnauthorized},
		{"чужой токен", kbserver.Client{URL: srv.URL, Token: strings.Repeat("x", 40), HTTP: srv.Client()}, kbserver.ErrUnauthorized},
		{"сервер выключен", kbserver.Client{URL: downURL, Token: token, HTTP: downClient}, kbserver.ErrUnreachable},
		{"не kb-сервер", kbserver.Client{URL: notKB.URL, Token: token, HTTP: notKB.Client()}, kbserver.ErrNotKB},
		// Сертификату, которому система не доверяет, — отказ, а не тихое
		// согласие: иначе токен ушел бы тому, кто встал посередине.
		{"чужой сертификат", kbserver.Client{URL: srv.URL, Token: token, HTTP: &http.Client{}}, kbserver.ErrUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.c.Info(context.Background()); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// Сервер, который не отвечает, не вешает окно: ответ ограничен контекстом.
func TestClient_InfoRespectsContext(t *testing.T) {
	slow := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(slow.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := kbserver.Client{URL: slow.URL, Token: token, HTTP: slow.Client()}.Info(ctx)
	if !errors.Is(err, kbserver.ErrUnreachable) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v за %v", err, time.Since(start))
	}
}

// Редирект не выполняется: Go пересылает Authorization на тот же хост и при
// смене https на http — токен ушел бы открытым текстом мимо правила «http
// только на этот Mac». Цель редиректа не получает ни одного запроса.
func TestClient_RefusesRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(target.Close)
	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	t.Cleanup(redir.Close)
	if _, err := (kbserver.Client{URL: redir.URL, Token: token, HTTP: redir.Client()}).Info(context.Background()); err == nil {
		t.Fatal("редирект принят")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("клиент пошел по редиректу: %d запросов к цели", n)
	}
}

// Коды ответа различаются: настоящий kb-сервер с ошибкой (5xx, 429) — не «не
// kb-сервер», а «сервер ответил ошибкой»; 403 — вопрос доступа, как 401.
func TestClient_StatusMapping(t *testing.T) {
	for code, want := range map[int]error{
		http.StatusForbidden:           kbserver.ErrUnauthorized,
		http.StatusTooManyRequests:     kbserver.ErrServerError,
		http.StatusInternalServerError: kbserver.ErrServerError,
		http.StatusBadGateway:          kbserver.ErrServerError,
		http.StatusServiceUnavailable:  kbserver.ErrServerError,
		http.StatusNotFound:            kbserver.ErrNotKB,
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		_, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Info(context.Background())
		srv.Close()
		if !errors.Is(err, want) {
			t.Errorf("%d: err = %v, want %v", code, err, want)
		}
	}
}

// Отказ от редиректа различим: сервер ответил, но перенаправил — человеку надо
// поправить адрес, а не искать VPN.
func TestClient_RedirectIsItsOwnError(t *testing.T) {
	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example"+r.URL.Path, http.StatusMovedPermanently)
	}))
	t.Cleanup(redir.Close)
	_, err := kbserver.Client{URL: redir.URL, Token: token, HTTP: redir.Client()}.Info(context.Background())
	if !errors.Is(err, kbserver.ErrRedirect) || errors.Is(err, kbserver.ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
}

// Адрес с путем (kb за прокси на /kb/) — запросы идут под этот путь.
func TestClient_URLWithPath(t *testing.T) {
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/api/engine") {
			_, _ = w.Write([]byte(`{"version":"0.29.0"}`))
			return
		}
		_, _ = w.Write([]byte(`{"total":3}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := (kbserver.Client{URL: srv.URL + "/kb/", Token: token, HTTP: srv.Client()}).Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/kb/api/engine" || paths[1] != "/kb/api/stats" {
		t.Fatalf("paths = %q", paths)
	}
}

// Поиск идет на /api/search того же сервера, за тем же Bearer. Запрос с
// кириллицей, «&» и «#» доходит целиком: склеенный в путь, он оборвался бы на
// «&» или ушел бы в fragment.
func TestClient_Search(t *testing.T) {
	const q = "договор & ключ #5"
	want := sha256.Sum256([]byte(token))
	var got string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := sha256.Sum256([]byte(r.Header.Get("X-KB-Token")))
		if subtle.ConstantTimeCompare(bearer[:], want[:]) != 1 {
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/kb/api/search" {
			http.NotFound(w, r)
			return
		}
		got = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`[{"id":7,"title":"первый"},{"id":3,"title":"второй"}]`))
	}))
	t.Cleanup(srv.Close)
	c := kbserver.Client{URL: srv.URL + "/kb/", Token: token, HTTP: srv.Client()}
	res, err := c.Search(context.Background(), q, 0)
	entries := res.Entries
	if err != nil {
		t.Fatal(err)
	}
	if got != q {
		t.Fatalf("сервер получил q = %q, want %q", got, q)
	}
	// Записи приходят как есть и в порядке сервера: окно показывает их той же
	// разметкой, что и записи своего каталога.
	if len(entries) != 2 || string(entries[0]) != `{"id":7,"title":"первый"}` || string(entries[1]) != `{"id":3,"title":"второй"}` {
		t.Fatalf("entries = %s", entries)
	}
	if _, err := (kbserver.Client{URL: srv.URL + "/kb/", HTTP: srv.Client()}).Search(context.Background(), q, 0); !errors.Is(err, kbserver.ErrUnauthorized) {
		t.Fatalf("без токена: err = %v", err)
	}
}

// Выдача поиска весит больше, чем ответы /api/engine и /api/stats: широкий
// запрос по каталогу в тысячи записей — мегабайты. Предел чтения у поиска
// свой, а ответ не того вида (объект вместо списка) — «не kb-сервер».
func TestClient_SearchBodies(t *testing.T) {
	big := `[` + strings.Repeat(`{"id":1,"title":"`+strings.Repeat("я", 500)+`"},`, 3000) + `{"id":2}]`
	for _, tt := range []struct {
		name, body string
		n          int
		want       error
	}{
		{"выдача в несколько мегабайт", big, 3001, nil},
		{"пустая выдача", `[]`, 0, nil},
		{"объект вместо списка", `{"total":3}`, 0, kbserver.ErrNotKB},
		{"страница вместо JSON", `<html>сайт</html>`, 0, kbserver.ErrNotKB},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			res, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Search(context.Background(), "q", 0)
			entries := res.Entries
			if !errors.Is(err, tt.want) || (tt.want == nil && len(entries) != tt.n) {
				t.Fatalf("err = %v, записей %d, want %v и %d", err, len(entries), tt.want, tt.n)
			}
		})
	}
}

// Выдача больше предела чтения — не «не kb-сервер»: адрес верный, сервер
// здоров, просто ответ слишком велик. Человеку говорится об этом.
func TestClient_SearchTooLarge(t *testing.T) {
	item := `{"id":1,"title":"` + strings.Repeat("x", 1000) + `"},`
	body := "[" + strings.Repeat(item, 34*1024) + `{"id":2}]` // ~34 МБ > 32 МБ
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	_, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Search(context.Background(), "q", 0)
	if !errors.Is(err, kbserver.ErrTooLarge) || errors.Is(err, kbserver.ErrNotKB) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// limit уходит серверу; сервер текущей версии отвечает объектом (первые N,
// счет, ключи остальных), сервер 0.30.0 limit не знает и отвечает массивом —
// клиент понимает оба: массив значит «все найденное здесь, ключей отдельно нет».
func TestClient_SearchLimit(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		entries    int
		total      int
		keys       []string
	}{
		{"сервер с limit", `{"entries":[{"id":1,"title":"a"}],"total":3,"keys":["url x","id 3 c"]}`, 1, 3, []string{"url x", "id 3 c"}},
		{"сервер 0.30.0: массив", `[{"id":1,"title":"a"},{"id":2,"title":"b"}]`, 2, 2, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var gotLimit string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotLimit = r.URL.Query().Get("limit")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			res, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Search(context.Background(), "q", 40)
			if err != nil {
				t.Fatal(err)
			}
			if gotLimit != "40" || len(res.Entries) != tt.entries || res.Total != tt.total || !slices.Equal(res.Keys, tt.keys) {
				t.Fatalf("limit=%q, res = %d записей, total %d, keys %q", gotLimit, len(res.Entries), res.Total, res.Keys)
			}
		})
	}
	// Объект, где счет не сходится с записями и ключами, без entries или с
	// ключом-простыней — не kb-сервер.
	long := `"` + strings.Repeat("k", 5000) + `"`
	for _, bad := range []string{
		`{"total":1,"entries":[{"id":1},{"id":2}]}`,
		`{"total":3}`,
		`{"total":3,"entries":[{"id":1}],"keys":["a"]}`,
		`{"total":5,"entries":[{"id":1}],"keys":["a","b"]}`,
		`{"total":2,"entries":[{"id":1}],"keys":[` + long + `]}`,
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(bad)) }))
		_, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Search(context.Background(), "q", 40)
		srv.Close()
		if !errors.Is(err, kbserver.ErrNotKB) {
			t.Errorf("%s: err = %v, want ErrNotKB", bad, err)
		}
	}
}

// null вместо списка (прокси, сборка, что пустое кодирует null) — пустая
// выдача, а не «не kb-сервер».
func TestClient_SearchNull(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("null")) }))
	defer srv.Close()
	res, err := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}.Search(context.Background(), "q", 40)
	if err != nil || len(res.Entries) != 0 || res.Total != 0 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

// Клиент и сервер называют заголовок токена одной константой: переименуй
// одну сторону — окно перестанет входить, а тесты каждой стороны остались бы
// зелеными.
func TestClient_TokenHeaderIsTheServers(t *testing.T) {
	if kbserver.TokenHeader != httpapi.TokenHeader {
		t.Fatalf("client sends %q, server reads %q", kbserver.TokenHeader, httpapi.TokenHeader)
	}
}

// 403 — отказ до приложения (облако, прокси), 401 — отказ приложения или
// сервер старой версии без X-KB-Token: человек должен видеть разницу.
func TestClient_DescribeTells403From401(t *testing.T) {
	for code, says := range map[int]string{http.StatusForbidden: "403", http.StatusUnauthorized: "старой версии"} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		c := kbserver.Client{URL: srv.URL, Token: token, HTTP: srv.Client()}
		_, err := c.Info(context.Background())
		srv.Close()
		if !errors.Is(err, kbserver.ErrUnauthorized) {
			t.Fatalf("%d: err = %v, want ErrUnauthorized", code, err)
		}
		if d := kbserver.Describe(err); !strings.Contains(d, says) {
			t.Errorf("%d: Describe = %q, want it to name %q", code, d, says)
		}
	}
}
