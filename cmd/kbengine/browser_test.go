package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/cdpbrowser"
)

type stubBrowser struct{ closed bool }

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
	tok := filepath.Join(t.TempDir(), "tok")
	launch := func(context.Context, cdpbrowser.Options) (browserScreen, error) { return &stubBrowser{}, nil }
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"нет подкоманды", nil, "usage"},
		{"чужая подкоманда", []string{"fly"}, "usage"},
		{"адрес в сеть", []string{"serve", "--addr", "0.0.0.0:8790", "--token-file", tok}, "только 127.0.0.1"},
		{"адрес без порта", []string{"serve", "--addr", "localhost", "--token-file", tok}, "host:port"},
		{"имя вместо адреса", []string{"serve", "--addr", "example.com:80", "--token-file", tok}, "только 127.0.0.1"},
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

func TestBrowserServeAnswersWithToken(t *testing.T) {
	tokPath := filepath.Join(t.TempDir(), "tok")
	token, err := ensureBrowserToken(tokPath)
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubBrowser{}
	launch := func(context.Context, cdpbrowser.Options) (browserScreen, error) { return stub, nil }
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() {
		var errb bytes.Buffer
		done <- browserServe(ctx, []string{"serve", "--addr", "127.0.0.1:0", "--token-file", tokPath, "--profile", t.TempDir()}, pw, &errb, launch)
		_ = pw.Close()
	}()
	// Первая строка вывода — адрес, который выбрала система.
	buf := make([]byte, 256)
	n, _ := pr.Read(buf)
	addr := regexp.MustCompile(`127\.0\.0\.1:\d+`).FindString(string(buf[:n]))
	if addr == "" {
		cancel()
		t.Fatalf("адрес не напечатан: %q", buf[:n])
	}
	get := func(tok string) int {
		r, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/frame?after=0", nil)
		if tok != "" {
			r.Header.Set("X-KB-Token", tok)
		}
		resp, err := http.DefaultClient.Do(r)
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
}
