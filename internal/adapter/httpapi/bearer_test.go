package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

// Сервер, открытый в сеть, отдает базу только с токеном: каждый запрос, включая
// страницы и /metrics, без верного Bearer получает 401 и ничего не узнает.
func TestRequireBearer(t *testing.T) {
	const token = "k3Jx9_secret-token-0123456789abcdefXYZ"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("catalog")) })
	h := httpapi.RequireBearer(token, next)
	tests := []struct {
		name, header string
		want         int
	}{
		{"без заголовка", "", http.StatusUnauthorized},
		{"верный токен", "Bearer " + token, http.StatusOK},
		{"схема без учета регистра", "bearer " + token, http.StatusOK},
		{"чужой токен", "Bearer " + strings.Repeat("x", len(token)), http.StatusUnauthorized},
		{"префикс токена", "Bearer " + token[:10], http.StatusUnauthorized},
		{"токен длиннее", "Bearer " + token + "x", http.StatusUnauthorized},
		{"Basic вместо Bearer", "Basic " + token, http.StatusUnauthorized},
		{"пустой Bearer", "Bearer ", http.StatusUnauthorized},
		{"пробел в конце", "Bearer " + token + " ", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, path := range []string{"/api/search?q=x", "/", "/metrics"} {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				if tt.header != "" {
					r.Header.Set("Authorization", tt.header)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != tt.want {
					t.Fatalf("%s: code = %d, want %d", path, w.Code, tt.want)
				}
				if tt.want == http.StatusUnauthorized {
					if !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer") {
						t.Fatalf("%s: нет WWW-Authenticate: %q", path, w.Header().Get("WWW-Authenticate"))
					}
					if strings.Contains(w.Body.String(), "catalog") {
						t.Fatalf("%s: 401 отдал содержимое", path)
					}
				}
			}
		})
	}
}

// Пустой токен никого не пускает: даже «Bearer » с пустым значением, которое
// совпало бы по хешу с пустой строкой.
func TestRequireBearer_EmptyTokenLetsNobodyIn(t *testing.T) {
	h := httpapi.RequireBearer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	for _, header := range []string{"", "Bearer ", "Bearer"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", header)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%q: code = %d", header, w.Code)
		}
	}
}

// Пробы здоровья (liveness/readiness Kubernetes) ходят без заголовка: за
// токеном под перезапускался бы по кругу. Данных они не отдают.
func TestRequireBearer_ProbesPassWithoutToken(t *testing.T) {
	h := httpapi.RequireBearer("k3Jx9_secret-token-0123456789abcdefXYZ", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusOK, "/healthz/x": http.StatusUnauthorized, "/healthzx": http.StatusUnauthorized} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Errorf("%s: code = %d, want %d", path, w.Code, want)
		}
	}
}

// Ответ 401 — на языке остальных ошибок API (английском), а не строкой окна.
func TestRequireBearer_UnauthorizedBodyIsPlainAPIText(t *testing.T) {
	h := httpapi.RequireBearer("k3Jx9_secret-token-0123456789abcdefXYZ", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/search", nil))
	if got := strings.TrimSpace(w.Body.String()); got != "bearer token required" {
		t.Fatalf("body = %q", got)
	}
}

// /readyz без токена отдает только код: текст ошибки готовности несет путь к
// каталогу. С токеном — полный текст, как прежде.
func TestRequireBearer_ReadyzWithoutTokenHidesDetails(t *testing.T) {
	const token = "k3Jx9_secret-token-0123456789abcdefXYZ"
	h := httpapi.RequireBearer(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready: open /srv/kb/catalog.json: no such file", http.StatusServiceUnavailable)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "/srv/kb") {
		t.Fatalf("без токена: %d %q", w.Code, w.Body.String())
	}
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "/srv/kb") {
		t.Fatalf("с токеном подробности пропали: %q", w.Body.String())
	}
}
