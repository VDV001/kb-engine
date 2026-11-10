package cdpbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
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
type Chrome struct {
	cmd  *exec.Cmd
	conn *websocket.Conn
	sess string // сессия страницы в CDP

	nextID atomic.Int64
	mu     sync.Mutex
	wait   map[int64]chan cdpReply
	frame  Frame
	change chan struct{} // закрывается при каждом новом кадре
	down   bool          // кнопка мыши зажата: движение тогда перетаскивание

	dead    chan struct{}
	deadErr error
	once    sync.Once
}

type cdpReply struct {
	result json.RawMessage
	err    error
}

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
	portFile := filepath.Join(o.Profile, "DevToolsActivePort")
	_ = os.Remove(portFile) // от прошлого запуска: иначе подключимся к умершему порту
	args := []string{
		"--remote-debugging-port=0", // порт выбирает система: чужой процесс его заранее не знает
		"--user-data-dir=" + o.Profile, "--no-first-run", "--no-default-browser-check",
		"--window-size=" + strconv.Itoa(o.Width) + "," + strconv.Itoa(o.Height),
	}
	if o.Headless {
		args = append(args, "--headless=new")
	}
	// Без CommandContext: запуск живет дольше ctx вызова, остановка — Close.
	cmd := exec.Command(path, append(args, "about:blank")...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("запуск %s: %w", filepath.Base(path), err)
	}
	c := &Chrome{cmd: cmd, wait: map[int64]chan cdpReply{}, change: make(chan struct{}), dead: make(chan struct{})}
	go func() { c.die(cmd.Wait()) }()

	wsURL, err := waitDevTools(ctx, portFile, c.dead)
	if err != nil {
		c.kill()
		return nil, err
	}
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		c.kill()
		return nil, fmt.Errorf("подключение к браузеру: %w", err)
	}
	conn.SetReadLimit(32 << 20) // кадр JPEG бывает в сотни КБ
	c.conn = conn
	go c.readLoop()
	if err := c.attach(ctx, o); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// waitDevTools reads the port and path Chrome writes once it listens.
func waitDevTools(ctx context.Context, portFile string, dead <-chan struct{}) (string, error) {
	deadline := time.After(20 * time.Second)
	for {
		if raw, err := os.ReadFile(portFile); err == nil {
			if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) >= 2 {
				return "ws://127.0.0.1:" + strings.TrimSpace(lines[0]) + strings.TrimSpace(lines[1]), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-dead:
			return "", errors.New("браузер закрылся сразу после запуска")
		case <-deadline:
			return "", errors.New("браузер не открыл порт отладки за 20 с")
		case <-time.After(100 * time.Millisecond):
		}
	}
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
	for _, step := range []struct {
		m string
		p any
	}{
		{"Page.enable", nil},
		{"Emulation.setDeviceMetricsOverride", map[string]any{"width": o.Width, "height": o.Height, "deviceScaleFactor": 1, "mobile": false}},
		{"Page.startScreencast", map[string]any{"format": "jpeg", "quality": 70, "maxWidth": o.Width, "maxHeight": o.Height, "everyNthFrame": 1}},
	} {
		if _, err := c.call(ctx, c.sess, step.m, step.p); err != nil {
			return err
		}
	}
	return nil
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

func (c *Chrome) kill() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// Close stops the browser: сначала просьбой закрыться (профиль остается
// целым), потом, если не вышло за три секунды, силой.
func (c *Chrome) Close() error {
	if c.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = c.call(ctx, "", "Browser.close", nil)
		cancel()
	}
	select {
	case <-c.dead:
	case <-time.After(3 * time.Second):
		c.kill()
		<-c.dead
	}
	if c.conn != nil {
		_ = c.conn.CloseNow()
	}
	return nil
}

func (c *Chrome) call(ctx context.Context, sess, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	c.wait[id] = ch
	c.mu.Unlock()
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sess != "" {
		msg["sessionId"] = sess
	}
	b, _ := json.Marshal(msg)
	if err := c.conn.Write(ctx, websocket.MessageText, b); err != nil {
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

func (c *Chrome) readLoop() {
	for {
		_, b, err := c.conn.Read(context.Background())
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
		if json.Unmarshal(b, &m) != nil {
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
		}
	}
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
	c.frame = Frame{Seq: c.frame.Seq + 1, JPEG: jpg}
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
