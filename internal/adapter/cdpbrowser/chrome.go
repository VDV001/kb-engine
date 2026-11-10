package cdpbrowser

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options of a launched Chrome.
type Options struct {
	Path     string // пусто — найти установленный браузер на движке Chromium
	Profile  string // отдельная папка профиля: основной Chrome порт отладки не открывает
	Width    int
	Height   int
	Headless bool // для проверок без окна на экране
}

// browsers are the Chromium-based browsers the module can drive, in the order
// they are looked for: любой на движке Chromium говорит на одном протоколе.
var (
	macBrowsers = []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
	}
	pathBrowsers = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave-browser", "microsoft-edge"}
)

// ErrNoBrowser means no Chromium-based browser was found.
var ErrNoBrowser = errors.New("не нашла браузер на движке Chromium (Chrome, Brave, Edge, Chromium): поставьте любой или укажите путь флагом --chrome")

// FindBrowser returns the first installed Chromium-based browser.
func FindBrowser() (string, error) {
	for _, p := range macBrowsers {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	for _, name := range pathBrowsers {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", ErrNoBrowser
}

// Chrome is a running browser; it is a Screen.
//
// CDP идет по каналу (--remote-debugging-pipe, дескрипторы 3 и 4), а не по
// порту: порт отладки открыт любому локальному процессу без токена, а через
// него — cookie и вкладки (находка security-ревью #526, F2).
type Chrome struct {
	cmd  *exec.Cmd
	out  io.WriteCloser // команды браузеру
	wmu  sync.Mutex     // запись в канал — по одному сообщению
	sess string         // сессия страницы в CDP

	nextID atomic.Int64
	mu     sync.Mutex
	wait   map[int64]chan cdpReply
	frame  Frame
	change chan struct{} // закрывается при каждом новом кадре
	url    string
	down   bool // кнопка мыши зажата: движение тогда перетаскивание

	dead    chan struct{}
	deadErr error
	once    sync.Once
}

type cdpReply struct {
	result json.RawMessage
	err    error
}

// Done is closed when the browser is gone: закрыли окно или он упал.
func (c *Chrome) Done() <-chan struct{} { return c.dead }

// Launch starts the browser and attaches to its first page.
func Launch(ctx context.Context, o Options) (*Chrome, error) {
	path := o.Path
	if path == "" {
		var err error
		if path, err = FindBrowser(); err != nil {
			return nil, err
		}
	}
	if o.Profile == "" {
		return nil, errors.New("нужна папка профиля браузера")
	}
	if err := os.MkdirAll(o.Profile, 0o700); err != nil {
		return nil, err
	}
	args := []string{
		"--remote-debugging-pipe", "--user-data-dir=" + o.Profile, "--no-first-run", "--no-default-browser-check",
		"--window-size=" + strconv.Itoa(o.Width) + "," + strconv.Itoa(o.Height),
		// Закрытое другим окном или свернутое окно Chrome иначе перестает рисовать, и картинка замирает.
		"--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding", "--disable-background-timer-throttling",
	}
	if o.Headless {
		args = append(args, "--headless=new")
	}
	childIn, parentOut, err := os.Pipe() // браузер читает fd 3, мы пишем
	if err != nil {
		return nil, err
	}
	parentIn, childOut, err := os.Pipe() // браузер пишет fd 4, мы читаем
	if err != nil {
		_ = childIn.Close()
		_ = parentOut.Close()
		return nil, err
	}
	// Без CommandContext: запуск живет дольше ctx вызова, остановка — Close.
	cmd := exec.Command(path, append(args, "about:blank")...)
	cmd.ExtraFiles = []*os.File{childIn, childOut}
	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{childIn, parentOut, parentIn, childOut} {
			_ = f.Close()
		}
		return nil, fmt.Errorf("запуск %s: %w", filepath.Base(path), err)
	}
	_ = childIn.Close() // концы ребенка остались у него
	_ = childOut.Close()
	c := &Chrome{cmd: cmd, out: parentOut, wait: map[int64]chan cdpReply{}, change: make(chan struct{}), dead: make(chan struct{})}
	go func() { c.die(cmd.Wait()) }()
	go c.readLoop(parentIn)
	if err := c.attach(ctx, o); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Chrome) attach(ctx context.Context, o Options) error {
	r, err := c.call(ctx, "", "Target.getTargets", nil)
	if err != nil {
		return err
	}
	var ts struct {
		TargetInfos []struct{ TargetID, Type string } `json:"targetInfos"`
	}
	_ = json.Unmarshal(r, &ts)
	id := ""
	for _, t := range ts.TargetInfos {
		if t.Type == "page" {
			id = t.TargetID
			break
		}
	}
	if id == "" {
		r, err = c.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"})
		if err != nil {
			return err
		}
		var ct struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(r, &ct)
		id = ct.TargetID
	}
	if r, err = c.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": id, "flatten": true}); err != nil {
		return err
	}
	var at struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(r, &at)
	if at.SessionID == "" {
		return errors.New("браузер не дал сессию страницы")
	}
	c.sess = at.SessionID
	// Загрузки закрыты: страница не кладет файлы в ~/Downloads мимо человека.
	if _, err := c.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{"behavior": "deny"}); err != nil {
		return err
	}
	if _, err := c.call(ctx, c.sess, "Page.enable", nil); err != nil {
		return err
	}
	if o.Headless {
		// Без окна Chrome называет себя HeadlessChrome, и часть сайтов видит в этом бота.
		if err := c.hideHeadless(ctx); err != nil {
			return err
		}
	}
	return c.resize(ctx, o.Width, o.Height)
}

func (c *Chrome) hideHeadless(ctx context.Context) error {
	r, err := c.call(ctx, "", "Browser.getVersion", nil)
	if err != nil {
		return err
	}
	var v struct {
		UserAgent string `json:"userAgent"`
	}
	_ = json.Unmarshal(r, &v)
	if !strings.Contains(v.UserAgent, "HeadlessChrome") {
		return nil
	}
	_, err = c.call(ctx, c.sess, "Emulation.setUserAgentOverride", map[string]any{"userAgent": strings.Replace(v.UserAgent, "HeadlessChrome", "Chrome", 1)})
	return err
}

// resize sets the page's screen size and restarts the picture stream at it:
// вкладка занимает все окно DVK, и размер приходит от окна.
func (c *Chrome) resize(ctx context.Context, w, h int) error {
	if _, err := c.call(ctx, c.sess, "Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": h, "deviceScaleFactor": 1, "mobile": false}); err != nil {
		return err
	}
	_, _ = c.call(ctx, c.sess, "Page.stopScreencast", nil) // первый запуск: останавливать нечего
	_, err := c.call(ctx, c.sess, "Page.startScreencast", map[string]any{"format": "jpeg", "quality": 70, "maxWidth": w, "maxHeight": h, "everyNthFrame": 1})
	return err
}

// die records why the browser is gone; первое слово остается.
func (c *Chrome) die(err error) {
	c.once.Do(func() {
		if err == nil {
			err = errors.New("браузер закрыт")
		}
		c.deadErr = err
		close(c.dead)
	})
}

// Close stops the browser: сначала просьбой закрыться (профиль остается
// целым), потом, если не вышло за три секунды, силой.
//
// ponytail: убитый kbengine оставляет Chrome с занятым профилем; канал при этом
// закрывается, и Chrome сам завершается (EOF на fd 3). Потолок — зависший
// Chrome, не читающий канал; путь — убивать по PID из файла.
func (c *Chrome) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, _ = c.call(ctx, "", "Browser.close", nil)
	cancel()
	select {
	case <-c.dead:
	case <-time.After(3 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-c.dead
	}
	_ = c.out.Close()
	return nil
}

func (c *Chrome) call(ctx context.Context, sess, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	c.wait[id] = ch
	c.mu.Unlock()
	defer func() { // ответа может не быть (отмена, смерть браузера): запись не должна копиться
		c.mu.Lock()
		delete(c.wait, id)
		c.mu.Unlock()
	}()
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sess != "" {
		msg["sessionId"] = sess
	}
	b, _ := json.Marshal(msg)
	c.wmu.Lock()
	_, err := c.out.Write(append(b, 0)) // сообщения в канале разделены нулевым байтом
	c.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.dead:
		return nil, c.deadErr
	}
}

func (c *Chrome) readLoop(in io.Reader) {
	br := bufio.NewReaderSize(in, 1<<20) // кадр JPEG бывает в сотни КБ
	for {
		b, err := br.ReadBytes(0)
		if err != nil {
			c.die(err)
			return
		}
		var m struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b[:len(b)-1], &m) != nil {
			continue
		}
		switch {
		case m.ID != 0:
			c.mu.Lock()
			ch := c.wait[m.ID]
			delete(c.wait, m.ID)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if m.Error != nil {
				ch <- cdpReply{err: errors.New(m.Error.Message)}
			} else {
				ch <- cdpReply{result: m.Result}
			}
		case m.Method == "Page.screencastFrame":
			c.onFrame(m.Params)
		case m.Method == "Page.frameNavigated":
			c.onNavigated(m.Params)
		case m.Method == "Page.javascriptDialogOpening":
			// Диалог без ответа замораживает страницу, а окна Chrome человек может не видеть.
			// ponytail: отказываемся молча; потолок — beforeunload и confirm нельзя принять;
			// путь — показывать текст диалога в окне и спрашивать.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = c.call(ctx, c.sess, "Page.handleJavaScriptDialog", map[string]any{"accept": false})
			}()
		}
	}
}

func (c *Chrome) onNavigated(p json.RawMessage) {
	var f struct {
		Frame struct {
			ParentID string `json:"parentId"`
			URL      string `json:"url"`
		} `json:"frame"`
	}
	if json.Unmarshal(p, &f) != nil || f.Frame.ParentID != "" {
		return // только главный фрейм: вложенные не адрес страницы
	}
	c.mu.Lock()
	c.url = f.Frame.URL
	c.mu.Unlock()
}

func (c *Chrome) onFrame(p json.RawMessage) {
	var f struct {
		Data      string `json:"data"`
		SessionID int    `json:"sessionId"`
	}
	if json.Unmarshal(p, &f) != nil {
		return
	}
	// Подтверждение кадра — иначе браузер перестает слать следующие.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.call(ctx, c.sess, "Page.screencastFrameAck", map[string]any{"sessionId": f.SessionID})
	}()
	jpg, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.frame = Frame{Seq: c.frame.Seq + 1, JPEG: jpg, URL: c.url}
	close(c.change)
	c.change = make(chan struct{})
	c.mu.Unlock()
}

// Next implements Screen.
func (c *Chrome) Next(ctx context.Context, after uint64) (Frame, error) {
	for {
		c.mu.Lock()
		f, ch := c.frame, c.change
		c.mu.Unlock()
		if f.Seq > after {
			return f, nil
		}
		select {
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		case <-c.dead:
			return Frame{}, c.deadErr
		case <-ch:
		}
	}
}

// namedKeys maps the names the window may press to what Chrome expects.
var namedKeys = map[string]struct {
	code string
	vk   int
	text string
}{
	"Enter": {"Enter", 13, "\r"}, "Backspace": {"Backspace", 8, ""}, "Tab": {"Tab", 9, ""},
	"Escape": {"Escape", 27, ""}, "Delete": {"Delete", 46, ""},
	"ArrowLeft": {"ArrowLeft", 37, ""}, "ArrowUp": {"ArrowUp", 38, ""}, "ArrowRight": {"ArrowRight", 39, ""}, "ArrowDown": {"ArrowDown", 40, ""},
	"Home": {"Home", 36, ""}, "End": {"End", 35, ""}, "PageUp": {"PageUp", 33, ""}, "PageDown": {"PageDown", 34, ""},
}

// Input implements Screen; события Handler уже проверил.
func (c *Chrome) Input(ctx context.Context, in Input) error {
	switch in.T {
	case "mouse":
		typ := map[string]string{"pressed": "mousePressed", "released": "mouseReleased", "moved": "mouseMoved"}[in.Type]
		c.mu.Lock()
		if in.Type != "moved" {
			c.down = in.Type == "pressed"
		}
		down := c.down
		c.mu.Unlock()
		p := map[string]any{"type": typ, "x": in.X, "y": in.Y, "button": "none"}
		if in.Type != "moved" {
			p["button"], p["clickCount"] = "left", 1
		} else if down {
			p["button"], p["buttons"] = "left", 1
		}
		_, err := c.call(ctx, c.sess, "Input.dispatchMouseEvent", p)
		return err
	case "wheel":
		_, err := c.call(ctx, c.sess, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": in.X, "y": in.Y, "deltaX": in.DX, "deltaY": in.DY})
		return err
	case "text":
		_, err := c.call(ctx, c.sess, "Input.insertText", map[string]any{"text": in.Text})
		return err
	case "key":
		k := namedKeys[in.Key]
		down := map[string]any{"type": "rawKeyDown", "key": in.Key, "code": k.code, "windowsVirtualKeyCode": k.vk}
		if k.text != "" {
			down["type"], down["text"] = "keyDown", k.text
		}
		if _, err := c.call(ctx, c.sess, "Input.dispatchKeyEvent", down); err != nil {
			return err
		}
		_, err := c.call(ctx, c.sess, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": in.Key, "code": k.code, "windowsVirtualKeyCode": k.vk})
		return err
	case "nav":
		return c.navigate(ctx, in.Text)
	case "resize":
		return c.resize(ctx, int(in.X), int(in.Y))
	}
	return fmt.Errorf("событие %q не поддержано", in.T)
}

func (c *Chrome) navigate(ctx context.Context, what string) error {
	switch what {
	case "reload":
		_, err := c.call(ctx, c.sess, "Page.reload", nil)
		return err
	case "back", "forward":
		r, err := c.call(ctx, c.sess, "Page.getNavigationHistory", nil)
		if err != nil {
			return err
		}
		var h struct {
			CurrentIndex int `json:"currentIndex"`
			Entries      []struct {
				ID int `json:"id"`
			} `json:"entries"`
		}
		_ = json.Unmarshal(r, &h)
		i := h.CurrentIndex - 1
		if what == "forward" {
			i = h.CurrentIndex + 1
		}
		if i < 0 || i >= len(h.Entries) {
			return nil // дальше некуда: не ошибка
		}
		_, err = c.call(ctx, c.sess, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[i].ID})
		return err
	}
	r, err := c.call(ctx, c.sess, "Page.navigate", map[string]any{"url": what})
	if err != nil {
		return err
	}
	var n struct {
		ErrorText string `json:"errorText"`
	}
	_ = json.Unmarshal(r, &n)
	if n.ErrorText != "" {
		return errors.New(n.ErrorText)
	}
	return nil
}

var _ Screen = (*Chrome)(nil)
