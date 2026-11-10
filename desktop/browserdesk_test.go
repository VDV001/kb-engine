//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/cdpbrowser"
	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

var (
	chromeApp = cdpbrowser.Browser{Name: "Google Chrome", Path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	braveApp  = cdpbrowser.Browser{Name: "Brave", Path: "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"}
)

// fakeHost записывает, с чем окно запускало сервер.
type fakeHost struct {
	state       browserState
	started     []string // chrome каждого Start
	stopped     int
	startErr    error
	stopErr     error
	gotBin      string
	startCalled bool
}

func (f *fakeHost) State() browserState { return f.state }
func (f *fakeHost) Start(bin, chrome string) error {
	f.startCalled, f.gotBin = true, bin
	f.started = append(f.started, chrome)
	return f.startErr
}
func (f *fakeHost) Stop() error { f.stopped++; return f.stopErr }

func newTestDesk(t *testing.T, installed ...cdpbrowser.Browser) (*browserDesk, *fakeHost, *folderSettings) {
	t.Helper()
	s, _, _ := newSourcesSettings(t)
	h := &fakeHost{}
	d := newBrowserDesk(s, h)
	d.installed = func() []cdpbrowser.Browser { return installed }
	d.kbengine = func() (string, error) { return "/opt/kbengine", nil }
	return d, h, s
}

func TestBrowserDeskViewListsInstalledAndAuto(t *testing.T) {
	d, h, _ := newTestDesk(t, chromeApp, braveApp)
	h.state = browserState{Running: true, Own: true}
	v := d.View()
	if !slices.Equal(v.Installed, []cdpbrowser.Browser{chromeApp, braveApp}) {
		t.Errorf("Installed = %v", v.Installed)
	}
	if v.Auto != "Google Chrome" || v.Chosen != "" || !v.State.Running || !v.State.Own {
		t.Errorf("View = %+v", v)
	}
}

func TestBrowserDeskViewWithoutBrowsers(t *testing.T) {
	d, _, _ := newTestDesk(t)
	if v := d.View(); len(v.Installed) != 0 || v.Auto != "" {
		t.Fatalf("View без браузеров = %+v", v)
	}
	if err := d.Start(); !errors.Is(err, cdpbrowser.ErrNoBrowser) {
		t.Fatalf("Start без браузеров = %v, want ErrNoBrowser", err)
	}
}

func TestBrowserDeskChooseSavesAndStartUsesIt(t *testing.T) {
	d, h, s := newTestDesk(t, chromeApp, braveApp)
	if err := d.Choose(braveApp.Path); err != nil {
		t.Fatal(err)
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil || cfg.Browser.Path != braveApp.Path {
		t.Fatalf("в конфиге Browser.Path = %q, %v", cfg.Browser.Path, err)
	}
	if got := d.View().Chosen; got != braveApp.Path {
		t.Errorf("View.Chosen = %q", got)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.started, []string{braveApp.Path}) || h.gotBin != "/opt/kbengine" {
		t.Errorf("Start запустил %v через %q", h.started, h.gotBin)
	}
}

func TestBrowserDeskAutoStartsWithoutChromeFlag(t *testing.T) {
	d, h, _ := newTestDesk(t, chromeApp, braveApp)
	if err := d.Start(); err != nil || !slices.Equal(h.started, []string{""}) {
		t.Fatalf("Start = %v, started %v: без выбора флаг --chrome не нужен", err, h.started)
	}
	if err := d.Choose(braveApp.Path); err != nil {
		t.Fatal(err)
	}
	if err := d.Choose(""); err != nil { // «Автоматически» снимает выбор
		t.Fatal(err)
	}
	if d.View().Chosen != "" {
		t.Error("пустой путь не вернул «Автоматически»")
	}
}

// Граница доверия: путь приходит со страницы окна и станет запускаемым файлом,
// поэтому принимается только из списка установленных.
func TestBrowserDeskChooseRefusesUnlistedPath(t *testing.T) {
	d, _, s := newTestDesk(t, chromeApp)
	for _, p := range []string{"/bin/sh", "/tmp/evil", braveApp.Path, "chrome", filepath.Join(chromeApp.Path, "..", "..", "x")} {
		if err := d.Choose(p); !errors.Is(err, errBrowserNotListed) {
			t.Errorf("Choose(%q) = %v, want errBrowserNotListed", p, err)
		}
	}
	if cfg, _ := desktopconfig.Read(s.path); cfg.Browser.Path != "" {
		t.Errorf("отказанный путь попал в конфиг: %q", cfg.Browser.Path)
	}
}

// Браузер, который выбрали и потом удалили, не должен ронять запуск.
func TestBrowserDeskStartFallsBackWhenChosenGone(t *testing.T) {
	d, h, s := newTestDesk(t, chromeApp, braveApp)
	if err := d.Choose(braveApp.Path); err != nil {
		t.Fatal(err)
	}
	d.installed = func() []cdpbrowser.Browser { return []cdpbrowser.Browser{chromeApp} }
	if v := d.View(); v.Chosen != "" {
		t.Errorf("View.Chosen = %q: удаленный браузер не выбран", v.Chosen)
	}
	if err := d.Start(); err != nil || !slices.Equal(h.started, []string{""}) {
		t.Fatalf("Start = %v, started %v", err, h.started)
	}
	_ = s
}

func TestBrowserDeskStartWithoutKbengine(t *testing.T) {
	d, h, _ := newTestDesk(t, chromeApp)
	d.kbengine = func() (string, error) { return "", errNoKbengine }
	if err := d.Start(); !errors.Is(err, errNoKbengine) || h.startCalled {
		t.Fatalf("Start = %v, host вызван: %v", err, h.startCalled)
	}
}

func TestBrowserDeskStopDelegates(t *testing.T) {
	d, h, _ := newTestDesk(t, chromeApp)
	if err := d.Stop(); err != nil || h.stopped != 1 {
		t.Fatalf("Stop = %v, stopped %d", err, h.stopped)
	}
	h.stopErr = errBrowserForeign
	if err := d.Stop(); !errors.Is(err, errBrowserForeign) {
		t.Fatalf("Stop чужого = %v", err)
	}
}

func TestFindKbengine(t *testing.T) {
	for _, tc := range []struct {
		name string
		have []string // относительно exeDir/home
		want string
	}{
		{"рядом с окном", []string{"app/kbengine", "home/go/bin/kbengine"}, "app/kbengine"},
		{"в go/bin", []string{"home/go/bin/kbengine"}, "home/go/bin/kbengine"},
		{"нигде", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, p := range tc.have {
				mkExec(t, filepath.Join(root, p))
			}
			got, err := findKbengine(filepath.Join(root, "app"), filepath.Join(root, "home"), func(string) (string, error) { return "", errNoKbengine })
			if tc.want == "" {
				if !errors.Is(err, errNoKbengine) {
					t.Fatalf("err = %v, want errNoKbengine", err)
				}
				return
			}
			if err != nil || got != filepath.Join(root, tc.want) {
				t.Fatalf("findKbengine = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func mkExec(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Привязки Wails: окно зовет их по именам, и все они идут через browserDesk.
func TestAppBrowserBindings(t *testing.T) {
	d, h, _ := newTestDesk(t, chromeApp, braveApp)
	a := &App{browser: d}
	if got := a.BrowserState(); len(got.Installed) != 2 || got.Auto != "Google Chrome" {
		t.Fatalf("BrowserState = %+v", got)
	}
	if err := a.BrowserChoose(braveApp.Path); err != nil {
		t.Fatal(err)
	}
	if err := a.BrowserStart(); err != nil || !slices.Equal(h.started, []string{braveApp.Path}) {
		t.Fatalf("BrowserStart = %v, started %v", err, h.started)
	}
	if err := a.BrowserStop(); err != nil || h.stopped != 1 {
		t.Fatalf("BrowserStop = %v, stopped %d", err, h.stopped)
	}
}
