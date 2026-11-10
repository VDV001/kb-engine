package cdpbrowser

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed viewer.html
var viewerHTML string

const (
	maxBody    = 64 << 10 // событие — несколько десятков байт; больше — не событие
	maxText    = 4096     // вставка текста за раз, в символах
	maxAddress = 2048
	maxWheel   = 5000 // пикселей за одно событие колеса
)

// keys is every key the window may press by name; буквы и знаки идут текстом.
// Список закрытый: незнакомое имя — отказ, а не передача браузеру как есть.
var keys = map[string]bool{
	"Enter": true, "Backspace": true, "Tab": true, "Escape": true, "Delete": true,
	"ArrowLeft": true, "ArrowRight": true, "ArrowUp": true, "ArrowDown": true,
	"Home": true, "End": true, "PageUp": true, "PageDown": true,
}

// Handler serves the viewer page, the frame long-poll and the input endpoint.
// Все три — только с токеном в X-KB-Token: через CDP браузер отдается целиком
// (входы в сайты, cookie), а порт на loopback достанется любому процессу Мака.
func Handler(s Screen, cfg Config) http.Handler {
	page := strings.NewReplacer("{{W}}", strconv.Itoa(cfg.Width), "{{H}}", strconv.Itoa(cfg.Height)).Replace(viewerHTML)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("GET /frame", func(w http.ResponseWriter, r *http.Request) { serveFrame(w, r, s, cfg) })
	mux.HandleFunc("POST /input", func(w http.ResponseWriter, r *http.Request) { serveInput(w, r, s, cfg) })
	return authorized(cfg.Token, mux)
}

// authorized refuses every request without the token. Пустой токен в
// конфиге — отказ всем, а не «без защиты».
func authorized(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-KB-Token")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "нужен токен браузера", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func serveFrame(w http.ResponseWriter, r *http.Request, s Screen, cfg Config) {
	after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	if err != nil {
		http.Error(w, "after — номер последнего кадра, 0 для первого", http.StatusBadRequest)
		return
	}
	ctx, cancel := withWait(r.Context(), cfg.Wait)
	defer cancel()
	f, err := s.Next(ctx, after)
	if err != nil {
		if r.Context().Err() != nil {
			return // окно закрыло запрос
		}
		if ctx.Err() != nil {
			w.WriteHeader(http.StatusNoContent) // нового кадра нет: окно спросит снова
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Seq", strconv.FormatUint(f.Seq, 10))
	_, _ = w.Write(f.JPEG)
}

func serveInput(w http.ResponseWriter, r *http.Request, s Screen, cfg Config) {
	var in Input
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields() // лишнее поле — не событие окна, а попытка проверить, что пропустят
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "не событие: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := validate(in, cfg); err != "" {
		http.Error(w, err, http.StatusBadRequest)
		return
	}
	if err := s.Input(r.Context(), in); err != nil {
		http.Error(w, "браузер не ответил: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validate returns why the event is refused; пусто — можно передавать браузеру.
func validate(in Input, cfg Config) string {
	inside := func() bool {
		return in.X >= 0 && in.X <= float64(cfg.Width) && in.Y >= 0 && in.Y <= float64(cfg.Height)
	}
	switch in.T {
	case "mouse":
		if in.Type != "pressed" && in.Type != "released" && in.Type != "moved" {
			return "мышь: pressed, released или moved"
		}
		if !inside() {
			return "мышь вне экрана"
		}
	case "wheel":
		if !inside() || abs(in.DX) > maxWheel || abs(in.DY) > maxWheel {
			return "колесо вне экрана или слишком сильное"
		}
	case "text":
		if in.Text == "" || utf8.RuneCountInString(in.Text) > maxText || !utf8.ValidString(in.Text) {
			return "текст пуст или длиннее " + strconv.Itoa(maxText)
		}
	case "key":
		if !keys[in.Key] {
			return "клавиша не из списка"
		}
	case "nav":
		return navRefusal(in.Text)
	default:
		return "неизвестный вид события"
	}
	return ""
}

// navRefusal checks an address or one of back, forward, reload. Только http и
// https: javascript:, file:, chrome: и data: из окна не открываются никогда.
func navRefusal(addr string) string {
	switch addr {
	case "back", "forward", "reload":
		return ""
	}
	if len(addr) > maxAddress {
		return "адрес слишком длинный"
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "адрес — только http:// или https://"
	}
	return ""
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
