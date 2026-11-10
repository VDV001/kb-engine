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
	"slices"
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
	// ScreencastIdle — сколько картинка идет после последнего запроса кадра; пусто — 10 с.
	ScreencastIdle time.Duration
}

const defaultScreencastIdle = 10 * time.Second

// Browser is an installed Chromium-based browser: Name — для человека, Path — что запускать.
type Browser struct{ Name, Path string }

// Любой браузер на движке Chromium говорит на одном протоколе; порядок списка —
// порядок поиска без выбора человека.
var (
	macBrowsers = []Browser{
		{"Google Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		{"Brave", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
		{"Microsoft Edge", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
		{"Chromium", "/Applications/Chromium.app/Contents/MacOS/Chromium"},
		{"Vivaldi", "/Applications/Vivaldi.app/Contents/MacOS/Vivaldi"},
	}
	pathBrowsers = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave-browser", "microsoft-edge"}
)

// ErrNoBrowser means no Chromium-based browser was found.
var ErrNoBrowser = errors.New("не нашла браузер на движке Chromium (Chrome, Brave, Edge, Chromium): поставьте любой или укажите путь флагом --chrome")

// Installed lists the Chromium-based browsers on this machine: сначала
// приложения Мака, потом найденные по PATH; один файл — одна запись.
func Installed() []Browser {
	var out []Browser
	add := func(b Browser) {
		if st, err := os.Stat(b.Path); err == nil && !st.IsDir() && !slices.ContainsFunc(out, func(o Browser) bool { return o.Path == b.Path }) {
			out = append(out, b)
		}
	}
	for _, b := range macBrowsers {
		add(b)
	}
	for _, name := range pathBrowsers {
		if p, err := exec.LookPath(name); err == nil {
			add(Browser{name, p})
		}
	}
	return out
}

// FindBrowser returns the first installed Chromium-based browser.
func FindBrowser() (string, error) {
	if all := Installed(); len(all) > 0 {
		return all[0].Path, nil
	}
	return "", ErrNoBrowser
}

// Chrome is a running browser; it is a Screen.
//
// CDP идет по каналу (--remote-debugging-pipe, дескрипторы 3 и 4), а не по
// порту: порт отладки открыт любому локальному процессу без токена, а через
// него — cookie и вкладки (находка security-ревью #526, F2).
type Chrome struct {
	cmd *exec.Cmd
	out io.WriteCloser // команды браузеру
	wmu sync.Mutex     // запись в канал — по одному сообщению

	nextID   atomic.Int64
	mu       sync.Mutex
	wait     map[int64]chan cdpReply
	sess     string // сессия текущей страницы в CDP
	pages    []page // открытые страницы; окно, открытое страницей, становится текущим
	main     string // id главного фрейма текущей страницы
	w, h     int    // размер экрана, который просило окно
	headless bool
	frame    Frame
	change   chan struct{} // закрывается при каждом новом кадре
	url      string
	down     bool // кнопка мыши зажата: движение тогда перетаскивание

	// Картинка идет, только пока кто-то спрашивает кадры: иначе страница с
	// видео грузила бы процессор при любой другой открытой вкладке окна.
	castMu   sync.Mutex // casting и вызовы start/stopScreencast — вместе
	casting  bool
	polling  int       // сколько запросов кадра ждут прямо сейчас (под mu)
	lastPoll time.Time // когда ушел последний (под mu)

	dead    chan struct{}
	deadErr error
	once    sync.Once
}

// page is one open tab of the browser and the CDP session attached to it.
type page struct{ target, sess string }

// newChrome builds the state around a started browser; out — канал команд.
func newChrome(cmd *exec.Cmd, out io.WriteCloser) *Chrome {
	return &Chrome{cmd: cmd, out: out, wait: map[int64]chan cdpReply{}, change: make(chan struct{}), dead: make(chan struct{}), lastPoll: time.Now()}
}

func (c *Chrome) session() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

func (c *Chrome) setSession(s string) {
	c.mu.Lock()
	c.sess = s
	c.mu.Unlock()
}

func (c *Chrome) currentURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.url
}

func (c *Chrome) lastFrame() Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frame
}

func (c *Chrome) screencasting() bool {
	c.castMu.Lock()
	defer c.castMu.Unlock()
	return c.casting
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
	c := newChrome(cmd, parentOut)
	c.headless = o.Headless
	go func() { c.die(cmd.Wait()) }()
	go c.readLoop(parentIn)
	if err := c.attach(ctx, o); err != nil {
		_ = c.Close()
		return nil, err
	}
	idle := o.ScreencastIdle
	if idle <= 0 {
		idle = defaultScreencastIdle
	}
	go c.idleLoop(idle)
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
	sess, err := c.attachTo(ctx, id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.sess, c.pages, c.w, c.h = sess, []page{{id, sess}}, o.Width, o.Height
	c.mu.Unlock()
	// Загрузки закрыты: страница не кладет файлы в ~/Downloads мимо человека.
	if _, err := c.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{"behavior": "deny"}); err != nil {
		return err
	}
	if err := c.prime(ctx, sess); err != nil {
		return err
	}
	// Окна, которые открывает сама страница (target=_blank, вход через Google), и
	// закрытие окон приходят событиями Target.*; страницы уже известны выше.
	_, err = c.call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true})
	return err
}

func (c *Chrome) attachTo(ctx context.Context, target string) (string, error) {
	r, err := c.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	if err != nil {
		return "", err
	}
	var at struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(r, &at)
	if at.SessionID == "" {
		return "", errors.New("браузер не дал сессию страницы")
	}
	return at.SessionID, nil
}

// prime readies a page session: события страницы, размер экрана, лицо без
// слова Headless. Картинку включает первый запрос кадра.
func (c *Chrome) prime(ctx context.Context, sess string) error {
	if _, err := c.call(ctx, sess, "Page.enable", nil); err != nil {
		return err
	}
	if c.headless {
		// Без окна Chrome называет себя HeadlessChrome, и часть сайтов видит в этом бота.
		if err := c.hideHeadless(ctx, sess); err != nil {
			return err
		}
	}
	c.mu.Lock()
	w, h := c.w, c.h
	c.mu.Unlock()
	_, err := c.call(ctx, sess, "Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": h, "deviceScaleFactor": 1, "mobile": false})
	if err != nil {
		return err
	}
	return c.syncFrameTree(ctx, sess)
}

// syncFrameTree learns the page's main frame and address: событий о уже
// загруженной странице браузер не повторяет.
func (c *Chrome) syncFrameTree(ctx context.Context, sess string) error {
	r, err := c.call(ctx, sess, "Page.getFrameTree", nil)
	if err != nil {
		return err
	}
	var t struct {
		FrameTree struct {
			Frame struct{ ID, URL string } `json:"frame"`
		} `json:"frameTree"`
	}
	_ = json.Unmarshal(r, &t)
	c.mu.Lock()
	if c.sess == sess {
		c.main, c.url = t.FrameTree.Frame.ID, t.FrameTree.Frame.URL
	}
	c.mu.Unlock()
	return nil
}

func (c *Chrome) hideHeadless(ctx context.Context, sess string) error {
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
	_, err = c.call(ctx, sess, "Emulation.setUserAgentOverride", map[string]any{"userAgent": strings.Replace(v.UserAgent, "HeadlessChrome", "Chrome", 1)})
	return err
}

// resize sets the page's screen size and, if the picture is on, restarts it at
// the new size: вкладка занимает все окно DVK, и размер приходит от окна.
func (c *Chrome) resize(ctx context.Context, w, h int) error {
	c.mu.Lock()
	c.w, c.h = w, h
	sess := c.sess
	c.mu.Unlock()
	if _, err := c.call(ctx, sess, "Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": h, "deviceScaleFactor": 1, "mobile": false}); err != nil {
		return err
	}
	c.castMu.Lock()
	defer c.castMu.Unlock()
	if !c.casting {
		return nil
	}
	_, _ = c.call(ctx, sess, "Page.stopScreencast", nil)
	return c.startScreencast(ctx, sess)
}

// startScreencast must be called under castMu.
func (c *Chrome) startScreencast(ctx context.Context, sess string) error {
	c.mu.Lock()
	w, h := c.w, c.h
	c.mu.Unlock()
	_, err := c.call(ctx, sess, "Page.startScreencast", map[string]any{"format": "jpeg", "quality": 70, "maxWidth": w, "maxHeight": h, "everyNthFrame": 1})
	return err
}

// watch turns the picture on for a viewer that asks for frames.
func (c *Chrome) watch(ctx context.Context) error {
	c.castMu.Lock()
	defer c.castMu.Unlock()
	if c.casting {
		return nil
	}
	if err := c.startScreencast(ctx, c.session()); err != nil {
		return err
	}
	c.casting = true
	return nil
}

// idleLoop stops the picture when nobody has asked for a frame for idle.
func (c *Chrome) idleLoop(idle time.Duration) {
	t := time.NewTicker(max(idle/4, 50*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-c.dead:
			return
		case <-t.C:
			c.stopIfIdle(idle)
		}
	}
}

func (c *Chrome) stopIfIdle(idle time.Duration) {
	c.castMu.Lock()
	defer c.castMu.Unlock()
	c.mu.Lock()
	quiet := c.polling == 0 && time.Since(c.lastPoll) > idle
	c.mu.Unlock()
	if !c.casting || !quiet {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = c.call(ctx, c.session(), "Page.stopScreencast", nil)
	c.casting = false
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
			ID        int64           `json:"id"`
			SessionID string          `json:"sessionId"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
			Result    json.RawMessage `json:"result"`
			Error     *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b[:len(b)-1], &m) != nil {
			continue
		}
		if m.ID == 0 {
			c.onEvent(m.SessionID, m.Method, m.Params)
			continue
		}
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
	}
}

// onEvent handles one event of the browser; sess — страница, приславшая его.
// Вызовы CDP отсюда только в отдельной горутине: читает ответы этот же цикл.
func (c *Chrome) onEvent(sess, method string, p json.RawMessage) {
	switch method {
	case "Page.screencastFrame":
		c.onFrame(sess, p)
	case "Page.frameNavigated":
		c.onNavigated(sess, p)
	case "Page.navigatedWithinDocument":
		c.onNavigatedInDocument(sess, p)
	case "Page.javascriptDialogOpening":
		// Диалог без ответа замораживает страницу, а окна Chrome человек может не видеть.
		// ponytail: отказываемся молча; потолок — beforeunload и confirm нельзя принять;
		// путь — показывать текст диалога в окне и спрашивать.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = c.call(ctx, sess, "Page.handleJavaScriptDialog", map[string]any{"accept": false})
		}()
	case "Target.targetCreated":
		c.onTargetCreated(p)
	case "Target.targetDestroyed":
		c.onTargetDestroyed(p)
	case "Target.targetCrashed":
		c.onTargetCrashed(p)
	}
}

func (c *Chrome) onNavigated(sess string, p json.RawMessage) {
	var f struct {
		Frame struct {
			ID       string `json:"id"`
			ParentID string `json:"parentId"`
			URL      string `json:"url"`
		} `json:"frame"`
	}
	if json.Unmarshal(p, &f) != nil || f.Frame.ParentID != "" {
		return // только главный фрейм: вложенные не адрес страницы
	}
	c.mu.Lock()
	if sess == c.sess {
		c.main, c.url = f.Frame.ID, f.Frame.URL
	}
	c.mu.Unlock()
}

// onNavigatedInDocument follows pushState and hash changes of single-page apps.
func (c *Chrome) onNavigatedInDocument(sess string, p json.RawMessage) {
	var f struct {
		FrameID string `json:"frameId"`
		URL     string `json:"url"`
	}
	if json.Unmarshal(p, &f) != nil {
		return
	}
	c.mu.Lock()
	if sess == c.sess && f.FrameID == c.main {
		c.url = f.URL
	}
	c.mu.Unlock()
}

func (c *Chrome) onFrame(sess string, p json.RawMessage) {
	var f struct {
		Data      string `json:"data"`
		SessionID int    `json:"sessionId"`
	}
	if json.Unmarshal(p, &f) != nil {
		return
	}
	// Подтверждение кадра — иначе браузер перестает слать следующие; страница
	// подтверждает кадры той сессии, что их прислала.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.call(ctx, sess, "Page.screencastFrameAck", map[string]any{"sessionId": f.SessionID})
	}()
	jpg, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sess != c.sess {
		return // закрытая или фоновая страница не перекрывает текущую
	}
	c.frame = Frame{Seq: c.frame.Seq + 1, JPEG: jpg, URL: c.url}
	close(c.change)
	c.change = make(chan struct{})
}

func (c *Chrome) onTargetCreated(p json.RawMessage) {
	var t struct {
		TargetInfo struct{ TargetID, Type string } `json:"targetInfo"`
	}
	if json.Unmarshal(p, &t) != nil || t.TargetInfo.Type != "page" {
		return
	}
	id := t.TargetInfo.TargetID
	c.mu.Lock()
	for _, pg := range c.pages {
		if pg.target == id {
			c.mu.Unlock()
			return // страницы, уже известные, повторно не берем
		}
	}
	c.pages = append(c.pages, page{target: id})
	c.mu.Unlock()
	go c.adopt(id)
}

// adopt makes a page the browser opened on its own the screen.
func (c *Chrome) adopt(target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := c.attachTo(ctx, target)
	if err != nil {
		c.forget(target)
		return
	}
	if err := c.prime(ctx, sess); err != nil {
		c.forget(target)
		return
	}
	c.switchTo(ctx, target, sess)
}

// switchTo shows another page: картинка идет только у одной.
func (c *Chrome) switchTo(ctx context.Context, target, sess string) {
	c.castMu.Lock()
	defer c.castMu.Unlock()
	c.mu.Lock()
	old := c.sess
	for i := range c.pages {
		if c.pages[i].target == target {
			c.pages[i].sess = sess
		}
	}
	c.sess = sess
	c.mu.Unlock()
	if c.casting {
		if old != "" && old != sess {
			_, _ = c.call(ctx, old, "Page.stopScreencast", nil)
		}
		_ = c.startScreencast(ctx, sess)
	}
	_ = c.syncFrameTree(ctx, sess)
}

func (c *Chrome) forget(target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, pg := range c.pages {
		if pg.target == target {
			c.pages = append(c.pages[:i], c.pages[i+1:]...)
			return
		}
	}
}

func (c *Chrome) onTargetDestroyed(p json.RawMessage) {
	var t struct {
		TargetID string `json:"targetId"`
	}
	if json.Unmarshal(p, &t) != nil {
		return
	}
	c.mu.Lock()
	var gone page
	for i, pg := range c.pages {
		if pg.target == t.TargetID {
			gone = pg
			c.pages = append(c.pages[:i], c.pages[i+1:]...)
			break
		}
	}
	wasCurrent := gone.sess != "" && gone.sess == c.sess
	var back page
	if wasCurrent {
		for _, pg := range slices.Backward(c.pages) {
			if pg.sess != "" {
				back = pg
				break
			}
		}
		c.sess = back.sess
	}
	c.mu.Unlock()
	switch {
	case !wasCurrent:
	case back.sess != "":
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c.switchTo(ctx, back.target, back.sess)
		}()
	default: // закрыли последнее окно: пустая страница, targetCreated возьмет ее экраном
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = c.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"})
		}()
	}
}

// onTargetCrashed closes a page whose renderer died: Target.targetDestroyed
// вернет экран на прежнее окно, а не оставит замершую картинку.
func (c *Chrome) onTargetCrashed(p json.RawMessage) {
	var t struct {
		TargetID string `json:"targetId"`
	}
	if json.Unmarshal(p, &t) != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": t.TargetID})
	}()
}

// Next implements Screen.
func (c *Chrome) Next(ctx context.Context, after uint64) (Frame, error) {
	c.mu.Lock()
	c.polling++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.polling--
		c.lastPoll = time.Now()
		c.mu.Unlock()
	}()
	if err := c.watch(ctx); err != nil {
		return Frame{}, err
	}
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
	"SelectAll": {"KeyA", 65, ""},
}

// Input implements Screen; события Handler уже проверил.
func (c *Chrome) Input(ctx context.Context, in Input) error {
	switch in.T {
	case "mouse":
		return c.inputMouse(ctx, in)
	case "wheel":
		_, err := c.call(ctx, c.session(), "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": in.X, "y": in.Y, "deltaX": in.DX, "deltaY": in.DY})
		return err
	case "text":
		_, err := c.call(ctx, c.session(), "Input.insertText", map[string]any{"text": in.Text})
		return err
	case "key":
		return c.inputKey(ctx, in)
	case "nav":
		return c.navigate(ctx, in.Text)
	case "resize":
		return c.resize(ctx, int(in.X), int(in.Y))
	}
	return fmt.Errorf("событие %q не поддержано", in.T)
}

func (c *Chrome) inputMouse(ctx context.Context, in Input) error {
	typ := map[string]string{"pressed": "mousePressed", "released": "mouseReleased", "moved": "mouseMoved"}[in.Type]
	c.mu.Lock()
	if in.Type != "moved" {
		c.down = in.Type == "pressed"
	}
	down := c.down
	c.mu.Unlock()
	p := map[string]any{"type": typ, "x": in.X, "y": in.Y, "button": "none", "modifiers": in.Mods}
	if in.Type != "moved" {
		// Двойной щелчок выделяет слово, тройной строку: число щелчков считает окно.
		p["button"], p["clickCount"] = "left", max(in.Count, 1)
	} else if down {
		p["button"], p["buttons"] = "left", 1
	}
	_, err := c.call(ctx, c.session(), "Input.dispatchMouseEvent", p)
	return err
}

func (c *Chrome) inputKey(ctx context.Context, in Input) error {
	k := namedKeys[in.Key]
	key, mods := in.Key, in.Mods
	down := map[string]any{"type": "rawKeyDown", "key": key, "code": k.code, "windowsVirtualKeyCode": k.vk}
	if in.Key == "SelectAll" { // Cmd+A и Ctrl+A окна: команда редактора, а не буква
		key, mods = "a", modMeta
		down["key"], down["commands"] = key, []string{"selectAll"}
	}
	down["modifiers"] = mods
	if k.text != "" {
		down["type"], down["text"] = "keyDown", k.text
	}
	if _, err := c.call(ctx, c.session(), "Input.dispatchKeyEvent", down); err != nil {
		return err
	}
	_, err := c.call(ctx, c.session(), "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": mods})
	return err
}

func (c *Chrome) navigate(ctx context.Context, what string) error {
	switch what {
	case "reload":
		_, err := c.call(ctx, c.session(), "Page.reload", nil)
		return err
	case "back", "forward":
		r, err := c.call(ctx, c.session(), "Page.getNavigationHistory", nil)
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
		_, err = c.call(ctx, c.session(), "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[i].ID})
		return err
	}
	r, err := c.call(ctx, c.session(), "Page.navigate", map[string]any{"url": what})
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
