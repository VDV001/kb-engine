package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	Build     string `json:"build"`     // сборка как есть: v0.41.2-1-g2f1aa3ef или dev
	Version   string `json:"version"`   // предложенный выпуск
	Available bool   `json:"available"` // есть новее — «Обновить»
	Dev       bool   `json:"dev"`       // сборка без тега: не ищем
	Busy      bool   `json:"busy"`      // качается и ставится
	Saved     string `json:"saved"`     // куда положена проверенная сборка
	Restart   bool   `json:"restart"`   // установка прошла, окно перезапускается
	Phase     string `json:"phase"`     // шаг установки: download, install, restart, error
	Done      int64  `json:"done"`      // скачано байт
	Total     int64  `json:"total"`     // размер сборки
	Alert     bool   `json:"alert"`     // подпись не сошлась или нет сборки под платформу — человеку это видно
	Error     string `json:"error"`
}

// updateDesk checks for a newer window and installs it on the person's click (#651):
// проверка не ставит ничего, установка — только предложения, прошедшего подпись.
type updateDesk struct {
	u     *selfupdate.Updater // nil — сборка без тега
	apply func(ctx context.Context, path, version string) error
	tmp   string // папка временных файлов сборки ("" — системная)

	emit  func(updateState) // шаги и ход скачивания — странице (nil — тихо)
	dir   string            // папка настроек: метка «обновилось»
	mu    sync.Mutex
	offer selfupdate.Offer
	st    updateState
}

func newUpdateDesk(build string, src selfupdate.Source, pub ed25519.PublicKey, platform string, apply func(context.Context, string, string) error) *updateDesk {
	d := &updateDesk{apply: apply, tmp: os.TempDir()}
	v, err := domain.NewReleaseVersion(build)
	if err != nil {
		d.st = updateState{Current: build, Build: build, Dev: true}
		return d
	}
	d.u = &selfupdate.Updater{Source: src, PublicKey: pub, Current: v, Platform: platform}
	d.st = updateState{Current: v.String(), Build: build}
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
	if err != nil { // сбой проверки не стирает прежнее проверенное предложение (ревью #679)
		d.st.Error = err.Error()
		d.st.Alert = errors.Is(err, domain.ErrReleaseSignature) || errors.Is(err, domain.ErrNoReleaseAsset) || errors.Is(err, domain.ErrReleaseExpired)
		return
	}
	d.offer, d.st.Error, d.st.Alert = o, "", false
	d.st.Available, d.st.Version = o.Available(), ""
	if o.Available() {
		d.st.Version = o.Version().String()
	}
}

// State is the update state for the page.
func (d *updateDesk) State() updateState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.st
}

var errNoUpdate = errors.New("нового выпуска нет — проверьте еще раз позже")

// update changes the state under the lock and tells the page (steps and the
// download's progress); emit — вне замка.
func (d *updateDesk) update(fn func(*updateState)) {
	d.mu.Lock()
	fn(&d.st)
	st, emit := d.st, d.emit
	d.mu.Unlock()
	if emit != nil {
		emit(st)
	}
}

// progressWriter counts the download and reports it at most every 100 ms.
type progressWriter struct {
	w      io.Writer
	done   int64
	last   time.Time
	report func(done int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if now := time.Now(); now.Sub(p.last) >= 100*time.Millisecond {
		p.last = now
		p.report(p.done)
	}
	return n, err
}

// Install downloads the offered build into a temp file — подпись и sha256
// проверены по дороге — and hands it to the platform's apply. Каждый шаг уходит
// странице (download с числом байт, install, restart); сбой — шаг error со
// словами, кнопка снова доступна (отзыв владельца 17.11: «закрылось и все»).
func (d *updateDesk) Install(ctx context.Context) error {
	d.mu.Lock()
	o := d.offer
	if d.u == nil || !o.Available() || d.st.Busy {
		d.mu.Unlock()
		return errNoUpdate
	}
	d.mu.Unlock()
	d.update(func(s *updateState) {
		s.Busy, s.Saved, s.Error, s.Phase, s.Done, s.Total = true, "", "", "download", 0, o.AssetSize()
	})
	err := d.install(ctx, o)
	if err != nil {
		d.update(func(s *updateState) { s.Busy, s.Phase, s.Error = false, "error", err.Error() })
		return err
	}
	d.update(func(s *updateState) { // предложение потрачено: кнопка не предложит то же снова
		s.Busy = false
		s.Phase = "saved"
		if s.Restart {
			s.Phase = "restart"
		}
		if d.apply != nil {
			d.offer, s.Available = selfupdate.Offer{}, false
		}
	})
	return nil
}

func (d *updateDesk) install(ctx context.Context, o selfupdate.Offer) error {
	f, err := os.CreateTemp(d.tmp, "dvk-update-*"+filepath.Ext(o.AssetName()))
	if err != nil {
		return err
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }() // установка уже взяла свое; подмененный файл — не нужен
	err = d.u.Download(ctx, o, &progressWriter{w: f, report: func(n int64) { d.update(func(s *updateState) { s.Done = n }) }})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	d.update(func(s *updateState) { s.Done, s.Phase = s.Total, "install" })
	if d.apply != nil {
		return d.apply(ctx, path, o.Version().String())
	}
	return nil
}

// updateEvery is how often the window asks for a newer release.
const updateEvery = 5 * time.Minute

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
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	// Во временный файл (O_EXCL) и переименование: симлинк на итоговом имени не
	// читается как цель записи, обрезок на нем не остается (ревью #679).
	tmp, err := os.CreateTemp(dir, ".dvk-*"+filepath.Ext(path))
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	dst := filepath.Join(dir, "DVK-"+version+filepath.Ext(path))
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return dst, nil
}

// noteRestart tells the page the window is about to restart on the new build
// and leaves the «обновилось» marker for the first start of the new window.
func (d *updateDesk) noteRestart(to string) {
	d.mu.Lock()
	d.st.Restart = true
	from := d.st.Current
	d.mu.Unlock()
	if d.dir != "" {
		_ = writeUpdateDone(d.dir, from, to)
	}
}

// noteSaved remembers where the build was put, for the page.
func (d *updateDesk) noteSaved(path string) {
	d.mu.Lock()
	d.st.Saved = path
	d.mu.Unlock()
}

// UpdateState is the update state for the page.
func (a *App) UpdateState() updateState {
	if a.update == nil {
		return updateState{Dev: true}
	}
	return a.update.State()
}

// UpdateCheck asks the release now and returns the state.
func (a *App) UpdateCheck() updateState {
	if a.update == nil {
		return updateState{Dev: true}
	}
	a.update.check(a.ctx)
	return a.update.State()
}

// UpdateInstall installs the offered build by the person's click.
func (a *App) UpdateInstall() error {
	if a.update == nil {
		return errNoUpdate
	}
	return a.update.Install(a.ctx)
}

// launchdLabel — служба окна в launchd (scripts/desktop-install.sh).
const launchdLabel = "com.daniilvdovin.kb"

// restartCommand restarts the window on the new build. Под launchd —
// `launchctl kickstart -k`: помощник-потомок умирает вместе с окном (замер
// 17.11, обновление «закрылось и все»); без launchd — open -n после выхода
// окна по pid.
func restartCommand(service string, uid int, bundle string, pid int) *exec.Cmd {
	if service == launchdLabel {
		return exec.Command("launchctl", "kickstart", "-k", "gui/"+strconv.Itoa(uid)+"/"+launchdLabel)
	}
	return exec.Command("/bin/sh", "-c", `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; /usr/bin/open -n "$0"`, bundle, strconv.Itoa(pid))
}

// updateDone is the marker «окно обновилось» for the first start after it.
type updateDone struct {
	From string `json:"from"`
	To   string `json:"to"`
}

const updateDoneFile = "update-done.json"

// writeUpdateDone leaves the marker next to the settings: новое окно прочтет его
// один раз и скажет, что обновилось.
func writeUpdateDone(dir, from, to string) error {
	b, err := json.Marshal(updateDone{From: from, To: to})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, updateDoneFile), b, 0o600)
}

// consumeUpdateDone reads and removes the marker.
func consumeUpdateDone(dir string) (updateDone, bool) {
	p := filepath.Join(dir, updateDoneFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return updateDone{}, false
	}
	_ = os.Remove(p)
	var d updateDone
	if json.Unmarshal(b, &d) != nil || d.To == "" {
		return updateDone{}, false
	}
	return d, true
}

// UpdateDone says once, after a restart, that the window has just updated.
func (a *App) UpdateDone() updateDone {
	if a.update == nil || a.update.dir == "" {
		return updateDone{}
	}
	d, _ := consumeUpdateDone(a.update.dir)
	return d
}
