package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodToken = "k3Jx9_secret-token-0123456789abcdefXYZ"

func writeToken(t *testing.T, body string, perm os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, perm); err != nil {
		t.Fatal(err)
	}
	return p
}

// Токен лежит в файле, а не во флаге: значение флага видно любому в ps. Файл,
// который читают группа или все, — отказ: токен уже нельзя считать секретом.
func TestReadTokenFile(t *testing.T) {
	tests := []struct {
		name string
		body string
		perm os.FileMode
		want error
	}{
		{"годный, перевод строки в конце", goodToken + "\n", 0o600, nil},
		{"только чтение владельцу", goodToken, 0o400, nil},
		{"читают все", goodToken, 0o644, errTokenPerms},
		{"читает группа", goodToken, 0o640, errTokenPerms},
		{"короче 32", "short-token-123", 0o600, errTokenWeak},
		{"пустой", "\n", 0o600, errTokenWeak},
		{"пробел внутри", "abcdefghijklmnop qrstuvwxyz0123456789", 0o600, errTokenWeak},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readTokenFile(writeToken(t, tt.body, tt.perm))
			if tt.want == nil {
				if err != nil || got != goodToken {
					t.Fatalf("got %q, %v", got, err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if _, err := readTokenFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("нет файла — ждали ошибку")
	}
}

// Адрес не на этом Mac требует и токен, и TLS: без токена базу прочтет любой
// в сети, без TLS токен уйдет по сети открытым текстом. За обратным прокси с
// TLS сервер слушает loopback, и правило его не касается.
func TestCheckExposure(t *testing.T) {
	tests := []struct {
		addr       string
		token, tls bool
		open       bool
		want       error
	}{
		{"127.0.0.1:8080", false, false, false, nil},
		{"localhost:8080", false, false, false, nil},
		{"[::1]:8080", false, false, false, nil},
		{"127.0.0.1:8080", true, false, false, nil},
		{":8080", false, false, false, errExposedNoToken},
		{"0.0.0.0:8443", false, true, false, errExposedNoToken},
		{"0.0.0.0:8443", true, false, false, errExposedNoTLS},
		{"10.8.0.1:8443", true, false, false, errExposedNoTLS},
		{"kb.example.ru:443", true, false, false, errExposedNoTLS},
		{"0.0.0.0:8443", true, true, false, nil},
		{"10.8.0.1:8443", true, true, false, nil},
		{"no-port", true, true, false, errBadAddr},
		// Контейнер слушает все адреса, а наружу его выпускает платформа
		// (ingress с TLS, docker -p 127.0.0.1): это явный выбор --allow-open.
		{":8080", false, false, true, nil},
		{"no-port", false, false, true, errBadAddr},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			err := checkExposure(tt.addr, tt.token, tt.tls, tt.open)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("checkExposure(%q, token=%v, tls=%v) = %v, want %v", tt.addr, tt.token, tt.tls, err, tt.want)
			}
		})
	}
}

// Отказы видны при запуске, до того как порт открыт.
func TestRunServe_RefusesUnsafeExposure(t *testing.T) {
	catalog := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(catalog, []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tok := writeToken(t, goodToken, 0o600)
	cert := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(cert, []byte("-----BEGIN CERTIFICATE-----\nnot a cert\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"сеть без токена", []string{"--addr", "0.0.0.0:0"}, "--token-file"},
		{"сеть без TLS", []string{"--addr", "0.0.0.0:0", "--token-file", tok}, "--tls-cert"},
		{"сертификат без ключа", []string{"--token-file", tok, "--tls-cert", cert}, "--tls-key"},
		{"негодный файл токена", []string{"--token-file", writeToken(t, goodToken, 0o644)}, "0600"},
		// Битый сертификат — отказ при запуске, а не после строки «serving».
		{"битый сертификат", []string{"--addr", "127.0.0.1:0", "--token-file", tok, "--tls-cert", cert, "--tls-key", cert}, "--tls-cert"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := runServe(append([]string{"--catalog", catalog}, tt.args...), &out, &errb)
			if code != 2 || !strings.Contains(errb.String(), tt.want) {
				t.Fatalf("code = %d, stderr = %q, want mention of %q", code, errb.String(), tt.want)
			}
		})
	}
}
