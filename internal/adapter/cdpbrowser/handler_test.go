package cdpbrowser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "test-token-only-for-handler-tests-xx"

type fakeScreen struct {
	mu     sync.Mutex
	frames []Frame
	inputs []Input
	err    error
}

func (f *fakeScreen) Next(ctx context.Context, after uint64) (Frame, error) {
	for {
		f.mu.Lock()
		for _, fr := range f.frames {
			if fr.Seq > after {
				f.mu.Unlock()
				return fr, nil
			}
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (f *fakeScreen) Input(_ context.Context, in Input) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	return f.err
}

func newTestHandler(s Screen) http.Handler {
	return Handler(s, Config{Token: testToken, Width: 1280, Height: 800, Wait: 60 * time.Millisecond})
}

func do(h http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		r.Header.Set("X-KB-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestToken(t *testing.T) {
	h := newTestHandler(&fakeScreen{})
	tests := []struct {
		name, token string
		want        int
	}{
		{"нет токена", "", http.StatusUnauthorized},
		{"чужой токен", "zzz", http.StatusUnauthorized},
		{"тот же префикс", testToken[:31], http.StatusUnauthorized},
		{"верный", testToken, http.StatusOK},
	}
	for _, p := range []string{"/", "/frame?after=0", "/input"} {
		for _, tc := range tests {
			t.Run(p+" "+tc.name, func(t *testing.T) {
				method := http.MethodGet
				if p == "/input" {
					method = http.MethodPost
				}
				got := do(h, method, p, `{"t":"text","text":"a"}`, tc.token).Code
				if tc.want == http.StatusOK {
					if got == http.StatusUnauthorized {
						t.Fatalf("верный токен отвергнут: %d", got)
					}
					return
				}
				if got != tc.want {
					t.Fatalf("code = %d, want %d", got, tc.want)
				}
			})
		}
	}
}

func TestTokenEmptyConfigRefuses(t *testing.T) {
	// Пустой токен в конфиге не «без защиты»: обработчик отказывает всем.
	h := Handler(&fakeScreen{}, Config{Width: 1280, Height: 800})
	if got := do(h, http.MethodGet, "/", "", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("без токена в конфиге: %d, want 401", got)
	}
}

func TestViewerPage(t *testing.T) {
	w := do(newTestHandler(&fakeScreen{}), http.MethodGet, "/", "", testToken)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("страница: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	// Относительные адреса: окно открывает страницу под /browser/, и абсолютный
	// /frame ушел бы мимо прокси.
	if strings.Contains(body, `"/frame`) || strings.Contains(body, `'/frame`) || strings.Contains(body, `"/input`) {
		t.Fatal("страница ходит по абсолютным адресам")
	}
	if !strings.Contains(body, "1280") || !strings.Contains(body, "800") {
		t.Fatal("размер экрана не подставлен в страницу")
	}
}

func TestFrame(t *testing.T) {
	s := &fakeScreen{frames: []Frame{{Seq: 3, JPEG: []byte("\xff\xd8jpeg")}}}
	h := newTestHandler(s)

	w := do(h, http.MethodGet, "/frame?after=0", "", testToken)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("X-Frame-Seq") != "3" || w.Body.String() != "\xff\xd8jpeg" {
		t.Fatalf("кадр: %d %q seq=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Header().Get("X-Frame-Seq"), w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("кадр кэшируется: в кэше осел бы кусок страницы")
	}
	// Ничего нового за срок — 204, окно спрашивает снова.
	if got := do(h, http.MethodGet, "/frame?after=3", "", testToken).Code; got != http.StatusNoContent {
		t.Fatalf("нет нового кадра: %d, want 204", got)
	}
	for _, bad := range []string{"/frame", "/frame?after=-1", "/frame?after=x"} {
		if got := do(h, http.MethodGet, bad, "", testToken).Code; got != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", bad, got)
		}
	}
}

func TestInputValidation(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
	}{
		{"мышь", `{"t":"mouse","type":"pressed","x":10,"y":20}`, http.StatusNoContent},
		{"мышь на краю", `{"t":"mouse","type":"moved","x":1280,"y":800}`, http.StatusNoContent},
		{"мышь за краем справа", `{"t":"mouse","type":"moved","x":1281,"y":5}`, http.StatusBadRequest},
		{"мышь левее нуля", `{"t":"mouse","type":"moved","x":-1,"y":5}`, http.StatusBadRequest},
		{"мышь, чужой тип", `{"t":"mouse","type":"drag","x":5,"y":5}`, http.StatusBadRequest},
		{"колесо", `{"t":"wheel","x":5,"y":5,"dx":0,"dy":120}`, http.StatusNoContent},
		{"колесо слишком сильное", `{"t":"wheel","x":5,"y":5,"dy":100000}`, http.StatusBadRequest},
		{"текст", `{"t":"text","text":"Привет мир"}`, http.StatusNoContent},
		{"пустой текст", `{"t":"text","text":""}`, http.StatusBadRequest},
		{"клавиша из списка", `{"t":"key","key":"Enter"}`, http.StatusNoContent},
		{"клавиша не из списка", `{"t":"key","key":"F13"}`, http.StatusBadRequest},
		{"адрес https", `{"t":"nav","text":"https://example.com/a?b=1"}`, http.StatusNoContent},
		{"адрес http", `{"t":"nav","text":"http://example.com"}`, http.StatusNoContent},
		{"назад", `{"t":"nav","text":"back"}`, http.StatusNoContent},
		{"javascript:", `{"t":"nav","text":"javascript:alert(1)"}`, http.StatusBadRequest},
		{"file:", `{"t":"nav","text":"file:///etc/passwd"}`, http.StatusBadRequest},
		{"chrome:", `{"t":"nav","text":"chrome://settings"}`, http.StatusBadRequest},
		{"data:", `{"t":"nav","text":"data:text/html,hi"}`, http.StatusBadRequest},
		{"адрес без хоста", `{"t":"nav","text":"https://"}`, http.StatusBadRequest},
		{"неизвестный вид", `{"t":"eval","text":"1"}`, http.StatusBadRequest},
		{"не JSON", `not json`, http.StatusBadRequest},
		{"лишнее поле", `{"t":"text","text":"a","cdp":"Browser.close"}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeScreen{}
			w := do(newTestHandler(s), http.MethodPost, "/input", tc.body, testToken)
			if w.Code != tc.want {
				t.Fatalf("code = %d (%s), want %d", w.Code, strings.TrimSpace(w.Body.String()), tc.want)
			}
			if got, want := len(s.inputs), btoi(tc.want == http.StatusNoContent); got != want {
				t.Fatalf("до браузера дошло %d событий, want %d", got, want)
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestInputTooLarge(t *testing.T) {
	s := &fakeScreen{}
	big := `{"t":"text","text":"` + strings.Repeat("a", 70_000) + `"}`
	if got := do(newTestHandler(s), http.MethodPost, "/input", big, testToken).Code; got != http.StatusBadRequest && got != http.StatusRequestEntityTooLarge {
		t.Fatalf("огромное тело: %d", got)
	}
	if len(s.inputs) != 0 {
		t.Fatal("огромный текст дошел до браузера")
	}
}

func TestInputMethodAndBrowserFailure(t *testing.T) {
	h := newTestHandler(&fakeScreen{})
	if got := do(h, http.MethodGet, "/input", "", testToken).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("GET /input: %d, want 405", got)
	}
	s := &fakeScreen{err: context.DeadlineExceeded}
	if got := do(newTestHandler(s), http.MethodPost, "/input", `{"t":"text","text":"a"}`, testToken).Code; got != http.StatusBadGateway {
		t.Fatalf("браузер не ответил: %d, want 502", got)
	}
}
