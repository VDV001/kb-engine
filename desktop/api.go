package main

import (
	"log"
	"net/http"
	"os"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/fileindexgob"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
	"github.com/daniil/kb-engine/internal/usecase/audit"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

// emptyCatalog — каталог, когда база знаний не настроена: вкладка базы пуста,
// а поиск по файлам работает. Отсутствие базы — законное состояние окна.
type emptyCatalog struct{}

func (emptyCatalog) Load() (*domain.Catalog, error) { return domain.NewCatalog(nil) }

// catalogLoader is what the query, audit and analytics services read through.
type catalogLoader interface {
	Load() (*domain.Catalog, error)
}

// newAPI composes the engine's HTTP API for the window: the same handler
// `kbengine serve` builds, so the window, a browser and a remote server speak
// one protocol. The file index is returned unbuilt: the caller starts the
// indexer, and the API answers 503 until the first build is done.
//
// ponytail: сборка повторяет часть buildServeHandler из cmd/kbengine (тот
// живет в package main и недоступен). Потолок: две точки сборки разойдутся,
// когда серверу добавят опцию. Путь: вынести сборку в internal/composition и
// звать ее из обоих.
func newAPI(cfg desktopconfig.Config, indexPath string) (http.Handler, *indexer, error) {
	home, _ := os.UserHomeDir() // без домашней папки «~» просто не раскроется
	roots, skipped := desktopconfig.NormalizeRoots(cfg.Roots, home)
	for _, s := range skipped {
		log.Printf("files: папка пропущена: %s", s)
	}
	src, err := localfiles.New(roots, cfg.Exclude)
	if err != nil {
		return nil, nil, err
	}
	// Снимок индекса рядом с config.json: со второго запуска перечитываются только
	// измененные файлы. Пустой путь — индекс только в памяти (тесты).
	files := filesearch.NewService(src, fileindexgob.Option(indexPath))

	var loader catalogLoader = emptyCatalog{}
	if cfg.Catalog != "" {
		loader = catalogjson.FileLoader{Path: cfg.Catalog}
	}
	h := httpapi.NewServer(query.NewService(loader), audit.NewService(loader), analytics.NewService(loader), nil,
		func() (analyticsconfig.Config, error) { return analyticsconfig.Config{}, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, httpapi.EngineInfo{Version: "desktop"}, nil,
		httpapi.WithFiles(files))
	return h, newIndexer(files, src), nil
}
