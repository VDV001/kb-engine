package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/cdpbrowser"
)

type stubBrowser struct {
	closed bool
	done   chan struct{}
}

func (s *stubBrowser) Done() <-chan struct{} { return s.done }

func (s *stubBrowser) Next(ctx context.Context, after uint64) (cdpbrowser.Frame, error) {
	return cdpbrowser.Frame{Seq: after + 1, JPEG: []byte("\xff\xd8x")}, nil
}
func (s *stubBrowser) Input(context.Context, cdpbrowser.Input) error { return nil }
func (s *stubBrowser) Close() error                                  { s.closed = true; return nil }

func TestEnsureBrowserToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "browser")

	tok, err := ensureBrowserToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < minTokenLen || !printableASCII(tok) {
		t.Fatalf("токен слабый: %q", tok)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("права файла токена: %v %v, want 0600", st.Mode().Perm(), err)
	}
	// Второй запуск читает тот же токен: окно уже знает его из файла.
	again, err := ensureBrowserToken(path)
	if err != nil || again != tok {
		t.Fatalf("повторно: %q %v, want %q", again, err, tok)
	}
	// Файл с чужими правами — отказ, а не молчаливое чтение.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureBrowserToken(path); err == nil {
		t.Fatal("токен с правами 0644 принят")
	}
}

func TestBrowserServeRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(dir, "b.sock")
	tok := filepath.Join(t.TempDir(), "tok")
	launch := func(context.Context, cdpbrowser.Options) (browserScreen, error) { return &stubBrowser{}, nil }
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"нет подкоманды", nil, "usage"},
		{"чужая подкоманда", []string{"fly"}, "usage"},
		{"папка сокета открыта другим", []string{"serve", "--socket", open, "--token-file", tok}, "доступна другим"},
		{"слишком длинный путь сокета", []string{"serve", "--socket", filepath.Join(t.TempDir(), strings.Repeat("d", 120), "b.sock"), "--token-file", tok}, "длиннее предела"},
		{"адрес вместо сокета", []string{"serve", "--addr", "127.0.0.1:8790"}, "flag provided but not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := browserServe(context.Background(), tc.args, &out, &errb, launch)
			if code == 0 || !strings.Contains(errb.String()+out.String(), tc.want) {
				t.Fatalf("code=%d out=%q err=%q, want отказ со словом %q", code, out.String(), errb.String(), tc.want)
			}
		})
	}
}

// unixClient ходит на сокет сервера браузера, как окно.
func unixClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
}

// shortSock — путь сокета короче предела sockaddr_un (104 байта на macOS):
// t.TempDir() с длинным именем теста не помещается.
func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kbb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "b.sock")
}

func waitSock(t *testing.T, sock string) {
	t.Helper()
	for range 100 {
		if c, err := net.Dial("unix", sock); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("сокет не открылся")
}

func TestBrowserServeAnswersWithToken(t *testing.T) {
	tokPath := filepath.Join(t.TempDir(), "tok")
	token, err := ensureBrowserToken(tokPath)
	if err != nil {
		t.Fatal(err)
	}
	sock := shortSock(t)
	stub := &stubBrowser{}
	launch := func(context.Context, cdpbrowser.Options) (browserScreen, error) { return stub, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var out, errb bytes.Buffer
		done <- browserServe(ctx, []string{"serve", "--socket", sock, "--token-file", tokPath, "--profile", t.TempDir()}, &out, &errb, launch)
	}()
	waitSock(t, sock)
	if st, err := os.Stat(sock); err != nil || st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("права сокета: %v %v, want без доступа другим", st.Mode().Perm(), err)
	}
	get := func(tok string) int {
		r, _ := http.NewRequest(http.MethodGet, "http://browser/frame?after=0", nil)
		if tok != "" {
			r.Header.Set("X-KB-Token", tok)
		}
		resp, err := unixClient(sock).Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := get(""); got != http.StatusUnauthorized {
		t.Fatalf("без токена: %d, want 401", got)
	}
	if got := get(token); got != http.StatusOK {
		t.Fatalf("с токеном: %d, want 200", got)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("выход %d после остановки, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve не остановился по отмене")
	}
	if !stub.closed {
		t.Fatal("браузер не закрыт при остановке")
	}
	if _, err := os.Stat(sock); err == nil {
		t.Fatal("сокет остался после остановки")
	}
}

// Закрыли окно Chrome — serve не остается отвечать 502 вечно: выходит с кодом 1,
// и окно DVK показывает подсказку «браузер не запущен».
func TestBrowserServeExitsWhenBrowserCloses(t *testing.T) {
	tokPath := filepath.Join(t.TempDir(), "tok")
	sock := shortSock(t)
	stub := &stubBrowser{done: make(chan struct{})}
	launch := func(context.Context, cdpbrowser.Options) (browserScreen, error) { return stub, nil }
	done := make(chan int, 1)
	go func() {
		var out, errb bytes.Buffer
		done <- browserServe(context.Background(), []string{"serve", "--socket", sock, "--token-file", tokPath, "--profile", t.TempDir()}, &out, &errb, launch)
	}()
	waitSock(t, sock)
	close(stub.done)
	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("выход %d после закрытия браузера, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve не вышел, хотя браузер закрыт")
	}
}

// Живой сервер на сокете — отказ до запуска Chrome: второй экземпляр не должен
// трогать профиль первого.
func TestBrowserServeBusySocketDoesNotLaunch(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	launched := false
	launch := func(context.Context, cdpbrowser.Options) (browserScreen, error) {
		launched = true
		return &stubBrowser{}, nil
	}
	var out, errb bytes.Buffer
	code := browserServe(context.Background(), []string{"serve", "--socket", sock, "--token-file", filepath.Join(t.TempDir(), "tok"), "--profile", t.TempDir()}, &out, &errb, launch)
	if code == 0 || launched || !strings.Contains(errb.String(), "уже запущен") {
		t.Fatalf("код %d, браузер запущен: %v, err=%q; на занятый сокет запуск не нужен", code, launched, errb.String())
	}
}

// Файл сокета от прошлого запуска (процесс убили) не мешает: на него никто не слушает.
func TestBrowserServeReplacesStaleSocket(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	_ = ln.Close() // файл остался, слушателя нет
	if _, err := os.Stat(sock); err != nil {
		t.Fatal("заготовка: файла сокета нет")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var out, errb bytes.Buffer
		done <- browserServe(ctx, []string{"serve", "--socket", sock, "--token-file", filepath.Join(t.TempDir(), "tok"), "--profile", t.TempDir()}, &out, &errb,
			func(context.Context, cdpbrowser.Options) (browserScreen, error) { return &stubBrowser{}, nil })
	}()
	waitSock(t, sock)
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("выход %d, want 0", code)
	}
}
