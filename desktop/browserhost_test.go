//go:build unix

package main

import (
	"errors"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain делает из тестового бинарника поддельный `kbengine browser serve`:
// окно запускает настоящий исполняемый файл, и проверять запуск, остановку и
// отказ надо на настоящем процессе, а не на подмене.
func TestMain(m *testing.M) {
	if mode := os.Getenv("KB_FAKE_BROWSER"); mode != "" {
		os.Exit(fakeBrowserServe(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeBrowserServe(mode string, args []string) int {
	if mode == "fail" {
		os.Stderr.WriteString("не удалось запустить Brave: профиль занят\n")
		return 1
	}
	var sock string
	for i, a := range args {
		if a == "--socket" && i+1 < len(args) {
			sock = args[i+1]
		}
	}
	if out := os.Getenv("KB_FAKE_ARGS"); out != "" {
		_ = os.WriteFile(out, []byte(strings.Join(args, "\n")), 0o600)
	}
	_ = os.MkdirAll(filepath.Dir(sock), 0o700)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return 2
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
		select {}
	}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	<-term
	_ = ln.Close()
	return 0
}

// shortHome — домашняя папка с коротким путем: сокет в ней не должен выйти за 104 байта macOS.
func shortHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kbh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newTestHost(t *testing.T, mode string) *browserHost {
	t.Helper()
	t.Setenv("KB_FAKE_BROWSER", mode)
	h := newBrowserHost(shortHome(t))
	h.grace = 300 * time.Millisecond
	h.ready = 3 * time.Second
	t.Cleanup(func() { _ = h.Stop() })
	return h
}

func selfPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestBrowserHostStartStop(t *testing.T) {
	h := newTestHost(t, "ok")
	argsFile := filepath.Join(h.home, "args")
	t.Setenv("KB_FAKE_ARGS", argsFile)
	if st := h.State(); st.Running || st.Own {
		t.Fatalf("до запуска State = %+v", st)
	}
	if err := h.Start(selfPath(t), "/Applications/Brave"); err != nil {
		t.Fatal(err)
	}
	if st := h.State(); !st.Running || !st.Own {
		t.Fatalf("после Start State = %+v, want Running и Own", st)
	}
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"browser\nserve", "--socket\n" + filepath.Join(h.home, ".kb-tokens", "browser.sock"), "--chrome\n/Applications/Brave"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("аргументы serve %q не содержат %q", got, want)
		}
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if st := h.State(); st.Running || st.Own {
		t.Fatalf("после Stop State = %+v", st)
	}
}

func TestBrowserHostStartWithoutChoiceOmitsChromeFlag(t *testing.T) {
	h := newTestHost(t, "ok")
	argsFile := filepath.Join(h.home, "args")
	t.Setenv("KB_FAKE_ARGS", argsFile)
	if err := h.Start(selfPath(t), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(argsFile); strings.Contains(string(got), "--chrome") {
		t.Fatalf("без выбора флаг --chrome не нужен: %q", got)
	}
}

func TestBrowserHostStartFailureCarriesReason(t *testing.T) {
	h := newTestHost(t, "fail")
	err := h.Start(selfPath(t), "")
	if err == nil || !strings.Contains(err.Error(), "профиль занят") {
		t.Fatalf("Start = %v, want причина из stderr сервера", err)
	}
	if st := h.State(); st.Running || st.Own || !strings.Contains(st.Why, "профиль занят") {
		t.Fatalf("State = %+v, want не запущен и Why с причиной", st)
	}
}

func TestBrowserHostStartMissingBinary(t *testing.T) {
	h := newTestHost(t, "ok")
	if err := h.Start(filepath.Join(h.home, "нет-kbengine"), ""); err == nil {
		t.Fatal("Start без исполняемого файла должен вернуть ошибку")
	}
}

func TestBrowserHostRefusesSecondStart(t *testing.T) {
	h := newTestHost(t, "ok")
	if err := h.Start(selfPath(t), ""); err != nil {
		t.Fatal(err)
	}
	if err := h.Start(selfPath(t), ""); !errors.Is(err, errBrowserRunning) {
		t.Fatalf("второй Start = %v, want errBrowserRunning", err)
	}
}

// Отрицательный контроль: сервер, запущенный не окном (из терминала), окно
// не останавливает — ни сигналом, ни закрытием сокета.
func TestBrowserHostLeavesForeignServerAlone(t *testing.T) {
	h := newTestHost(t, "ok")
	dir := filepath.Join(h.home, ".kb-tokens")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "browser.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if st := h.State(); !st.Running || st.Own {
		t.Fatalf("State = %+v, want Running без Own", st)
	}
	if err := h.Stop(); !errors.Is(err, errBrowserForeign) {
		t.Fatalf("Stop чужого = %v, want errBrowserForeign", err)
	}
	if st := h.State(); !st.Running {
		t.Fatal("чужой сервер остановлен окном")
	}
	if err := h.Start(selfPath(t), ""); !errors.Is(err, errBrowserRunning) {
		t.Fatalf("Start поверх чужого = %v, want errBrowserRunning", err)
	}
}

func TestBrowserHostKillsStubbornServer(t *testing.T) {
	h := newTestHost(t, "stubborn")
	if err := h.Start(selfPath(t), ""); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took < h.grace || took > 3*time.Second {
		t.Fatalf("Stop занял %v: сначала ждем %v на SIGTERM, потом Kill", took, h.grace)
	}
	if st := h.State(); st.Running || st.Own {
		t.Fatalf("после Kill State = %+v", st)
	}
}

func TestBrowserHostStopWhenNotRunning(t *testing.T) {
	h := newTestHost(t, "ok")
	if err := h.Stop(); err != nil {
		t.Fatalf("Stop выключенного = %v, want nil", err)
	}
}

// Свой процесс сам вышел (Chrome закрыли руками, serve завершился): State это видит.
func TestBrowserHostNoticesServerExit(t *testing.T) {
	h := newTestHost(t, "ok")
	if err := h.Start(selfPath(t), ""); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	_ = h.cmd.Process.Signal(syscall.SIGTERM)
	h.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for h.State().Own && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := h.State(); st.Running || st.Own {
		t.Fatalf("State = %+v после выхода процесса", st)
	}
}
