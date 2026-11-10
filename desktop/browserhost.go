package main

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Кнопки «Включить» и «Выключить» во вкладке «Браузер»: окно запускает
// `kbengine browser serve` дочерним процессом и останавливает его. Сервер остается
// отдельным процессом (падение Chrome не задевает окно, весь код serve и его тесты
// те же, что у команды для терминала).

var (
	errBrowserRunning = errors.New("браузер уже запущен")
	errBrowserForeign = errors.New("браузер запущен не из окна: остановите его там, где запускали (Ctrl+C в терминале)")
)

// browserState is what the tab shows: Running — сокет отвечает; Own — процесс запущен
// этим окном и им же останавливается; Why — почему последний запуск не удался.
type browserState struct {
	Running, Own bool
	Why          string
}

type browserHost struct {
	home         string
	grace, ready time.Duration // сколько ждать выхода после SIGTERM и появления сокета

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{} // закрыт, когда cmd завершился
	why  string
}

func newBrowserHost(home string) *browserHost {
	return &browserHost{home: home, grace: 5 * time.Second, ready: 10 * time.Second}
}

func (h *browserHost) tokens() string { return filepath.Join(h.home, ".kb-tokens") }

// listening: «работает ли» — по сокету, а не по своему процессу, чтобы видеть и сервер из терминала.
func (h *browserHost) listening() bool {
	c, err := net.DialTimeout("unix", filepath.Join(h.tokens(), "browser.sock"), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (h *browserHost) alive() bool {
	if h.cmd == nil {
		return false
	}
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

// State reports the browser server; вызывается опросом вкладки раз в пару секунд.
func (h *browserHost) State() browserState {
	h.mu.Lock()
	own, why := h.alive(), h.why
	h.mu.Unlock()
	return browserState{Running: h.listening(), Own: own, Why: why}
}

// Start launches the server with the chosen browser (chrome пусто — первый найденный).
// Возвращается, когда сокет отвечает или процесс умер: причина — из его stderr.
func (h *browserHost) Start(bin, chrome string) error {
	h.mu.Lock()
	if h.alive() || h.listening() {
		h.mu.Unlock()
		return errBrowserRunning
	}
	args := []string{"browser", "serve", "--socket", filepath.Join(h.tokens(), "browser.sock"), "--token-file", filepath.Join(h.tokens(), "browser")}
	if chrome != "" {
		args = append(args, "--chrome", chrome)
	}
	cmd, errs := exec.Command(bin, args...), &tailBuf{}
	cmd.Stderr = errs
	if err := cmd.Start(); err != nil {
		h.why = "не удалось запустить kbengine: " + err.Error()
		h.mu.Unlock()
		return errors.New(h.why)
	}
	done := make(chan struct{})
	h.cmd, h.done, h.why = cmd, done, ""
	go func() { _ = cmd.Wait(); close(done) }()
	h.mu.Unlock()

	deadline := time.After(h.ready)
	for tick := time.NewTicker(50 * time.Millisecond); ; <-tick.C {
		select {
		case <-done:
			return h.failed(fmt.Sprintf("браузер не запустился: %s", or(errs.String(), "сервер завершился без сообщения")))
		case <-deadline:
			_ = h.Stop()
			return h.failed("браузер не ответил за " + h.ready.String())
		default:
		}
		if h.listening() {
			return nil
		}
	}
}

func (h *browserHost) failed(why string) error {
	h.mu.Lock()
	h.why = why
	h.mu.Unlock()
	return errors.New(why)
}

// Stop ends the server this window started: SIGTERM (serve закрывает браузер сам),
// через grace — Kill. Чужой сервер не трогает.
//
// ponytail: при Kill сервер не успевает закрыть браузер сам; тот выходит по закрытию
// канала отладки. Потолок — зависший браузер, переживший канал; путь — группа процессов.
func (h *browserHost) Stop() error {
	h.mu.Lock()
	if !h.alive() {
		h.mu.Unlock()
		if h.listening() {
			return errBrowserForeign
		}
		return nil
	}
	cmd, done := h.cmd, h.done
	h.mu.Unlock()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(h.grace):
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}

// tailBuf keeps the last bytes written: причина отказа — в конце вывода сервера.
type tailBuf struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}
