package cdpbrowser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

// Настоящий Chrome: гоняется только с KB_BROWSER_E2E=1 (в CI Chrome нет, а
// зеленый «пропущен» ничего не доказывает — поэтому условие названо вслух).
func TestChromeEndToEnd(t *testing.T) {
	if os.Getenv("KB_BROWSER_E2E") != "1" {
		t.Skip("нужен установленный Chrome: KB_BROWSER_E2E=1 go test ./internal/adapter/cdpbrowser -run EndToEnd")
	}
	var mu sync.Mutex
	var typed string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><body style="margin:0;height:3000px">` +
			`<input id=u style="position:absolute;left:20px;top:20px;width:300px;height:30px">` +
			`<script>u.oninput=()=>fetch('/seen?v='+encodeURIComponent(u.value));` +
			`onscroll=()=>fetch('/scrolled?y='+scrollY)</script>`))
	})
	mux.HandleFunc("/seen", func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		typed, _ = url.QueryUnescape(r.URL.Query().Get("v"))
		mu.Unlock()
	})
	scrolled := make(chan string, 4)
	mux.HandleFunc("/scrolled", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case scrolled <- r.URL.Query().Get("y"):
		default:
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	c, err := Launch(ctx, Options{Profile: t.TempDir(), Width: 1280, Height: 800, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Input(ctx, Input{T: "nav", Text: srv.URL}); err != nil {
		t.Fatal(err)
	}
	// Кадр со страницей приходит сам: после перехода картинка меняется.
	var seq uint64
	for range 3 {
		f, err := c.Next(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.JPEG) < 3 || f.JPEG[0] != 0xff || f.JPEG[1] != 0xd8 {
			t.Fatalf("кадр не JPEG: %d байт", len(f.JPEG))
		}
		seq = f.Seq
	}
	for _, in := range []Input{
		{T: "mouse", Type: "pressed", X: 100, Y: 35}, {T: "mouse", Type: "released", X: 100, Y: 35},
		{T: "text", Text: "Привет"},
	} {
		if err := c.Input(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "текст дошел до поля", func() bool { mu.Lock(); defer mu.Unlock(); return typed == "Привет" })
	if err := c.Input(ctx, Input{T: "key", Key: "Backspace"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Backspace стер букву", func() bool { mu.Lock(); defer mu.Unlock(); return typed == "Приве" })
	if err := c.Input(ctx, Input{T: "wheel", X: 600, Y: 400, DY: 500}); err != nil {
		t.Fatal(err)
	}
	select {
	case y := <-scrolled:
		if y == "0" {
			t.Fatal("колесо не прокрутило страницу")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("страница не прокрутилась")
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("не дождались: " + what)
}
