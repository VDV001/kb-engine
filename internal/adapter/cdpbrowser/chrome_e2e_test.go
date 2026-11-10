package cdpbrowser

import (
	"bytes"
	"context"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Настоящий Chrome: гоняется только с KB_BROWSER_E2E=1 (в CI Chrome нет, а
// зеленый «пропущен» ничего не доказывает — поэтому условие названо вслух).
func TestChromeEndToEnd(t *testing.T) {
	if os.Getenv("KB_BROWSER_E2E") != "1" {
		t.Skip("нужен установленный Chrome: KB_BROWSER_E2E=1 [KB_BROWSER_PATH=/путь/к/браузеру] go test ./internal/adapter/cdpbrowser -run EndToEnd")
	}
	var mu sync.Mutex
	var typed string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><body style="margin:0;height:3000px">` +
			`<input id=u style="position:absolute;left:20px;top:20px;width:300px;height:30px">` +
			`<script>fetch('/ua?v='+encodeURIComponent(navigator.userAgent));u.oninput=()=>fetch('/seen?v='+encodeURIComponent(u.value));` +
			`onscroll=()=>fetch('/scrolled?y='+scrollY)</script>`))
	})
	mux.HandleFunc("/seen", func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		typed, _ = url.QueryUnescape(r.URL.Query().Get("v"))
		mu.Unlock()
	})
	var ua string
	mux.HandleFunc("/ua", func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ua = r.URL.Query().Get("v")
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
	// Не t.TempDir: помощники Chrome дописывают профиль еще миг после выхода, и
	// уборка папки роняла бы проверку, которая прошла.
	profile, err := os.MkdirTemp("", "kb-browser-e2e")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(profile) }()
	c, err := Launch(ctx, Options{Path: os.Getenv("KB_BROWSER_PATH"), Profile: profile, Width: 1280, Height: 800, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	// Кадр приходит сам, и после перехода он новый: картинка страницы, а не пустая.
	first, err := c.Next(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Input(ctx, Input{T: "nav", Text: srv.URL}); err != nil {
		t.Fatal(err)
	}
	f, err := c.Next(ctx, first.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.JPEG) < 3 || f.JPEG[0] != 0xff || f.JPEG[1] != 0xd8 {
		t.Fatalf("кадр не JPEG: %d байт", len(f.JPEG))
	}
	for _, in := range []Input{
		{T: "mouse", Type: "pressed", X: 100, Y: 35}, {T: "mouse", Type: "released", X: 100, Y: 35},
		{T: "text", Text: "Привет"},
	} {
		if err := c.Input(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	// Без окна Chrome называет себя HeadlessChrome, и часть сайтов видит в этом бота.
	waitFor(t, "страница назвала свой User-Agent", func() bool { mu.Lock(); defer mu.Unlock(); return ua != "" })
	mu.Lock()
	gotUA := ua
	mu.Unlock()
	if strings.Contains(gotUA, "Headless") {
		t.Fatalf("User-Agent выдает режим без окна: %q", gotUA)
	}
	waitFor(t, "текст дошел до поля", func() bool { mu.Lock(); defer mu.Unlock(); return typed == "Привет" })
	// Размер окна: кадр после resize такой, как просили (вкладка занимает все окно DVK).
	if err := c.Input(ctx, Input{T: "resize", X: 900, Y: 500}); err != nil {
		t.Fatal(err)
	}
	for seq := f.Seq; ; {
		fr, err := c.Next(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(fr.JPEG))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width == 900 && cfg.Height == 500 {
			break
		}
		if seq = fr.Seq; seq > f.Seq+30 {
			t.Fatalf("после resize кадры %dx%d, want 900x500", cfg.Width, cfg.Height)
		}
	}
	// Адрес страницы идет вместе с кадром: человек видит, где он на самом деле.
	gotURL := ""
	for seq := f.Seq; gotURL == "" || gotURL == "about:blank"; {
		fr, err := c.Next(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		seq, gotURL = fr.Seq, fr.URL
		if gotURL == srv.URL+"/" {
			break
		}
		if seq > f.Seq+20 {
			t.Fatalf("адрес кадра %q, want %q", gotURL, srv.URL+"/")
		}
	}
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

// Находки повторного ревью #526 на настоящем Chrome: окно, открытое страницей
// (target=_blank, OAuth), становится экраном; закрытое окно возвращает прежнее;
// скринкаст не идет, пока никто не смотрит.
func TestChromePopupsAndIdleScreencast(t *testing.T) {
	if os.Getenv("KB_BROWSER_E2E") != "1" {
		t.Skip("нужен установленный Chrome: KB_BROWSER_E2E=1")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/pop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><title>pop</title><body style="margin:0"><button id=b style="position:absolute;left:0;top:0;width:200px;height:60px" onclick="window.close()">close</button>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><title>main</title><body style="margin:0"><a id=a href="/pop" target="_blank" style="position:absolute;left:0;top:0;width:300px;height:80px;display:block">open</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	profile, err := os.MkdirTemp("", "kb-browser-e2e")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(profile) }()
	c, err := Launch(ctx, Options{Path: os.Getenv("KB_BROWSER_PATH"), Profile: profile, Width: 1280, Height: 800, Headless: true, ScreencastIdle: 400 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	first, err := c.Next(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Input(ctx, Input{T: "nav", Text: srv.URL + "/"}); err != nil {
		t.Fatal(err)
	}
	urlIs := func(want string) {
		t.Helper()
		seq := first.Seq
		for range 60 {
			f, err := c.Next(ctx, seq)
			if err != nil {
				t.Fatal(err)
			}
			if seq = f.Seq; f.URL == want {
				return
			}
		}
		t.Fatalf("адрес так и не стал %q", want)
	}
	urlIs(srv.URL + "/")
	for _, ev := range []string{"pressed", "released"} {
		if err := c.Input(ctx, Input{T: "mouse", Type: ev, X: 20, Y: 20, Count: 1}); err != nil {
			t.Fatal(err)
		}
	}
	urlIs(srv.URL + "/pop") // окно, открытое страницей, стало экраном
	for _, ev := range []string{"pressed", "released"} {
		if err := c.Input(ctx, Input{T: "mouse", Type: ev, X: 20, Y: 20, Count: 1}); err != nil {
			t.Fatal(err)
		}
	}
	urlIs(srv.URL + "/") // закрыли окно: вернулись на прежнее

	// Никто не спрашивает кадры: скринкаст останавливается.
	waitFor(t, "скринкаст остановлен без зрителя", func() bool { return !c.screencasting() })
	last := c.lastFrame().Seq
	got := make(chan Frame, 1)
	go func() {
		f, _ := c.Next(ctx, last)
		got <- f
	}()
	select {
	case f := <-got:
		if f.Seq <= last {
			t.Fatal("кадр после возобновления не новый")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("после нового запроса кадров скринкаст не возобновился")
	}
}

// Стартовая страница в настоящем браузере: он открывается на ней, адрес в строке
// dvk://start, а введенный на ней адрес открывается.
func TestChromeStartPageNavigates(t *testing.T) {
	if os.Getenv("KB_BROWSER_E2E") != "1" {
		t.Skip("нужен установленный Chrome: KB_BROWSER_E2E=1")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><title>hello</title><h1>hello</h1>`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	profile, err := os.MkdirTemp("", "kb-browser-e2e")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(profile) }()
	c, err := Launch(ctx, Options{Path: os.Getenv("KB_BROWSER_PATH"), Profile: profile, Width: 1280, Height: 800, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	urlIs := func(after uint64, want string) uint64 {
		t.Helper()
		seq := after
		for range 80 {
			f, err := c.Next(ctx, seq)
			if err != nil {
				t.Fatal(err)
			}
			if seq = f.Seq; f.URL == want {
				return seq
			}
		}
		t.Fatalf("адрес так и не стал %q", want)
		return 0
	}
	seq := urlIs(0, "dvk://start") // браузер открывается на своей странице, а не на белом экране
	// Фокус в строке уже стоит (страница сама его ставит): печатаем адрес и Enter.
	for _, in := range []Input{{T: "text", Text: srv.URL}, {T: "key", Key: "Enter"}} {
		if err := c.Input(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	urlIs(seq, srv.URL+"/")
}
