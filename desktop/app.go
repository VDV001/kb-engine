package main

import (
	"context"
	"os/exec"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// fileChecker is the part of the file index the app needs: the same trust
// boundary the API uses, so a path the index would refuse is not opened
// either. Check, а не Read: ради разрешения файл целиком не читается.
//
// ponytail: между проверкой и open файл можно подменить ссылкой (open
// получает путь, а не дескриптор). Потолок: нужен доступ на запись в
// индексируемую папку. Путь: открывать через NSWorkspace по дескриптору.
type fileChecker interface {
	Check(path string) error
}

// App is bound into the window: its exported methods are callable from the
// page as window.go.main.App.<Method>.
type App struct {
	ctx      context.Context
	files    fileChecker
	settings *folderSettings
	index    *indexer
	run      func(name string, args ...string) error
}

func newApp(files fileChecker, settings *folderSettings, index *indexer) *App {
	return &App{files: files, settings: settings, index: index, run: func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	}}
}

// Sources returns the sources in priority order with their state and counts.
// Открытие экрана заодно возвращает в индекс папки, что снова доступны, не
// дожидаясь плановой пересборки.
func (a *App) Sources() sourcesView {
	a.settings.reconcile()
	return a.settings.GetSources()
}

// SetSources validates and saves the sources, then reindexes in the background.
func (a *App) SetSources(list []desktopconfig.Source, exclude []string, tokens map[string]string) error {
	return a.settings.SetSources(list, exclude, tokens)
}

// Modules returns the window modules with whether each is on.
func (a *App) Modules() []desktopconfig.Module { return a.settings.Modules() }

// SetModules saves which modules are on.
func (a *App) SetModules(list []desktopconfig.Module) error { return a.settings.SetModules(list) }

// Tabs returns the search tabs after «Все».
func (a *App) Tabs() []desktopconfig.Tab { return a.settings.Tabs() }

// SetTabs saves the search tabs.
func (a *App) SetTabs(list []desktopconfig.Tab) error { return a.settings.SetTabs(list) }

// Accents returns the window's look: режим и свой набор человека.
func (a *App) Accents() desktopconfig.Accents { return a.settings.Accents() }

// SetAccents saves the window's look.
func (a *App) SetAccents(look desktopconfig.Accents) error { return a.settings.SetAccents(look) }

// NewSourceID returns an id for a source the person is adding: к id сервера
// привязан токен, и он нужен странице до сохранения списка.
func (a *App) NewSourceID(kind string) string { return desktopconfig.NewID(desktopconfig.Kind(kind)) }

// ServerStates returns the live state of the server sources; панель зовет его
// после того, как нарисовала список.
func (a *App) ServerStates() []sourceRow { return a.settings.ServerStates() }

// CheckServer is the «Проверить» button of the server form.
func (a *App) CheckServer(id, url, token string) (kbserver.Info, error) {
	return a.settings.CheckServer(id, url, token)
}

// Problems returns what the sources cannot do now, with the reason and what
// to do: серверы опрашиваются, как в ServerStates (≤ probeTimeout).
func (a *App) Problems() problemsView {
	rows := a.settings.GetSources().Sources
	live := map[string]sourceRow{}
	for _, r := range a.settings.ServerStates() {
		live[r.ID] = r
	}
	for i, r := range rows {
		if s, ok := live[r.ID]; ok {
			rows[i] = s
		}
	}
	return collectProblems(rows, a.index.Status(), a.settings.home)
}

// IndexStatus reports whether a build is running and how the last one went.
func (a *App) IndexStatus() indexStatus { return a.index.Status() }

// PickFolder opens the system folder chooser — the same one as when attaching
// files — and returns the chosen path, or "" if the person cancelled.
func (a *App) PickFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Папка для поиска",
		CanCreateDirectories: false,
	})
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// Open opens a found file in its default application.
//
// Путь приходит со страницы, то есть снаружи: прежде чем звать open, он
// проходит ту же проверку, что и чтение через API. Иначе окно открыло бы
// любой файл диска, включая .env, по одному вызову из консоли страницы.
func (a *App) Open(path string) error {
	if err := a.files.Check(path); err != nil {
		return err
	}
	return a.run("open", path)
}

// Reveal shows a found file in Finder, under the same check as Open.
func (a *App) Reveal(path string) error {
	if err := a.files.Check(path); err != nil {
		return err
	}
	return a.run("open", "-R", path)
}
