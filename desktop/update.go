package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/selfupdate"
)

// releaseKeyHex — открытый ключ подписи выпусков владельца (Связка «DVK release
// signing», создан 17.11): окно ставит только то, что подписано им.
const releaseKeyHex = "0e361ef641364868633f856399199b6ffb8cf4cad47b75f1c9b85b6ebe6902ba" // gitleaks:allow — открытый ключ ed25519, не секрет

func releasePublicKey() ed25519.PublicKey {
	k, err := hex.DecodeString(releaseKeyHex)
	if err != nil || len(k) != ed25519.PublicKeySize {
		return nil // окно без ключа не верит ничему (selfupdate.Check)
	}
	return k
}

// updateState is what the window shows about updates.
type updateState struct {
	Current   string `json:"current"`
	Version   string `json:"version"`   // предложенный выпуск
	Available bool   `json:"available"` // есть новее — «Обновить»
	Dev       bool   `json:"dev"`       // сборка без тега: не ищем
	Busy      bool   `json:"busy"`      // качается и ставится
	Saved     string `json:"saved"`     // куда положена проверенная сборка
	Alert     bool   `json:"alert"`     // подпись не сошлась или нет сборки под платформу — человеку это видно
	Error     string `json:"error"`
}

// updateDesk checks for a newer window and installs it on the person's click (#651):
// проверка не ставит ничего, установка — только предложения, прошедшего подпись.
type updateDesk struct {
	u     *selfupdate.Updater // nil — сборка без тега
	apply func(ctx context.Context, path, version string) error
	tmp   string // папка временных файлов сборки ("" — системная)

	mu    sync.Mutex
	offer selfupdate.Offer
	st    updateState
}

func newUpdateDesk(build string, src selfupdate.Source, pub ed25519.PublicKey, platform string, apply func(context.Context, string, string) error) *updateDesk {
	d := &updateDesk{apply: apply, tmp: os.TempDir()}
	v, err := domain.NewReleaseVersion(build)
	if err != nil {
		d.st = updateState{Current: build, Dev: true}
		return d
	}
	d.u = &selfupdate.Updater{Source: src, PublicKey: pub, Current: v, Platform: platform}
	d.st = updateState{Current: v.String()}
	return d
}

// check asks the release once; ошибка — словами в состоянии, окно работает дальше.
func (d *updateDesk) check(ctx context.Context) {
	if d.u == nil {
		return
	}
	o, err := d.u.Check(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.offer, d.st.Error = o, ""
	d.st.Available, d.st.Version = o.Available(), ""
	if o.Available() {
		d.st.Version = o.Version().String()
	}
	if err != nil {
		d.st.Error = err.Error()
	}
}

// State is the update state for the page.
func (d *updateDesk) State() updateState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st
}

var errNoUpdate = errors.New("нового выпуска нет — проверьте еще раз позже")

// Install downloads the offered build into a temp file — подпись и sha256
// проверены по дороге — and hands it to the platform's apply.
func (d *updateDesk) Install(ctx context.Context) error {
	d.mu.Lock()
	o := d.offer
	if d.u == nil || !o.Available() || d.st.Busy {
		d.mu.Unlock()
		return errNoUpdate
	}
	d.st.Busy = true
	d.mu.Unlock()
	defer func() { d.mu.Lock(); d.st.Busy = false; d.mu.Unlock() }()
	f, err := os.CreateTemp(d.tmp, "dvk-update-*"+filepath.Ext(o.AssetName()))
	if err != nil {
		return err
	}
	path := f.Name()
	err = d.u.Download(ctx, o, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && d.apply != nil {
		err = d.apply(ctx, path, o.Version().String())
	}
	_ = os.Remove(path) // установка уже взяла свое; подмененный файл — не нужен
	return err
}

// updateEvery is how often the window asks for a newer release.
const updateEvery = 24 * time.Hour

// watchUpdates checks at once and on every tick, and tells the page each
// outcome; ctx ends it with the window.
func watchUpdates(ctx context.Context, d *updateDesk, tick <-chan time.Time, emit func(updateState)) {
	for {
		d.check(ctx)
		emit(d.State())
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

// saveForManual puts the verified build into the person's Downloads as
// DVK-<версия><расширение>: до установки поверх окна (#651) ставит человек.
func saveForManual(home, path, version string) (string, error) {
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "DVK-"+version+filepath.Ext(path))
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return "", err
	}
	return dst, out.Close()
}

// noteSaved remembers where the build was put, for the page.
func (d *updateDesk) noteSaved(path string) {
	d.mu.Lock()
	d.st.Saved = path
	d.mu.Unlock()
}

// UpdateState is the update state for the page.
func (a *App) UpdateState() updateState { return a.update.State() }

// UpdateCheck asks the release now and returns the state.
func (a *App) UpdateCheck() updateState {
	a.update.check(a.ctx)
	return a.update.State()
}

// UpdateInstall installs the offered build by the person's click.
func (a *App) UpdateInstall() error { return a.update.Install(a.ctx) }
