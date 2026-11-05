package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
)

type catalogSource struct {
	loader interface {
		Load() (*domain.Catalog, error)
	}
	artefacts fs.FS
	synonyms  httpapi.Option
}

func parseS3Location(string) (string, string, bool, error) { return "", "", false, nil }

func storeCatalogSource(context.Context, blob.Store, string, io.Writer) (catalogSource, error) {
	return catalogSource{}, errors.New("not implemented")
}

func buildServeHandlerFrom(catalogSource, string, string, string, string, string, string, string, string, []string, ...httpapi.Option) (http.Handler, error) {
	return nil, errors.New("not implemented")
}
