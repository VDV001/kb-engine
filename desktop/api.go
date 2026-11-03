package main

import (
	"net/http"

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
// one protocol. The file index is returned unbuilt — the caller builds it in
// the background and the API answers 503 until then.
//
// ponytail: сборка повторяет часть buildServeHandler из cmd/kbengine (тот
// живет в package main и недоступен). Потолок: две точки сборки разойдутся,
// когда серверу добавят опцию. Путь: вынести сборку в internal/composition и
// звать ее из обоих.
func newAPI(cfg config) (http.Handler, *filesearch.Service, error) {
	src, err := localfiles.New(cfg.Roots, cfg.Exclude)
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
	return h, files, nil
}
