package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
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
	f, err := os.CreateTemp(d.tmp, "dvk-update-*")
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

func watchUpdates(context.Context, *updateDesk, <-chan time.Time, func(updateState)) {}

func saveForManual(string, string, string) (string, error) { return "", nil }

func (a *App) UpdateState() updateState { return updateState{} }
func (a *App) UpdateCheck() updateState { return updateState{} }
func (a *App) UpdateInstall() error     { return nil }
