package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

// Отказы при запуске serve: печатаются и останавливают процесс до того, как
// открыт порт.
var (
	errTokenPerms     = errors.New("файл токена читают не только владелец: нужны права 0600")
	errTokenOwner     = errors.New("файл токена принадлежит другому пользователю")
	errTokenWeak      = errors.New("токен короче 32 символов или с пробелами")
	errExposedNoToken = errors.New("адрес открыт в сеть без токена: нужен --token-file")
	errExposedNoTLS   = errors.New("адрес открыт в сеть без TLS: токен ушел бы открытым текстом, нужны --tls-cert и --tls-key (или слушать 127.0.0.1 за прокси с TLS)")
	errBadAddr        = errors.New("адрес вида host:port")
)

// minTokenLen — нижняя граница длины токена: 32 символа из openssl rand
// -base64 24 — это 192 бита, перебором не взять.
const minTokenLen = 32

// readTokenFile reads the bearer token. Токен в файле, а не во флаге: значение
// флага видно любому в ps. Файл, который читают группа или все, — отказ.
func readTokenFile(path string) (string, error) {
	// Права проверяются у открытого файла, а не по пути: между проверкой пути
	// и чтением его можно подменить (TOCTOU).
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if err := tokenFileMode(info); err != nil {
		return "", fmt.Errorf("%w (%s)", err, path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < minTokenLen || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("%w (%s)", errTokenWeak, path)
	}
	return token, nil
}

// checkExposure refuses an address reachable from the network unless both a
// token and TLS are set. Loopback (127.0.0.1, ::1, localhost) — без условий:
// так serve работает по умолчанию и за обратным прокси, который сам держит TLS.
// open — явный выбор --allow-open: контейнер слушает все адреса, а наружу его
// выпускает платформа (ingress с TLS и доступом, docker -p 127.0.0.1:…).
func checkExposure(addr string, token, tls, open bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: %q", errBadAddr, addr)
	}
	if open || loopback.Host(host) {
		return nil
	}
	if !token {
		return errExposedNoToken
	}
	if !tls {
		return errExposedNoTLS
	}
	return nil
}

// serveAccess reads the token and the TLS pair and checks the address against
// them — все отказы до того, как открыт порт. Пара грузится здесь, а не в
// ListenAndServeTLS: иначе битый сертификат всплывал бы после строки «serving».
func serveAccess(addr, pprofAddr, tokenFile, certFile, keyFile string, allowOpen bool) (string, *tls.Config, error) {
	// Профилировщик отдает дампы памяти — в них и токен, и данные базы. Он
	// идет мимо токена и TLS, поэтому только на этом Mac, при любом --allow-open.
	if pprofAddr != "" {
		if host, _, err := net.SplitHostPort(pprofAddr); err != nil || !loopback.Host(host) {
			return "", nil, fmt.Errorf("--pprof %s: профилировщик только на этом Mac (127.0.0.1:порт)", pprofAddr)
		}
	}
	if (certFile == "") != (keyFile == "") {
		return "", nil, errors.New("--tls-cert and --tls-key go together")
	}
	var token string
	if tokenFile != "" {
		t, err := readTokenFile(tokenFile)
		if err != nil {
			return "", nil, fmt.Errorf("--token-file: %w", err)
		}
		token = t
	}
	if err := checkExposure(addr, token != "", certFile != "", allowOpen); err != nil {
		return "", nil, fmt.Errorf("--addr %s: %w", addr, err)
	}
	if certFile == "" {
		return token, nil, nil
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return "", nil, fmt.Errorf("--tls-cert/--tls-key: %w", err)
	}
	return token, &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

// newServeServer builds the HTTP server serve listens with. С токеном обработчик
// оборачивается проверкой Bearer здесь, в одном месте, которое проверяет тест:
// токен не может быть прочитан и забыт по дороге к обработчику.
func newServeServer(addr string, handler http.Handler, token string, tlsCfg *tls.Config) *http.Server {
	if token != "" {
		handler = httpapi.RequireBearer(token, handler)
	}
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsCfg,
	}
}

// listenAndServe serves HTTPS when the server carries a TLS config (the pair is
// already loaded there), plain HTTP otherwise.
func listenAndServe(srv *http.Server) error {
	if srv.TLSConfig != nil {
		return srv.ListenAndServeTLS("", "")
	}
	return srv.ListenAndServe()
}
