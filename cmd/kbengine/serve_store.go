package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobs3"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/searchsyn"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// catalogLoader is what the use cases need from the catalog's source.
type catalogLoader interface {
	Load() (*domain.Catalog, error)
}

// catalogSource is everything serve derives from --catalog: сам каталог,
// дерево артефактов для /kb/ и словарь синонимов рядом с каталогом. С диска и
// из хранилища они выводятся одним правилом, поэтому живут вместе.
type catalogSource struct {
	loader    catalogLoader
	artefacts fs.FS
	synonyms  httpapi.Option // nil — словаря нет, поиск без перевода терминов
}

// openCatalogSource opens --catalog: путь на диске или s3://бакет/ключ в Yandex
// Object Storage. Токен к бакету — из сервиса метаданных (Serverless
// Container) или из --s3-token-file (IAM-токен на Маке, `yc iam create-token`).
func openCatalogSource(ctx context.Context, location, s3TokenFile string, stderr io.Writer) (catalogSource, error) {
	bucket, key, isS3, err := parseS3Location(location)
	if err != nil {
		return catalogSource{}, err
	}
	if !isS3 {
		if s3TokenFile != "" {
			return catalogSource{}, errors.New("--s3-token-file needs --catalog s3://bucket/key")
		}
		return catalogSource{
			loader:    catalogjson.FileLoader{Path: location},
			artefacts: artefactsFS(location),
			synonyms:  synonymsFor(location, stderr),
		}, nil
	}
	var tok blobs3.TokenSource = &blobs3.MetadataToken{}
	if s3TokenFile != "" {
		// Те же требования, что к токену serve: файл 0600, печатный ASCII.
		t, err := readTokenFile(s3TokenFile)
		if err != nil {
			return catalogSource{}, fmt.Errorf("--s3-token-file: %w", err)
		}
		tok = blobs3.StaticToken(t)
	}
	store, err := blobs3.New(blobs3.Config{Endpoint: blobs3.YandexEndpoint, Bucket: bucket, Token: tok})
	if err != nil {
		return catalogSource{}, err
	}
	return storeCatalogSource(ctx, store, key, stderr)
}

// parseS3Location splits s3://bucket/key. Не s3:// — путь на диске (isS3 false).
func parseS3Location(location string) (bucket, key string, isS3 bool, err error) {
	rest, ok := strings.CutPrefix(location, "s3://")
	if !ok {
		return "", "", false, nil
	}
	if strings.ContainsAny(rest, "?#") {
		return "", "", true, fmt.Errorf("--catalog %s: без ? и #", location)
	}
	bucket, key, _ = strings.Cut(rest, "/")
	if bucket == "" || key == "" {
		return "", "", true, fmt.Errorf("--catalog %s: нужен s3://бакет/ключ", location)
	}
	if err := blob.ValidKey(key); err != nil {
		return "", "", true, fmt.Errorf("--catalog %s: %w", location, err)
	}
	return bucket, key, true, nil
}

// storeCatalogSource builds the source from a store. Раскладка ключей та же,
// что у knowledge-base на диске: <корень>/_data/catalog.json, словарь рядом с
// каталогом, артефакты от корня. Каталог читается сразу: его отсутствие —
// отказ при старте, а не 500 на каждом запросе.
func storeCatalogSource(ctx context.Context, store blob.Store, key string, stderr io.Writer) (catalogSource, error) {
	loader := catalogjson.NewStoreLoader(ctx, store, key)
	if _, err := loader.Load(); err != nil {
		return catalogSource{}, err
	}
	dataDir := path.Dir(key)
	prefix := ""
	if root := path.Dir(dataDir); root != "." {
		prefix = root + "/"
	}
	artefacts, err := blob.FS(ctx, store, prefix)
	if err != nil {
		return catalogSource{}, err
	}
	return catalogSource{
		loader:    loader,
		artefacts: artefacts,
		synonyms:  storeSynonyms(ctx, store, path.Join(dataDir, searchsyn.FileName), stderr),
	}, nil
}

// storeSynonyms loads the dictionary from the store. Как synonymsFor с диска:
// словаря нет или он битый — поиск работает без перевода терминов, и об этом
// сказано при запуске.
func storeSynonyms(ctx context.Context, store blob.Store, key string, stderr io.Writer) httpapi.Option {
	o, err := store.Get(ctx, key)
	if errors.Is(err, blob.ErrNotFound) {
		err = fmt.Errorf("%w: %s", searchsyn.ErrNoDictionary, key)
	}
	var syn search.Dictionary
	if err == nil {
		syn, err = searchsyn.Parse(key, o.Data)
	}
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v — поиск ищет подстрокой, транслитерацией и с опечатками, но не переводит термины\n", err)
		return nil
	}
	return httpapi.WithSynonyms(search.New(syn))
}
