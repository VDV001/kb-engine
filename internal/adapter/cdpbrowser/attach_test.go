package cdpbrowser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCDP is the browser side of the pipe: читает команды (JSON, конец — NUL) и
// отвечает как браузер, у которого при запуске уже есть страницы.
type fakeCDP struct {
	mu    sync.Mutex
	calls []string // «метод» или «метод:целевая страница»
	pages []string // страницы, которые браузер показывает в getTargets

	failCreate bool     // браузер отказывает в создании страницы
	castFrame  bool     // после Page.startScreencast браузер шлет кадр (Chrome); Brave без окна молчит
	shots      int      // сколько раз просили скриншот
	navs       []string // адреса Page.navigate
	castFails  int      // сколько раз Page.startScreencast отвечает «Not attached to an active page»
	navDrops   int      // сколько первых Page.navigate браузер теряет молча (Brave сразу после запуска)
	pageURL    string   // адрес, который браузер считает адресом страницы
}

func startFakeCDP(t *testing.T, pages ...string) (*Chrome, *fakeCDP) {
	t.Helper()
	cmdR, cmdW := io.Pipe()
	replyR, replyW := io.Pipe()
	f := &fakeCDP{pages: pages}
	c := newChrome(nil, cmdW)
	go c.readLoop(replyR)
	go func() {
		in := bufio.NewReader(cmdR)
		for {
			raw, err := in.ReadBytes(0)
			if err != nil {
				return
			}
			var m struct {
				ID        int64           `json:"id"`
				Method    string          `json:"method"`
				SessionID string          `json:"sessionId"`
				Params    json.RawMessage `json:"params"`
			}
			if json.Unmarshal(raw[:len(raw)-1], &m) != nil {
				continue
			}
			var p struct {
				TargetID string `json:"targetId"`
				URL      string `json:"url"`
			}
			_ = json.Unmarshal(m.Params, &p)
			f.mu.Lock()
			call := m.Method
			if p.TargetID != "" {
				call += ":" + p.TargetID
			}
			f.calls = append(f.calls, call)
			if m.Method == "Page.navigate" {
				f.navs = append(f.navs, p.URL)
				if f.navDrops > 0 {
					f.navDrops--
				} else {
					f.pageURL = p.URL
				}
			}
			var res string
			switch m.Method {
			case "Target.getTargets":
				infos := make([]string, 0, len(f.pages))
				for _, id := range f.pages {
					infos = append(infos, fmt.Sprintf(`{"targetId":%q,"type":"page"}`, id))
				}
				res = `{"targetInfos":[` + strings.Join(infos, ",") + `]}`
			case "Target.createTarget":
				res = `{"targetId":"ours"}`
			case "Target.attachToTarget":
				res = fmt.Sprintf(`{"sessionId":"S-%s"}`, p.TargetID)
			case "Page.captureScreenshot":
				f.shots++
				res = `{"data":"SlBHMQ=="}` // base64("JPG1")
			case "Page.getFrameTree":
				u := f.pageURL
				if u == "" {
					u = "about:blank"
				}
				res = fmt.Sprintf(`{"frameTree":{"frame":{"id":"F","url":%q}}}`, u)
			default:
				res = `{}`
			}
			failed := f.failCreate && m.Method == "Target.createTarget"
			notAttached := m.Method == "Page.startScreencast" && f.castFails > 0
			if notAttached {
				f.castFails--
			}
			f.mu.Unlock()
			reply := map[string]any{"id": m.ID, "sessionId": m.SessionID, "result": json.RawMessage(res)}
			if failed {
				reply = map[string]any{"id": m.ID, "sessionId": m.SessionID, "error": map[string]any{"code": -32000, "message": "нет окон"}}
			}
			if notAttached {
				reply = map[string]any{"id": m.ID, "sessionId": m.SessionID, "error": map[string]any{"code": -32000, "message": "Not attached to an active page"}}
			}
			out, _ := json.Marshal(reply)
			if _, err := replyW.Write(append(out, 0)); err != nil {
				return
			}
			f.mu.Lock()
			emit := f.castFrame && m.Method == "Page.startScreencast"
			f.mu.Unlock()
			if emit {
				ev, _ := json.Marshal(map[string]any{"method": "Page.screencastFrame", "sessionId": m.SessionID, "params": map[string]any{"data": "Q0FTVA==", "sessionId": 1}}) // base64("CAST")
				if _, err := replyW.Write(append(ev, 0)); err != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() { _ = cmdW.Close(); _ = replyW.Close() })
	return c, f
}

func (f *fakeCDP) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Brave при запуске держит две пустые страницы: одна из них — та, что он показывает,
// другая скрыта и кадров не рисует. Раньше сервер брал первую попавшуюся и молча
// получал белый экран. Теперь он делает свою страницу и закрывает чужие.
func TestAttachUsesOwnPageAndClosesOthers(t *testing.T) {
	c, f := startFakeCDP(t, "hidden-1", "shown-2")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err != nil {
		t.Fatal(err)
	}
	if got := c.session(); got != "S-ours" {
		t.Fatalf("сессия = %q, want S-ours: сервер должен работать со своей страницей", got)
	}
	calls := f.seen()
	for _, want := range []string{"Target.createTarget", "Target.attachToTarget:ours", "Target.closeTarget:hidden-1", "Target.closeTarget:shown-2"} {
		if !slices.Contains(calls, want) {
			t.Errorf("не было вызова %q; вызовы: %v", want, calls)
		}
	}
	for _, bad := range []string{"Target.attachToTarget:hidden-1", "Target.attachToTarget:shown-2"} {
		if slices.Contains(calls, bad) {
			t.Errorf("сервер подключился к чужой странице: %q", bad)
		}
	}
	c.mu.Lock()
	pages := slices.Clone(c.pages)
	c.mu.Unlock()
	if len(pages) != 1 || pages[0].target != "ours" {
		t.Errorf("открытые страницы = %+v, want только ours", pages)
	}
}

// Чужие страницы закрываются только когда своя уже есть: если создать ее не удалось,
// браузер остался бы совсем без окон.
func TestAttachClosesNothingWhenOwnPageFails(t *testing.T) {
	c, f := startFakeCDP(t, "hidden-1", "shown-2")
	f.failCreate = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err == nil {
		t.Fatal("attach без своей страницы должен вернуть ошибку")
	}
	for _, call := range f.seen() {
		if call == "Target.closeTarget:hidden-1" || call == "Target.closeTarget:shown-2" {
			t.Fatalf("закрыта чужая страница %q, хотя своей нет", call)
		}
	}
}

func (f *fakeCDP) shotCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shots
}

// Brave без окна не шлет кадры скринкаста, но скриншоты отдает: если кадров нет,
// а зритель ждет, сервер снимает скриншоты сам. Иначе во вкладке белый экран.
func TestNextFallsBackToScreenshotsWhenCastIsSilent(t *testing.T) {
	c, f := startFakeCDP(t)
	c.noFrameWait = 80 * time.Millisecond
	c.mu.Lock()
	c.sess = "S1"
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fr, err := c.Next(ctx, 0)
	if err != nil {
		t.Fatalf("Next = %v: кадра нет, а скриншот можно снять", err)
	}
	if string(fr.JPEG) != "JPG1" || fr.Seq != 1 {
		t.Fatalf("кадр = %q seq %d, want JPG1 seq 1", fr.JPEG, fr.Seq)
	}
	if f.shotCount() == 0 {
		t.Fatal("скриншот не снимали")
	}
}

// Отрицательный контроль: у Chrome кадры идут сами, и скриншоты не снимаются
// (иначе вкладка нагружала бы процессор лишним).
func TestNextDoesNotTakeScreenshotsWhenCastWorks(t *testing.T) {
	c, f := startFakeCDP(t)
	f.castFrame = true
	c.noFrameWait = 80 * time.Millisecond
	c.mu.Lock()
	c.sess = "S1"
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fr, err := c.Next(ctx, 0)
	if err != nil || string(fr.JPEG) != "CAST" {
		t.Fatalf("Next = %q, %v; want кадр скринкаста CAST", fr.JPEG, err)
	}
	// Неподвижная страница: следующего кадра нет, ждем дольше noFrameWait, чтобы запасной путь мог включиться.
	wait, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	_, _ = c.Next(wait, fr.Seq)
	if n := f.shotCount(); n != 0 {
		t.Fatalf("снято скриншотов: %d, want 0: скринкаст работает", n)
	}
}

// Догоняющий тест (e2e на Brave был красным, правку нашли по нему): страница без окна
// не считает себя активной, и ввод в поле не доходит, пока фокус не эмулируется.
func TestAttachEnablesFocusEmulation(t *testing.T) {
	c, f := startFakeCDP(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.seen(), "Emulation.setFocusEmulationEnabled") {
		t.Fatalf("фокус страницы не включен; вызовы: %v", f.seen())
	}
}

func (f *fakeCDP) navigations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.navs)
}

// После перехода на новую страницу браузер на миг отвечает «Not attached to an active
// page»: картинку включаем заново, а не падаем (вкладка иначе остается без кадров).
func TestWatchRetriesWhileNotAttached(t *testing.T) {
	c, f := startFakeCDP(t)
	f.castFails = 3
	c.mu.Lock()
	c.sess = "S1"
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.watch(ctx); err != nil {
		t.Fatalf("watch = %v: временный отказ должен повторяться", err)
	}
	n := 0
	for _, call := range f.seen() {
		if call == "Page.startScreencast" {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("startScreencast вызван %d раз, want 4 (три отказа и успех)", n)
	}
}

// Настоящая ошибка не повторяется бесконечно.
func TestWatchGivesUpOnPersistentFailure(t *testing.T) {
	c, f := startFakeCDP(t)
	f.castFails = 1000
	c.mu.Lock()
	c.sess = "S1"
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.watch(ctx); err == nil {
		t.Fatal("watch должен вернуть ошибку, когда страница так и не подключилась")
	}
}

// Brave сразу после запуска молча теряет первые переходы: страница остается пустой.
// Сервер проверяет адрес и повторяет переход, пока стартовая страница не откроется.
func TestAttachRetriesStartPageWhenNavigationIsLost(t *testing.T) {
	c, f := startFakeCDP(t, "x")
	f.navDrops = 2
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err != nil {
		t.Fatal(err)
	}
	if got := c.currentURL(); got != StartURL() {
		t.Fatalf("адрес страницы = %.40q…, want стартовая страница", got)
	}
	if n := len(f.navigations()); n != 3 {
		t.Fatalf("переходов %d, want 3 (два потеряны и один принят)", n)
	}
}

// Не открылась совсем: это не ошибка запуска (пустая страница рабочая), и повторов не бесконечно много.
func TestAttachGivesUpStartPageQuietly(t *testing.T) {
	c, f := startFakeCDP(t, "x")
	f.navDrops = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err != nil {
		t.Fatalf("attach = %v: пустая страница вместо стартовой — не ошибка", err)
	}
	if n := len(f.navigations()); n < 2 || n > 12 {
		t.Fatalf("переходов %d, want от 2 до 12", n)
	}
}
