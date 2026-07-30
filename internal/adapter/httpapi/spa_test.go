package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// Кэширование задаётся явно, а не оставляется на усмотрение браузера. Без
// заголовков он гадает, и гадает в самую неудачную сторону: держит старый
// index.html, а тот ссылается на бандл по хешу содержимого — пересобранный
// движок продолжает отдавать прежнюю страницу, и починка выглядит непринятой.
// Ровно это и случилось: правки шапки и списка не доезжали до владельца.
func TestSPA_cacheHeaders(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: []byte("<html>")},
		"assets/index-abc123.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	srv := httptest.NewServer(spaHandler(fsys))
	defer srv.Close()

	tests := []struct {
		name, path, want string
	}{
		// Хеш в имени: изменился файл — изменился URL, значит старый можно
		// хранить сколько угодно.
		{"hashed asset", "/assets/index-abc123.js", "public, max-age=31536000, immutable"},
		// index.html хеша не несёт и он единственное место, где записаны те URL.
		{"index", "/", "no-cache"},
		// Маршрут SPA отдаёт тот же index.html и обязан отдать тот же заголовок.
		{"spa route", "/archives", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if got := resp.Header.Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}
