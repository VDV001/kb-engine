package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
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
func newAPI(cfg config) (http.Handler, *indexer, error) {
	home, _ := os.UserHomeDir() // без домашней папки «~» просто не раскроется
	roots, skipped := normalizeRoots(cfg.Roots, home)
	for _, s := range skipped {
		log.Printf("files: папка пропущена: %s", s)
	}
	src, err := localfiles.New(roots, cfg.Exclude)
	if err != nil {
		return nil, nil, err
	}
	files := filesearch.NewService(src)

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

// normalizeRoots раскрывает «~», оставляет только существующие абсолютные папки
// и называет остальные. Удаленная папка или опечатка в конфиге не роняют окно:
// раньше любая такая строка доходила до log.Fatal (ревью #371).
func normalizeRoots(roots []string, home string) (ok, skipped []string) {
	for _, r := range roots {
		p := r
		if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		if !filepath.IsAbs(p) {
			skipped = append(skipped, r+" (путь не абсолютный)")
			continue
		}
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			skipped = append(skipped, r+" (папки нет)")
			continue
		}
		ok = append(ok, filepath.Clean(p))
	}
	return ok, skipped
}
