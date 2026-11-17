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
	m := fmt.Appendf(nil, `{"version":%q,"expires":%q,"assets":[{"platform":"darwin-arm64","name":"DVK-mac-arm64.zip","sha256":%q,"size":%d},{"platform":"windows-amd64","name":"DVK-Setup.exe","sha256":%q,"size":%d}]}`,
		version, time.Now().AddDate(0, 0, 60).UTC().Format(time.RFC3339), hex.EncodeToString(sum[:]), len(body), hex.EncodeToString(sum[:]), len(body))
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

// Сбой проверки не стирает последнее проверенное предложение: плашка не
// мигает от одного недоступного GitHub (ревью #679, п.1).
func TestUpdateDeskKeepsOfferOnCheckFailure(t *testing.T) {
	pub, src := signedRelease(t, "v0.41.0", []byte("x"))
	d := newUpdateDesk("v0.40.0", src, pub, "darwin-arm64", func(context.Context, string, string) error { return nil })
	d.check(context.Background())
	delete(src, "download/dvk-latest/dvk-manifest.json")
	d.check(context.Background())
	st := d.State()
	if !st.Available || st.Version != "v0.41.0" || st.Error == "" {
		t.Errorf("after a failed check %+v", st)
	}
	if err := d.Install(context.Background()); err != nil {
		t.Errorf("the checked offer stays installable: %v", err)
	}
}

// Подпись не сошлась или нет сборки под платформу — человек это видит, а не
// «обновлений нет» (ревью #679, п.2).
func TestUpdateDeskAlertsOnBadRelease(t *testing.T) {
	pub, src := signedRelease(t, "v0.41.0", []byte("x"))
	src["download/dvk-latest/dvk-manifest.json.sig"] = make([]byte, ed25519.SignatureSize)
	d := newUpdateDesk("v0.40.0", src, pub, "darwin-arm64", nil)
	d.check(context.Background())
	if st := d.State(); !st.Alert || st.Available {
		t.Errorf("bad signature %+v", st)
	}
	pub, src = signedRelease(t, "v0.41.0", []byte("x"))
	d = newUpdateDesk("v0.40.0", src, pub, "linux-arm64", nil)
	d.check(context.Background())
	if st := d.State(); !st.Alert {
		t.Errorf("no build for the platform %+v", st)
	}
	delete(src, "download/dvk-latest/dvk-manifest.json")
	d = newUpdateDesk("v0.40.0", src, pub, "darwin-arm64", nil)
	d.check(context.Background())
	if st := d.State(); st.Alert || st.Error == "" {
		t.Errorf("a network failure is not an alert %+v", st)
	}
}

// После установки кнопка не предлагает то же снова (ревью #679, п.5).
func TestUpdateDeskOfferIsSpentAfterInstall(t *testing.T) {
	pub, src := signedRelease(t, "v0.41.0", []byte("x"))
	d := newUpdateDesk("v0.40.0", src, pub, "darwin-arm64", func(context.Context, string, string) error { return nil })
	d.check(context.Background())
	if err := d.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := d.State(); st.Available {
		t.Errorf("still offered %+v", st)
	}
}

// Сохранение не идет по симлинку на итоговом имени и не оставляет обрезок
// (ревью #679, п.3, 4).
func TestSaveForManualIsSafe(t *testing.T) {
	home := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	dl := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dl, "DVK-v0.41.0.zip")); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "dvk.zip")
	if err := os.WriteFile(src, []byte("build"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveForManual(home, src, "v0.41.0"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "mine" {
		t.Errorf("the symlink target was overwritten: %q", b)
	}
	if _, err := saveForManual(home, filepath.Join(t.TempDir(), "none.zip"), "v0.42.0"); err == nil {
		t.Error("missing source accepted")
	}
	if left, _ := filepath.Glob(filepath.Join(dl, "*v0.42.0*")); len(left) != 0 {
		t.Errorf("a stub left behind: %v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(dl, ".dvk-*")); len(left) != 0 {
		t.Errorf("a temp file left behind: %v", left)
	}
}

// Окно без обновления (не собранное так) не падает на вызове страницы (ревью #679, п.8).
func TestAppUpdateMethodsWithoutDesk(t *testing.T) {
	a := &App{ctx: context.Background()}
	if st := a.UpdateState(); st.Available {
		t.Errorf("%+v", st)
	}
	if st := a.UpdateCheck(); st.Available {
		t.Errorf("%+v", st)
	}
	if err := a.UpdateInstall(); err == nil {
		t.Error("install without a desk")
	}
}

// Просроченный манифест показывается человеку как не прошедшая проверка
// (владельцу пора продлить подпись), а не молчит (ревью #679, п.6).
func TestUpdateDeskAlertsOnExpiredManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	sum := sha256.Sum256([]byte("x"))
	m := fmt.Appendf(nil, `{"version":"v0.41.0","expires":"2020-01-01T00:00:00Z","assets":[{"platform":"darwin-arm64","name":"DVK-mac-arm64.zip","sha256":%q,"size":1}]}`, hex.EncodeToString(sum[:]))
	src := fakeRelease{"download/dvk-latest/dvk-manifest.json": m, "download/dvk-latest/dvk-manifest.json.sig": selfupdate.SignManifest(priv, m)}
	d := newUpdateDesk("v0.40.0", src, pub, "darwin-arm64", nil)
	d.check(context.Background())
	if st := d.State(); !st.Alert || st.Available {
		t.Errorf("%+v", st)
	}
}
