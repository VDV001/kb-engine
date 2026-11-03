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
// Пробы здоровья ходят без заголовка (liveness/readiness Kubernetes: за токеном
// под перезапускался бы по кругу): /healthz проходит как есть, /readyz отдает
// только код — текст ошибки готовности несет путь к каталогу.
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
		if token != "" && ok && strings.EqualFold(scheme, "Bearer") && subtle.ConstantTimeCompare(sum[:], want[:]) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			next.ServeHTTP(w, r)
		case "/readyz":
			rec := &codeOnly{header: http.Header{}}
			next.ServeHTTP(rec, r)
			if rec.code >= http.StatusBadRequest {
				http.Error(w, "not ready", rec.code)
				return
			}
			_, _ = w.Write([]byte("ready\n"))
		default:
			w.Header().Set("WWW-Authenticate", `Bearer realm="kbengine"`)
			http.Error(w, "bearer token required", http.StatusUnauthorized)
		}
	})
}

// codeOnly keeps the status of a response and drops its body.
type codeOnly struct {
	header http.Header
	code   int
}

func (c *codeOnly) Header() http.Header         { return c.header }
func (c *codeOnly) Write(b []byte) (int, error) { return len(b), nil }
func (c *codeOnly) WriteHeader(code int)        { c.code = code }
