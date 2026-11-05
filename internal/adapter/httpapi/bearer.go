package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// TokenHeader carries the app token where Authorization cannot: Serverless
// Container в Yandex Cloud вырезает Authorization из запроса, а Bearer в нем
// проверяет как свой IAM-токен (403 до приложения).
const TokenHeader = "X-KB-Token"

// RequestToken returns the app token of r and whether a header named one:
// X-KB-Token, если он есть, иначе Bearer из Authorization. Есть X-KB-Token —
// решает он, даже пустой: два заголовка с разными токенами не дают выбрать
// удобный.
func RequestToken(r *http.Request) (string, bool) {
	if v := r.Header.Values(TokenHeader); len(v) > 0 {
		return v[0], v[0] != ""
	}
	scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return tok, ok && strings.EqualFold(scheme, "Bearer")
}

// RequireBearer answers 401 to every request without the token in X-KB-Token
// or "Authorization: Bearer <token>" — страницы, API и /metrics одинаково: сервер, открытый в сеть,
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
		got, ok := RequestToken(r)
		sum := sha256.Sum256([]byte(got))
		// Пустой токен не пускает никого: иначе пустое значение заголовка
		// совпало бы с ним по хешу. С непустым пустое значение не совпадет.
		if token != "" && ok && subtle.ConstantTimeCompare(sum[:], want[:]) == 1 {
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
