package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireBearer answers 401 to every request without "Authorization: Bearer
// <token>" — страницы, API и /metrics одинаково: сервер, открытый в сеть,
// отдает базу только тому, у кого токен.
//
// Сравниваются хеши, а не строки: subtle.ConstantTimeCompare на строках разной
// длины возвращается сразу, и время ответа выдало бы длину токена.
func RequireBearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		sum := sha256.Sum256([]byte(got))
		// Пустой токен не пускает никого: иначе «Bearer » с пустым значением
		// совпал бы с ним по хешу. С непустым пустое значение не совпадет.
		if token == "" || !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kbengine"`)
			http.Error(w, "нужен токен: Authorization: Bearer <token>", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
