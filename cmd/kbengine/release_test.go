package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/selfupdate"
)

// fakeKeys is the Связка in memory: настоящую тест не трогает.
type fakeKeys map[string]string

func (k fakeKeys) Get(account string) (string, error) {
	if v, ok := k[account]; ok {
		return v, nil
	}
	return "", keychain.ErrNotFound
}

func (k fakeKeys) Set(account, secret string) error { k[account] = secret; return nil }

func withKeys(t *testing.T, k fakeKeys) {
	t.Helper()
	prev := releaseKeys
	releaseKeys = func() releaseKeyStore { return k }
	t.Cleanup(func() { releaseKeys = prev })
}

func TestReleaseKeyCreatesOnce(t *testing.T) {
	keys := fakeKeys{}
	withKeys(t, keys)
	var out, errb bytes.Buffer
	if code := run([]string{"release", "key"}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	pub, err := hex.DecodeString(strings.TrimSpace(out.String()))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("printed %q, want the public key in hex", out.String())
	}
	seed := keys[releaseKeyAccount]
	if seed == "" || strings.Contains(out.String()+errb.String(), seed) {
		t.Fatal("the private key is not in the store or was printed")
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"release", "key"}, &out, &errb); code == 0 || keys[releaseKeyAccount] != seed {
		t.Errorf("second key: code %d, replaced %v", code, keys[releaseKeyAccount] != seed)
	}
}

// Подписанный выпуск проходит ту же проверку, что делает окно.
func TestReleaseSignPassesTheWindowsCheck(t *testing.T) {
	keys := fakeKeys{}
	withKeys(t, keys)
	var keyOut bytes.Buffer
	if code := run([]string{"release", "key"}, &keyOut, &bytes.Buffer{}); code != 0 {
		t.Fatal("key")
	}
	pub, _ := hex.DecodeString(strings.TrimSpace(keyOut.String()))
	dir := t.TempDir()
	mac, win := filepath.Join(dir, "DVK-mac-arm64.zip"), filepath.Join(dir, "DVK-Setup.exe")
	_ = os.WriteFile(mac, []byte("mac zip"), 0o600)
	_ = os.WriteFile(win, []byte("windows setup"), 0o600)
	out := filepath.Join(dir, "out")
	var stdout, errb bytes.Buffer
	code := run([]string{"release", "sign", "--version", "v0.41.0", "--out", out, "darwin-arm64=" + mac, "windows-amd64=" + win}, &stdout, &errb)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if strings.TrimSpace(stdout.String()) != hex.EncodeToString(pub) {
		t.Errorf("sign printed %q, want the public key", stdout.String())
	}
	src := dirReleases{"download/dvk-latest/dvk-manifest.json": filepath.Join(out, "dvk-manifest.json"),
		"download/dvk-latest/dvk-manifest.json.sig": filepath.Join(out, "dvk-manifest.json.sig"),
		"download/v0.41.0/DVK-Setup.exe":            win}
	cur, _ := domain.NewReleaseVersion("v0.40.0")
	u := selfupdate.Updater{Source: src, PublicKey: pub, Current: cur, Platform: "windows-amd64"}
	offer, err := u.Check(t.Context())
	if err != nil || !offer.Available() {
		t.Fatalf("window check: %v %v", offer.Available(), err)
	}
	var b bytes.Buffer
	if err := u.Download(t.Context(), offer, &b); err != nil || b.String() != "windows setup" {
		t.Errorf("download: %q %v", b.String(), err)
	}
}

func TestReleaseKeyErrors(t *testing.T) {
	withKeys(t, fakeKeys{})
	if _, err := releasePrivateKey(); !errors.Is(err, errNoReleaseKey) {
		t.Errorf("empty store: %v", err)
	}
	withKeys(t, fakeKeys{releaseKeyAccount: "zz"})
	if _, err := releasePrivateKey(); !errors.Is(err, errBadReleaseKey) {
		t.Errorf("broken key: %v", err)
	}
}

// Повторный sign в ту же папку не оставляет новый манифест со старой подписью.
func TestReleaseSignReplacesBoth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dvk-manifest.json.sig"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dvk-manifest.json.sig.new"), 0o755); err != nil { // запись подписи упадет
		t.Fatal(err)
	}
	if err := writeRelease(dir, []byte("manifest"), []byte("sig")); err == nil {
		t.Fatal("write over a blocked signature succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "dvk-manifest.json")); err == nil {
		t.Error("manifest written although its signature was not")
	}
}

func TestReleaseSignRefusals(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.zip")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	cases := map[string][]string{
		"no key in the store": {"--version", "v0.41.0", "--out", dir, "darwin-arm64=" + f},
		"describe version":    {"--version", "v0.41.0-1-gabc", "--out", dir, "darwin-arm64=" + f},
		"no files":            {"--version", "v0.41.0", "--out", dir},
		"pair without =":      {"--version", "v0.41.0", "--out", dir, f},
		"missing file":        {"--version", "v0.41.0", "--out", dir, "darwin-arm64=" + f + ".nope"},
		"unknown platform":    {"--version", "v0.41.0", "--out", dir, "linux-amd64=" + f},
		"no --out":            {"--version", "v0.41.0", "darwin-arm64=" + f},
	}
	for name, args := range cases {
		withKeys(t, fakeKeys{})
		var errb bytes.Buffer
		if code := run(append([]string{"release", "sign"}, args...), &bytes.Buffer{}, &errb); code == 0 {
			t.Errorf("%s: accepted", name)
		}
		if _, err := os.Stat(filepath.Join(dir, "dvk-manifest.json")); err == nil {
			t.Fatalf("%s: manifest written", name)
		}
	}
}

// dirReleases is a release on disk: путь выпуска → файл.
type dirReleases map[string]string

func (d dirReleases) Open(ctx context.Context, path string, limit int64) (io.ReadCloser, error) {
	b, err := d.Fetch(ctx, path, limit)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (d dirReleases) Fetch(_ context.Context, path string, limit int64) ([]byte, error) {
	b, err := os.ReadFile(d[path])
	if err == nil && int64(len(b)) > limit {
		return nil, os.ErrInvalid
	}
	return b, err
}

func TestNoKeyStoreRefusesInWords(t *testing.T) {
	var errb bytes.Buffer
	withKeys(t, nil)
	releaseKeys = func() releaseKeyStore { return noKeyStore{} }
	if code := run([]string{"release", "key"}, &bytes.Buffer{}, &errb); code == 0 || !strings.Contains(errb.String(), "macOS") {
		t.Errorf("code %d: %s", code, errb.String())
	}
}

// release renew продлевает срок манифеста без пересборки: тот же выпуск и
// файлы, новая подпись, окно верит ему (ревью #679, п.6).
func TestReleaseRenew(t *testing.T) {
	withKeys(t, fakeKeys{})
	var keyOut bytes.Buffer
	if code := run([]string{"release", "key"}, &keyOut, &bytes.Buffer{}); code != 0 {
		t.Fatal("key")
	}
	pub, _ := hex.DecodeString(strings.TrimSpace(keyOut.String()))
	dir := t.TempDir()
	win := filepath.Join(dir, "DVK-Setup.exe")
	_ = os.WriteFile(win, []byte("windows setup"), 0o600)
	out := filepath.Join(dir, "out")
	var errb bytes.Buffer
	if code := run([]string{"release", "sign", "--version", "v0.41.0", "--valid-days", "1", "--out", out, "windows-amd64=" + win}, &bytes.Buffer{}, &errb); code != 0 {
		t.Fatalf("sign %d: %s", code, errb.String())
	}
	cur, _ := domain.NewReleaseVersion("v0.40.0")
	src := dirReleases{"download/dvk-latest/dvk-manifest.json": filepath.Join(out, "dvk-manifest.json"),
		"download/dvk-latest/dvk-manifest.json.sig": filepath.Join(out, "dvk-manifest.json.sig"),
		"download/v0.41.0/DVK-Setup.exe":            win}
	u := selfupdate.Updater{Source: src, PublicKey: pub, Current: cur, Platform: "windows-amd64",
		Now: func() time.Time { return time.Now().AddDate(0, 0, 30) }}
	if _, err := u.Check(t.Context()); !errors.Is(err, domain.ErrReleaseExpired) {
		t.Fatalf("a day-long manifest at day 30: %v", err)
	}
	if code := run([]string{"release", "renew", "--dir", out, "--valid-days", "90"}, &bytes.Buffer{}, &errb); code != 0 {
		t.Fatalf("renew %d: %s", code, errb.String())
	}
	if o, err := u.Check(t.Context()); err != nil || !o.Available() || o.Version().String() != "v0.41.0" {
		t.Errorf("after renew: %v %v", o.Available(), err)
	}
	if code := run([]string{"release", "renew", "--dir", t.TempDir()}, &bytes.Buffer{}, &bytes.Buffer{}); code == 0 {
		t.Error("renew without a manifest accepted")
	}
	if code := run([]string{"release", "sign", "--version", "v0.41.0", "--valid-days", "9999", "--out", out, "windows-amd64=" + win}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Errorf("an absurd validity accepted: %d", code)
	}
}
