package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobs3"
	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
	"github.com/daniil/kb-engine/internal/adapter/financevocab"
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
	// writer — запись через API; nil у каталога на диске: туда пишет CLI.
	writer httpapi.CatalogWriter
	// fin — финансы из хранилища (журнал рядом с базой); nil — источник
	// финансов задают флаги --ledger/--from, как раньше.
	fin       httpapi.Financier
	finWriter httpapi.FinanceWriter
}

// options are the server options the source brings: словарь синонимов и, у
// хранилища, запись.
func (s catalogSource) options() []httpapi.Option {
	opts := []httpapi.Option{s.synonyms}
	if s.writer != nil {
		opts = append(opts, httpapi.WithCatalogWriter(s.writer))
	}
	if s.finWriter != nil {
		opts = append(opts, httpapi.WithFinanceWriter(s.finWriter))
	}
	return opts
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
		return diskCatalogSource(location, stderr), nil
	}
	store, err := openS3Store(bucket, s3TokenFile)
	if err != nil {
		return catalogSource{}, err
	}
	return storeCatalogSource(ctx, store, key, stderr)
}

// openS3Store opens a bucket in Yandex Object Storage — одно место для serve
// --catalog, serve --tenants и users. Токен из сервиса метаданных (Serverless
// Container) или из --s3-token-file: файл 0600, печатный ASCII, как у токена
// serve, и перечитывается при смене.
func openS3Store(bucket, s3TokenFile string) (blob.Store, error) {
	var tok blobs3.TokenSource = &blobs3.MetadataToken{}
	if s3TokenFile != "" {
		ft, err := newFileToken(s3TokenFile)
		if err != nil {
			return nil, fmt.Errorf("--s3-token-file: %w", err)
		}
		tok = ft
	}
	return blobs3.New(blobs3.Config{Endpoint: blobs3.YandexEndpoint, Bucket: bucket, Token: tok})
}

// diskCatalogSource is the source for a catalog file on disk — единственное
// место, где из пути выводятся артефакты и словарь.
func diskCatalogSource(path string, stderr io.Writer) catalogSource {
	return catalogSource{
		loader:    catalogjson.FileLoader{Path: path},
		artefacts: artefactsFS(path),
		synonyms:  synonymsFor(path, stderr),
	}
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
	src := catalogSource{
		loader:    loader,
		artefacts: artefacts,
		synonyms:  storeSynonyms(ctx, store, path.Join(dataDir, searchsyn.FileName), stderr),
		writer:    storeCatalogWriter{w: catalogjson.NewStoreWriter(store, key), now: time.Now},
	}
	// Финансы — рядом с базой: <человек>/kb/_data/catalog.json → <человек>/fin/.
	// Они есть у каждого: журнала нет — операций нет, первая запись его
	// заводит. Журнал здесь не читается: его сбой — отказ финансового вида, а
	// не всей базы человека. При другой раскладке места финансов нет: угадывая
	// его по глубине ключа, базы a/_data и b/_data делили бы один журнал.
	kbRoot := path.Dir(dataDir)
	if path.Base(dataDir) != "_data" || path.Base(kbRoot) != "kb" {
		return src, nil
	}
	finDir := path.Join(path.Dir(kbRoot), "fin")
	sf := storeFinances{ctx: ctx, store: store, journalKey: path.Join(finDir, "journal.jsonl"),
		vocabKey: path.Join(finDir, financevocab.FileName), now: time.Now}
	src.fin, src.finWriter = sf, sf
	return src, nil
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

// fileToken is an IAM token kept in a file and re-read when the file changes:
// токен `yc iam create-token` живет до 12 часов, и свежий в том же файле
// подхватывается без перезапуска serve. Права файла проверяются при каждом
// запросе токена, содержимое — при каждой смене времени правки.
type fileToken struct {
	path   string
	cached atomic.Pointer[readToken]
}

// readToken is the token as read at the file's modification time mod.
type readToken struct {
	mod time.Time
	tok string
}

func newFileToken(path string) (*fileToken, error) {
	f := &fileToken{path: path}
	if _, err := f.Token(context.Background()); err != nil {
		return nil, err
	}
	return f, nil
}

// Token returns the token, re-reading the file if it changed.
func (f *fileToken) Token(context.Context) (string, error) {
	info, err := os.Stat(f.path)
	if err != nil {
		return "", err
	}
	if err := tokenFileMode(info); err != nil {
		return "", fmt.Errorf("%w (%s)", err, f.path)
	}
	// Без замка: два одновременных перечитывания читают один и тот же файл.
	if c := f.cached.Load(); c != nil && info.ModTime().Equal(c.mod) {
		return c.tok, nil
	}
	tok, err := readTokenFile(f.path)
	if err != nil {
		return "", err
	}
	f.cached.Store(&readToken{mod: info.ModTime(), tok: tok})
	return tok, nil
}
