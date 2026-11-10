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
	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

const browserUsage = `usage: kbengine browser serve [--addr 127.0.0.1:8790] [--token-file ~/.kb-tokens/browser] [--profile DIR] [--chrome PATH] [--headless]
       Браузер для вкладки «Браузер» окна DVK: Chrome, Brave, Edge или Chromium на этом Mac,
       картинка страницы и ввод — за токеном, только на 127.0.0.1.`

// Размер экрана вкладки. Одно число на сервер: окно масштабирует картинку.
const (
	browserWidth  = 1280
	browserHeight = 800
)

// errBrowserNotLoopback: через CDP браузер отдается целиком (входы в сайты,
// cookie), поэтому наружу его не открывают ни флагом, ни токеном.
var errBrowserNotLoopback = errors.New("адрес — только 127.0.0.1, ::1 или localhost: браузер с вашими входами в сеть не открывается")

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
	scr, err := launch(ctx, cdpbrowser.Options{Path: o.chrome, Profile: o.profile, Width: browserWidth, Height: browserHeight, Headless: o.headless})
	if err != nil {
		fmt.Fprintln(stderr, "браузер:", err)
		return 1
	}
	defer func() { _ = scr.Close() }()
	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "kbengine browser: слушает http://%s, токен в %s\n", ln.Addr(), o.tokenFile)
	if err := serveUntil(ctx, ln, cdpbrowser.Handler(scr, cdpbrowser.Config{Token: token, Width: browserWidth, Height: browserHeight, Wait: 25 * time.Second})); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// serveUntil serves h on ln until ctx ends; остановка по ctx — не ошибка.
func serveUntil(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
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
	addr, tokenFile, profile, chrome string
	headless                         bool
}

// parseBrowserFlags reads the flags and refuses a non-loopback address; ненулевой код — выход.
func parseBrowserFlags(args []string, stderr io.Writer) (browserFlags, int) {
	home, _ := os.UserHomeDir()
	cfgDir, _ := os.UserConfigDir()
	var o browserFlags
	fs := flag.NewFlagSet("browser serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8790", "address to listen on (loopback only)")
	fs.StringVar(&o.tokenFile, "token-file", filepath.Join(home, ".kb-tokens", "browser"), "token file (mode 0600); created with a random token if missing")
	fs.StringVar(&o.profile, "profile", filepath.Join(cfgDir, "kb-browser", "profile"), "browser profile folder (separate from your everyday browser)")
	fs.StringVar(&o.chrome, "chrome", "", "path to a Chromium-based browser; empty means find Chrome, Brave, Edge or Chromium")
	fs.BoolVar(&o.headless, "headless", false, "no browser window on screen (checks and servers)")
	if err := fs.Parse(args); err != nil {
		return o, 2
	}
	host, _, err := net.SplitHostPort(o.addr)
	if err != nil {
		fmt.Fprintf(stderr, "%v: %q\n", errBadAddr, o.addr)
		return o, 2
	}
	if !loopback.Host(host) {
		fmt.Fprintln(stderr, errBrowserNotLoopback)
		return o, 2
	}
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
