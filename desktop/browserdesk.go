package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/daniil/kb-engine/internal/adapter/cdpbrowser"
)

var (
	errBrowserNotListed = errors.New("этого браузера нет в списке установленных")
	errNoKbengine       = errors.New("не нашла программу kbengine рядом с окном, в ~/go/bin и в PATH: соберите окно командой just desktop или установите kbengine")
)

// browserServer is what the desk needs from the process that runs `kbengine browser serve`.
type browserServer interface {
	State() browserState
	Start(bin, chrome string) error
	Stop() error
}

// browserDesk is the Browser tab's controls: выбрать браузер, включить, выключить.
type browserDesk struct {
	settings  *folderSettings
	host      browserServer
	installed func() []cdpbrowser.Browser
	kbengine  func() (string, error)
}

func newBrowserDesk(s *folderSettings, h browserServer) *browserDesk {
	exe, _ := os.Executable()
	return &browserDesk{settings: s, host: h, installed: cdpbrowser.Installed, kbengine: func() (string, error) {
		return findKbengine(filepath.Dir(exe), s.home, exec.LookPath)
	}}
}

// browserView is the tab's whole state in one call: список для чипов, что выбрано,
// что значит «Автоматически» сейчас и работает ли сервер.
type browserView struct {
	Installed []cdpbrowser.Browser
	Chosen    string // путь выбранного браузера; пусто — «Автоматически»
	Auto      string // имя того, что выберется при «Автоматически»
	State     browserState
}

// View returns the controls' state; вкладка опрашивает его раз в пару секунд.
func (d *browserDesk) View() browserView {
	list := d.installed()
	v := browserView{Installed: list, Chosen: d.chosen(list), State: d.host.State()}
	if len(list) > 0 {
		v.Auto = list[0].Name
	}
	return v
}

// chosen is the saved browser while it is still installed: удаленный браузер не должен ронять запуск.
func (d *browserDesk) chosen(list []cdpbrowser.Browser) string {
	path := d.settings.snapshot().cfg.Browser.Path
	if slices.ContainsFunc(list, func(b cdpbrowser.Browser) bool { return b.Path == path }) {
		return path
	}
	return ""
}

// Choose saves the browser for the next start; пусто — «Автоматически». Путь
// приходит со страницы окна и станет запускаемым файлом, поэтому берется только из списка.
func (d *browserDesk) Choose(path string) error {
	if path != "" && !slices.ContainsFunc(d.installed(), func(b cdpbrowser.Browser) bool { return b.Path == path }) {
		return fmt.Errorf("%w: %q", errBrowserNotListed, path)
	}
	d.settings.mu.Lock()
	defer d.settings.mu.Unlock()
	next := d.settings.cfg
	next.Browser.Path = path
	return d.settings.saveModulesLocked(next)
}

// Start launches the browser server with the chosen browser.
func (d *browserDesk) Start() error {
	list := d.installed()
	if len(list) == 0 {
		return cdpbrowser.ErrNoBrowser
	}
	bin, err := d.kbengine()
	if err != nil {
		return err
	}
	return d.host.Start(bin, d.chosen(list))
}

// Stop ends the server this window started.
func (d *browserDesk) Stop() error { return d.host.Stop() }

// findKbengine looks for the engine binary: рядом с окном (его кладет just desktop),
// потом ~/go/bin, потом PATH.
func findKbengine(exeDir, home string, lookPath func(string) (string, error)) (string, error) {
	for _, p := range []string{filepath.Join(exeDir, "kbengine"), filepath.Join(home, "go", "bin", "kbengine")} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if p, err := lookPath("kbengine"); err == nil {
		return p, nil
	}
	return "", errNoKbengine
}
