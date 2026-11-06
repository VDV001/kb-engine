// Command kb-desktop is the macOS window over kb-engine: поиск по базе знаний
// и по файлам на этой машине в одном окне.
//
// Окно — тонкая оболочка: интерфейс (frontend/, чистые HTML/CSS/JS, вшиты в
// бинарь) ходит только в HTTP API движка, который собирается тут же, в
// процессе, без открытых портов. Поэтому тот же интерфейс работает в браузере
// и против удаленного сервера.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/extractcache"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/weblink"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend
var assets embed.FS

// appAssets — собранная витрина движка для модуля «Финансы» (#435, G4):
// `just desktop` копирует frontend/dist сюда; в git лежит только .keep.
//
//go:embed all:app-ui
var appAssets embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// browserAddr returns the address after -browser, or "" for the window. Only
// loopback is accepted: dev mode serves personal files without any auth.
func browserAddr(args []string) string {
	for i, a := range args {
		if a == "-browser" && i+1 < len(args) && strings.HasPrefix(args[i+1], "127.0.0.1:") {
			return args[i+1]
		}
	}
	return ""
}

// browserHandler serves the UI and the API for -browser — только этой машине,
// весь режим целиком: страница, перепривязавшая свой домен на 127.0.0.1 (DNS
// rebinding), не прочтет ни выдачу, ни записи, ни финансы.
func browserHandler(api http.Handler, ui fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/", http.FileServerFS(ui))
	return httpapi.LocalHostOnly(mux)
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfgPath := desktopconfig.Path(home)
	cfg, err := desktopconfig.Load(cfgPath, home)
	if err != nil {
		return err
	}
	// Текст PDF и картинок распознает kb-extract рядом с окном (kb.app/Contents/
	// MacOS); без него окно читает только то, что уже распознано.
	ext := extraction{cache: extractcache.Cache{Dir: desktopconfig.ExtractDir(cfgPath)}}
	if exe, err := os.Executable(); err == nil {
		if h, ok := extractcache.HelperNextTo(exe); ok {
			ext.helper = h
		}
	}
	api, idx, err := newAPI(cfg, desktopconfig.IndexPath(cfgPath), ext)
	if err != nil {
		return err
	}
	ui, err := fs.Sub(assets, "frontend")
	if err != nil {
		return err
	}
	appUI, err := fs.Sub(appAssets, "app-ui")
	if err != nil {
		return err
	}
	// Индекс собирается в фоне: окно открывается сразу и показывает экран
	// индексации, пока API отвечает 503. Закрытие окна отменяет обход: иначе он
	// дошел бы до конца, держа диск, уже после выхода из интерфейса.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened, _ := desktopconfig.NormalizeRoots(cfg.FolderRoots(), home) // то же, что открыл newAPI
	settings := &folderSettings{path: cfgPath, home: home, cfg: cfg, applied: opened,
		apply: idx.applySources, countFiles: idx.files.CountByRoot, countCatalog: idx.catalog.count,
		tokens: newCachedTokens(keychainTokens()), probe: probeServer, find: findServer,
		countMatches: idx.files.CountMatches, indexing: func() bool { return idx.Status().Building }}
	idx.reconcile = settings.reconcile // до start: горутина таймера читает поле
	settings.publish()                 // снимок для поиска: дальше он читается без f.mu
	settings.setOpen(opened)
	api = settings.searchRoute(api) // общая выдача источников — и в окне, и в -browser
	idx.start(ctx)

	// Режим разработки: тот же интерфейс и API в браузере, без окна. Интерфейс
	// правится без пересборки .app, и его можно проверить Playwright'ом.
	if addr := browserAddr(os.Args[1:]); addr != "" {
		log.Printf("kb: dev UI on http://%s", addr)
		srv := &http.Server{Addr: addr, Handler: browserHandler(api, ui), ReadHeaderTimeout: 10 * time.Second}
		return srv.ListenAndServe()
	}

	app := newApp(idx.files, settings, idx)
	app.chat = newChatDesk(settings, filepath.Join(filepath.Dir(cfgPath), "chats"))
	app.chat.useSources(idx.files)
	app.google = newGoogleDesk(settings)
	settings.drive = app.google.Search
	app.chat.useAttachments(idx.files, ext, weblink.Client{HTTP: &http.Client{Timeout: 30 * time.Second}}, app.google)
	app.listen = &dictation{emit: func(listenEvent) {}}
	app.voice = &speaker{}
	if exe, err := os.Executable(); err == nil {
		app.listen.bin = listenNextTo(exe)
	}
	return wails.Run(&options.App{
		Title:     "DVK",
		Width:     1180,
		Height:    760,
		MinWidth:  760,
		MinHeight: 480,
		// Фон окна совпадает с фоном страницы, чтобы при открытии не мелькал
		// белый прямоугольник до первой отрисовки.
		BackgroundColour: &options.RGBA{R: 17, G: 19, B: 24, A: 255},
		AssetServer: &assetserver.Options{
			Assets: ui,
			// Все, чего нет среди файлов интерфейса: /api/* окна и /app/* модуля
			// «Финансы» (прокси на kb-сервер с токеном — только здесь, не в -browser).
			Handler: withAppProxy(api, newAppProxy(settings.appUpstream, builtAppUI(appUI), nil)),
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			installMenuBar(tray{win: wailsWindow{ctx: ctx}, ctx: ctx})
		},
		// Красная кнопка прячет окно: вернуть его — ⌥Space, значок в Dock или в
		// меню-баре (menubar_darwin.m), выход — ⌘Q или из меню значка.
		HideWindowOnClose: true,
		OnShutdown:        func(context.Context) { cancel() },
		Bind:              []any{app},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.NSAppearanceNameDarkAqua,
			About: &mac.AboutInfo{
				Title:   "DVK",
				Message: fmt.Sprintf("Поиск по базе знаний и файлам.\nКонфиг: %s", cfgPath),
			},
		},
	})
}
