package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/cdpbrowser"
)

const browserUsage = `usage: kbengine browser serve [--socket ~/.kb-tokens/browser.sock] [--token-file ~/.kb-tokens/browser] [--profile DIR] [--chrome PATH] [--window]
       Браузер для вкладки «Браузер» окна DVK: Chrome, Brave, Edge или Chromium на этом Mac.
       Сервер слушает unix-сокет в вашей папке (не сетевой порт) и каждый запрос проверяет по токену.`

// Отказы запуска. Сокет, а не порт: порт на loopback занимает любой процесс
// Мака (и чужой пользователь), а папка с правами 0700 достается только вам
// (находка security-ревью #526, F1).
var (
	errBrowserSocketDir = errors.New("папка сокета доступна другим: нужны права 0700")
	errBrowserNotSocket = errors.New("по пути --socket лежит не сокет, файл не тронут")
	errBrowserRunning   = errors.New("kbengine browser serve уже запущен на этом сокете")
	errBrowserSockPath  = errors.New("путь сокета длиннее предела системы (104 байта на macOS): выберите короче флагом --socket")
)

// browserScreen is a launched browser as the command uses it.
type browserScreen interface {
	cdpbrowser.Screen
	Close() error
}

func launchBrowser(ctx context.Context, o cdpbrowser.Options) (browserScreen, error) {
	return cdpbrowser.Launch(ctx, o)
}

func runBrowser(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return browserServe(ctx, args, stdout, stderr, launchBrowser)
}

func browserServe(ctx context.Context, args []string, stdout, stderr io.Writer,
	launch func(context.Context, cdpbrowser.Options) (browserScreen, error),
) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(stdout, browserUsage)
		return 0
	}
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprintln(stderr, browserUsage)
		return 2
	}
	o, code := parseBrowserFlags(args[1:], stderr)
	if code != 0 {
		return code
	}
	token, err := ensureBrowserToken(o.tokenFile)
	if err != nil {
		fmt.Fprintln(stderr, "токен браузера:", err)
		return 1
	}
	// Сокет раньше браузера: занятый или чужой путь — отказ до того, как Chrome
	// откроет окно и тронет профиль.
	ln, err := listenBrowserSocket(o.socket)
	if err != nil {
		fmt.Fprintln(stderr, "сокет:", err)
		return 1
	}
	scr, err := launch(ctx, cdpbrowser.Options{Path: o.chrome, Profile: o.profile, Width: cdpbrowser.DefaultWidth, Height: cdpbrowser.DefaultHeight, Headless: o.headless})
	if err != nil {
		_ = ln.Close()
		fmt.Fprintln(stderr, "браузер:", err)
		return 1
	}
	defer func() { _ = scr.Close() }()
	fmt.Fprintf(stdout, "kbengine browser: слушает %s, токен в %s\n", o.socket, o.tokenFile)
	return runBrowserServer(ctx, ln, scr, token, stderr)
}

// runBrowserServer serves until ctx ends or the browser closes; закрытый
// браузер — код 1: отвечать 502 вечно незачем, вкладка подскажет запустить заново.
func runBrowserServer(ctx context.Context, ln net.Listener, scr browserScreen, token string, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	closed := make(chan struct{})
	if d, ok := scr.(interface{ Done() <-chan struct{} }); ok {
		go func() {
			select {
			case <-d.Done():
				fmt.Fprintln(stderr, "браузер закрыт: serve завершается, запустите его снова")
				close(closed)
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	h := cdpbrowser.Handler(scr, cdpbrowser.Config{Token: token, Width: cdpbrowser.DefaultWidth, Height: cdpbrowser.DefaultHeight, Wait: 25 * time.Second})
	if err := serveUntil(ctx, ln, h); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	select {
	case <-closed:
		return 1
	default:
		return 0
	}
}

// listenBrowserSocket opens the unix socket: папка 0700 и только ваша, файл
// 0600; сокет от убитого прошлого запуска заменяется, живой — отказ.
func listenBrowserSocket(path string) (net.Listener, error) {
	if len(path) >= 104 { // sockaddr_un: на macOS 104 байта, bind молча отвечает «invalid argument»
		return nil, fmt.Errorf("%w (%d)", errBrowserSockPath, len(path))
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: %s", errBrowserSocketDir, dir)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%w (%s)", errBrowserNotSocket, path)
		}
		if c, err := net.DialTimeout("unix", path, 500*time.Millisecond); err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("%w (%s)", errBrowserRunning, path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// serveUntil serves h on ln until ctx ends; остановка по ctx — не ошибка.
func serveUntil(ctx context.Context, ln net.Listener, h http.Handler) error {
	// ponytail: ограничений на число одновременных /frame нет (каждый держится до 25 с);
	// за токеном и в папке 0700 это только свой процесс. Потолок — зависший клиент;
	// путь — лимит соединений и WriteTimeout на /input.
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type browserFlags struct {
	socket, tokenFile, profile, chrome string
	headless                           bool
}

// parseBrowserFlags reads the flags; ненулевой код — выход.
func parseBrowserFlags(args []string, stderr io.Writer) (browserFlags, int) {
	home, _ := os.UserHomeDir()
	cfgDir, _ := os.UserConfigDir()
	var o browserFlags
	fs := flag.NewFlagSet("browser serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.socket, "socket", filepath.Join(home, ".kb-tokens", "browser.sock"), "unix socket to listen on (its folder must be 0700)")
	fs.StringVar(&o.tokenFile, "token-file", filepath.Join(home, ".kb-tokens", "browser"), "token file (mode 0600); created with a random token if missing")
	fs.StringVar(&o.profile, "profile", filepath.Join(cfgDir, "kb-browser", "profile"), "browser profile folder (separate from your everyday browser)")
	fs.StringVar(&o.chrome, "chrome", "", "path to a Chromium-based browser; empty means find Chrome, Brave, Edge or Chromium")
	window := fs.Bool("window", false, "show the browser's own window too (by default the picture is only in the DVK tab)")
	if err := fs.Parse(args); err != nil {
		return o, 2
	}
	o.headless = !*window
	return o, 0
}

// ensureBrowserToken reads the token or, when the file is missing, makes one:
// у браузера, который запускается без настройки, токен тоже должен быть.
// Файл — 0600, права и размер проверяет readTokenFile (как у serve).
func ensureBrowserToken(path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return readTokenFile(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		return "", err
	}
	return token, f.Close()
}
