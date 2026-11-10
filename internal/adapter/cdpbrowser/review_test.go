package cdpbrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Находки повторного ревью #526 (вторая часть). Каждый тест ниже красный до
// следующего коммита.

// blockingScreen never answers Input: зависший renderer или диалог beforeunload.
type blockingScreen struct{ fakeScreen }

func (*blockingScreen) Input(ctx context.Context, _ Input) error {
	<-ctx.Done()
	return ctx.Err()
}

// Зависший вызов CDP не должен держать /input вечно: за ним стоит вся очередь ввода окна.
func TestInputHasDeadline(t *testing.T) {
	h := Handler(&blockingScreen{}, Config{Token: testToken, Width: 1280, Height: 800, InputWait: 50 * time.Millisecond})
	done := make(chan int, 1)
	go func() { done <- do(h, http.MethodPost, "/input", `{"t":"text","text":"a"}`, testToken).Code }()
	select {
	case code := <-done:
		if code != http.StatusBadGateway {
			t.Fatalf("code = %d, want 502", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("/input завис на зависшем браузере")
	}
}

// Двойной щелчок, Shift+Tab, Cmd+A: число щелчков и модификаторы доходят до браузера.
func TestInputClickCountAndModifiers(t *testing.T) {
	tests := []struct {
		name, body string
		want       int
	}{
		{"двойной щелчок", `{"t":"mouse","type":"pressed","x":10,"y":10,"count":2}`, http.StatusNoContent},
		{"тройной щелчок", `{"t":"mouse","type":"pressed","x":10,"y":10,"count":3}`, http.StatusNoContent},
		{"четвертый щелчок", `{"t":"mouse","type":"pressed","x":10,"y":10,"count":4}`, http.StatusBadRequest},
		{"отрицательное число щелчков", `{"t":"mouse","type":"pressed","x":10,"y":10,"count":-1}`, http.StatusBadRequest},
		{"Shift+Tab", `{"t":"key","key":"Tab","mods":8}`, http.StatusNoContent},
		{"Shift+стрелка", `{"t":"key","key":"ArrowRight","mods":8}`, http.StatusNoContent},
		{"выделить все", `{"t":"key","key":"SelectAll"}`, http.StatusNoContent},
		{"модификаторы вне маски", `{"t":"key","key":"Tab","mods":16}`, http.StatusBadRequest},
		{"отрицательные модификаторы", `{"t":"key","key":"Tab","mods":-1}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := do(newTestHandler(&fakeScreen{}), http.MethodPost, "/input", tc.body, testToken).Code; got != tc.want {
				t.Fatalf("code = %d, want %d", got, tc.want)
			}
		})
	}
}

type nopWC struct{}

func (nopWC) Write(b []byte) (int, error) { return len(b), nil }
func (nopWC) Close() error                { return nil }

func event(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pushState и смена хэша в одностраничном приложении не вызывают frameNavigated,
// и адрес в строке отставал бы от настоящего.
func TestAddressFollowsInDocumentNavigation(t *testing.T) {
	c := newChrome(nil, nopWC{})
	c.setSession("S1")
	c.onEvent("S1", "Page.frameNavigated", event(t, map[string]any{"frame": map[string]any{"id": "main", "url": "https://app.example/"}}))
	c.onEvent("S1", "Page.navigatedWithinDocument", event(t, map[string]any{"frameId": "main", "url": "https://app.example/inbox#1"}))
	if got := c.currentURL(); got != "https://app.example/inbox#1" {
		t.Fatalf("адрес %q, want после pushState", got)
	}
	// Вложенный фрейм адрес страницы не меняет.
	c.onEvent("S1", "Page.navigatedWithinDocument", event(t, map[string]any{"frameId": "ad", "url": "https://ads.example/x"}))
	if got := c.currentURL(); got != "https://app.example/inbox#1" {
		t.Fatalf("адрес %q: вложенный фрейм подменил адрес страницы", got)
	}
}

// Кадр от закрытой или фоновой страницы не должен перекрыть текущую.
func TestFramesOnlyFromCurrentSession(t *testing.T) {
	c := newChrome(nil, nopWC{})
	c.setSession("S1")
	frame := event(t, map[string]any{"data": "/9j/", "sessionId": 1})
	c.onEvent("S2", "Page.screencastFrame", frame)
	if f := c.lastFrame(); f.Seq != 0 {
		t.Fatalf("кадр чужой сессии принят: seq %d", f.Seq)
	}
	c.onEvent("S1", "Page.screencastFrame", frame)
	if f := c.lastFrame(); f.Seq != 1 {
		t.Fatalf("кадр текущей сессии не принят: seq %d", f.Seq)
	}
}

// Страница считает размер отправленным только после ответа; сбой или перезапуск
// сервера сбрасывают отметку, иначе resize не повторяется и клики попадают мимо.
func TestViewerResendsResizeAfterFailureAndRestart(t *testing.T) {
	page := Viewer(1280, 800)
	for _, want := range []string{
		`.then((ok) => { if (!ok) { sent = ""`, // сбой resize — отметка сброшена
		`sent = ""; fit();`,                    // новая эпоха: Chrome снова 1280x800
		`AbortSignal.timeout(`,                 // запрос ввода не висит вечно
		`return false`,                         // run сообщает об отказе
		`count:`,                               // двойной и тройной щелчок
		`mods:`,                                // модификаторы клавиш
		`"SelectAll"`,                          // Cmd+A и Ctrl+A
	} {
		if !strings.Contains(page, want) {
			t.Errorf("в странице нет %q", want)
		}
	}
}
