package main

import (
	"log"
	"net/http"
	"os"
	"sync"

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

// catalogSwitch is the knowledge base the window reads now. Экран источников
// меняет каталог без перезапуска окна: сервисы базы читают через Load, а Load
// берет тот путь, что стоит сейчас. Пустой путь — базы нет.
type catalogSwitch struct {
	mu   sync.RWMutex
	path string
}

func (c *catalogSwitch) set(path string) {
	c.mu.Lock()
	c.path = path
	c.mu.Unlock()
}

// Load reads the current catalog.
func (c *catalogSwitch) Load() (*domain.Catalog, error) {
	c.mu.RLock()
	p := c.path
	c.mu.RUnlock()
	if p == "" {
		return emptyCatalog{}.Load()
	}
	return catalogjson.FileLoader{Path: p}.Load()
}

// count returns how many entries the current catalog has.
func (c *catalogSwitch) count() (int, error) {
	cat, err := c.Load()
	if err != nil {
		return 0, err
	}
	return cat.Len(), nil
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
func newAPI(cfg desktopconfig.Config, indexPath string, ext extraction) (http.Handler, *indexer, error) {
	home, _ := os.UserHomeDir() // без домашней папки «~» просто не раскроется
	roots, skipped := desktopconfig.NormalizeRoots(cfg.FolderRoots(), home)
	for _, s := range skipped {
		log.Printf("files: папка пропущена: %s", s)
	}
	src, err := localfiles.New(roots, cfg.Exclude, ext.option())
	if err != nil {
		return nil, nil, err
	}
	// Снимок индекса рядом с config.json: со второго запуска перечитываются только
	// измененные файлы. Пустой путь — индекс только в памяти (тесты).
	files := filesearch.NewService(src, fileindexgob.Option(indexPath))

	loader := &catalogSwitch{}
	loader.set(desktopconfig.ExpandHome(cfg.CatalogPath(), home))
	h := httpapi.NewServer(query.NewService(loader), audit.NewService(loader), analytics.NewService(loader), nil,
		func() (analyticsconfig.Config, error) { return analyticsconfig.Config{}, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, httpapi.EngineInfo{Version: "desktop"}, nil,
		httpapi.WithFiles(files))
	x := newIndexer(files, src)
	x.ext, x.catalog = ext, loader
	return h, x, nil
}
