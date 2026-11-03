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
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend
var assets embed.FS

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

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(configPath(home), home)
	if err != nil {
		return err
	}
	api, idx, err := newAPI(cfg)
	if err != nil {
		return err
	}
	ui, err := fs.Sub(assets, "frontend")
	if err != nil {
		return err
	}
	// Индекс собирается в фоне: окно открывается сразу и показывает экран
	// индексации, пока API отвечает 503. Закрытие окна отменяет обход: иначе он
	// дошел бы до конца, держа диск, уже после выхода из интерфейса.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idx.start(ctx)
	settings := &folderSettings{path: configPath(home), home: home, cfg: cfg, apply: idx.apply}

	// Режим разработки: тот же интерфейс и API в браузере, без окна. Интерфейс
	// правится без пересборки .app, и его можно проверить Playwright'ом.
	if addr := browserAddr(os.Args[1:]); addr != "" {
		mux := http.NewServeMux()
		mux.Handle("/api/", api)
		mux.Handle("/", http.FileServerFS(ui))
		log.Printf("kb: dev UI on http://%s", addr)
		srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		return srv.ListenAndServe()
	}

	app := newApp(idx.files, settings, idx)
	return wails.Run(&options.App{
		Title:     "kb",
		Width:     1180,
		Height:    760,
		MinWidth:  760,
		MinHeight: 480,
		// Фон окна совпадает с фоном страницы, чтобы при открытии не мелькал
		// белый прямоугольник до первой отрисовки.
		BackgroundColour: &options.RGBA{R: 17, G: 19, B: 24, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  ui,
			Handler: api, // все, чего нет среди файлов интерфейса, то есть /api/*
		},
		OnStartup:  app.startup,
		OnShutdown: func(context.Context) { cancel() },
		Bind:       []any{app},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.NSAppearanceNameDarkAqua,
			About: &mac.AboutInfo{
				Title:   "kb",
				Message: fmt.Sprintf("Поиск по базе знаний и файлам.\nКонфиг: %s", configPath(home)),
			},
		},
	})
}
