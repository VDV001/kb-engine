package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/selfupdate"
)

// fakeRelease is GitHub Releases on disk-free memory.
type fakeRelease map[string][]byte

func (f fakeRelease) Fetch(_ context.Context, path string, limit int64) ([]byte, error) {
	b, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("404 %s", path)
	}
	if int64(len(b)) > limit {
		return nil, errors.New("too large")
	}
	return b, nil
}

func (f fakeRelease) Open(ctx context.Context, path string, limit int64) (io.ReadCloser, error) {
	b, err := f.Fetch(ctx, path, limit)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func signedRelease(t *testing.T, version string, body []byte) (ed25519.PublicKey, fakeRelease) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	sum := sha256.Sum256(body)
	m := fmt.Appendf(nil, `{"version":%q,"assets":[{"platform":"darwin-arm64","name":"DVK-mac-arm64.zip","sha256":%q,"size":%d},{"platform":"windows-amd64","name":"DVK-Setup.exe","sha256":%q,"size":%d}]}`,
		version, hex.EncodeToString(sum[:]), len(body), hex.EncodeToString(sum[:]), len(body))
	return pub, fakeRelease{
		"download/dvk-latest/dvk-manifest.json":      m,
		"download/dvk-latest/dvk-manifest.json.sig":  selfupdate.SignManifest(priv, m),
		"download/" + version + "/DVK-mac-arm64.zip": body,
		"download/" + version + "/DVK-Setup.exe":     body,
	}
}

// Окно спрашивает выпуск, показывает «Обновить» и ставит только по нажатию:
// файл качается во временный, проверенный — отдается установке (#651).
func TestUpdateDeskOffersAndInstalls(t *testing.T) {
	body := []byte("new build")
	pub, src := signedRelease(t, "v0.41.0", body)
	var applied string
	d := newUpdateDesk("v0.40.0-3-gabcdef1", src, pub, "darwin-arm64", func(_ context.Context, path, v string) error {
		b, _ := os.ReadFile(path)
		applied = v + ":" + string(b)
		return nil
	})
	if st := d.State(); st.Available || st.Dev {
		t.Fatalf("before the check %+v", st)
	}
	d.check(context.Background())
	st := d.State()
	if !st.Available || st.Version != "v0.41.0" || st.Current != "v0.40.0" || st.Error != "" {
		t.Fatalf("after the check %+v", st)
	}
	if applied != "" {
		t.Fatal("installed without the person's click")
	}
	if err := d.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if applied != "v0.41.0:new build" {
		t.Errorf("applied %q", applied)
	}
}

// Подмененная сборка не доходит до установки, временный файл убран.
func TestUpdateDeskRefusesSubstitutedBuild(t *testing.T) {
	pub, src := signedRelease(t, "v0.41.0", []byte("new build"))
	src["download/v0.41.0/DVK-Setup.exe"] = []byte("evil build")
	var applied bool
	d := newUpdateDesk("v0.40.0", src, pub, "windows-amd64", func(context.Context, string, string) error { applied = true; return nil })
	d.tmp = t.TempDir()
	d.check(context.Background())
	if err := d.Install(context.Background()); err == nil || applied {
		t.Fatalf("substituted build: %v, applied %v", err, applied)
	}
	if left, _ := os.ReadDir(d.tmp); len(left) != 0 {
		t.Errorf("temp files left: %d", len(left))
	}
}

// Сборка разработчика (без тега) обновления не ищет; без предложения — нечего ставить.
func TestUpdateDeskDevAndNothing(t *testing.T) {
	pub, src := signedRelease(t, "v0.41.0", []byte("x"))
	dev := newUpdateDesk("dev", src, pub, "darwin-arm64", nil)
	dev.check(context.Background())
	if st := dev.State(); !st.Dev || st.Available {
		t.Errorf("dev %+v", st)
	}
	same := newUpdateDesk("v0.41.0", src, pub, "darwin-arm64", nil)
	same.check(context.Background())
	if st := same.State(); st.Available || st.Error != "" {
		t.Errorf("same version %+v", st)
	}
	if err := same.Install(context.Background()); err == nil {
		t.Error("install without an offer")
	}
}

// Вшитый ключ — ключ владельца из Связки (17.11): 32 байта.
func TestReleaseKeyEmbedded(t *testing.T) {
	if k := releasePublicKey(); len(k) != ed25519.PublicKeySize {
		t.Errorf("key %d bytes", len(k))
	}
}

func recvState(t *testing.T, c <-chan updateState) updateState {
	t.Helper()
	select {
	case s := <-c:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("the page was not told")
		return updateState{}
	}
}

// Окно проверяет выпуск при запуске и по сроку, и каждый итог отдает странице;
// закрытие окна останавливает проверку (#651).
func TestWatchUpdates(t *testing.T) {
	pub, src := signedRelease(t, "v0.41.0", []byte("x"))
	d := newUpdateDesk("v0.40.0", src, pub, "darwin-arm64", nil)
	tick := make(chan time.Time)
	emitted := make(chan updateState)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { watchUpdates(ctx, d, tick, func(s updateState) { emitted <- s }); close(done) }()
	if st := recvState(t, emitted); !st.Available || st.Version != "v0.41.0" {
		t.Fatalf("first check %+v", st)
	}
	delete(src, "download/dvk-latest/dvk-manifest.json") // выпуск пропал: ошибка — словами, окно живет
	tick <- time.Now()
	if st := recvState(t, emitted); st.Error == "" {
		t.Errorf("second check %+v — the failure is shown in words", st)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the watch outlived the window")
	}
}

// Пока установка не научилась ставить сама, проверенная сборка кладется в
// «Загрузки» под понятным именем (#651).
func TestSaveForManualInstall(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "dvk-1.zip")
	if err := os.WriteFile(src, []byte("build"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := saveForManual(home, src, "v0.41.0")
	if err != nil || filepath.Dir(got) != filepath.Join(home, "Downloads") || filepath.Base(got) != "DVK-v0.41.0.zip" {
		t.Fatalf("saved %q, %v", got, err)
	}
	if b, _ := os.ReadFile(got); string(b) != "build" {
		t.Errorf("content %q", b)
	}
}

// Страница берет состояние и ставит по нажатию через App.
func TestAppUpdateMethods(t *testing.T) {
	pub, src := signedRelease(t, "v0.41.0", []byte("x"))
	var applied bool
	d := newUpdateDesk("v0.40.0", src, pub, "darwin-arm64", func(context.Context, string, string) error { applied = true; return nil })
	a := &App{update: d, ctx: context.Background()}
	if st := a.UpdateCheck(); !st.Available {
		t.Fatalf("%+v", st)
	}
	if err := a.UpdateInstall(); err != nil || !applied {
		t.Errorf("install %v, applied %v", err, applied)
	}
	if st := a.UpdateState(); st.Current != "v0.40.0" {
		t.Errorf("%+v", st)
	}
}
