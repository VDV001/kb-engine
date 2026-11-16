// Package phonepage serves the phone chat page (#622, этап 4): статика под
// /m/, вшитая в бинарь. Данных в ней нет, поэтому она отдается без токена;
// токен телефона и ключ переписки приходят во фрагменте адреса QR и дальше
// живут только в браузере телефона.
package phonepage

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var static embed.FS

// csp: только свои файлы и свой kb-сервер; без inline и eval, без чужих фреймов.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"manifest-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Handler serves /m/ (и /m → /m/).
func Handler() http.Handler {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // каталог вшит при сборке
	}
	files := http.StripPrefix("/m/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/m" {
			http.Redirect(w, r, "/m/", http.StatusMovedPermanently)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "no-referrer") // адрес с ключом во фрагменте не уходит дальше
		h.Set("X-Content-Type-Options", "nosniff")
		// Модель телефона для окна («Samsung SM-S918B»): Chrome шлет ее этому
		// адресу дальше в запросах к ящику; Safari не шлет — там «iPhone».
		h.Set("Accept-CH", "Sec-CH-UA-Model")
		h.Set("Cache-Control", "no-cache") // новая версия страницы — с первого же открытия
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			h.Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}
