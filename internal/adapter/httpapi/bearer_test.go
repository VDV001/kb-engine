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
