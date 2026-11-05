package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста `fin sync` (sync_test.go)

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobs3"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// tenantsHandler serves several people from one store, each from their own
// base under users/<id>/.
//
// Изоляция устроена, а не проверяется: на каждого человека собирается свой
// обработчик (buildServeHandlerFrom со своим catalogSource), и у обработчика
// одного нет ни одной ссылки на данные другого. Верхний слой только узнает
// человека по токену и выбирает его обработчик. Фильтра «не показывать чужое»
// нет нигде, потому что чужого в обработчике нет.
func tenantsHandler(ctx context.Context, store blob.Store, stderr io.Writer) http.Handler {
	return &tenants{
		ctx: ctx, store: store, stderr: stderr,
		reg:      userauth.NewRegistry(ctx, store),
		handlers: map[domain.UserID]http.Handler{},
	}
}

type tenants struct {
	ctx    context.Context
	store  blob.Store
	stderr io.Writer
	reg    *userauth.Registry

	mu       gosync.Mutex
	handlers map[domain.UserID]http.Handler
}

func (t *tenants) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
		if id, ok := t.reg.Lookup(tok); ok {
			h, err := t.handlerFor(id)
			if err != nil {
				// Причина — в журнал сервера, а не в ответ: в ней ключ хранилища.
				fmt.Fprintf(t.stderr, "serve: base of %s: %v\n", id, err)
				http.Error(w, "base unavailable", http.StatusServiceUnavailable)
				return
			}
			h.ServeHTTP(w, r)
			return
		}
	}
	// Проба здоровья без токена: отвечает, что процесс жив, и ничего о базах.
	if r.URL.Path == "/healthz" {
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="kbengine"`)
	http.Error(w, "bearer token required", http.StatusUnauthorized)
}

// handlerFor returns the person's handler, building it on first use. Ошибка
// не запоминается: следующий запрос попробует снова.
//
// ponytail: обработчик собирается под общим замком. Потолок — первый вход
// одного человека задерживает первый вход другого на одно чтение каталога.
// Путь апгрейда — замок на человека.
func (t *tenants) handlerFor(id domain.UserID) (http.Handler, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h, ok := t.handlers[id]; ok {
		return h, nil
	}
	src, err := storeCatalogSource(t.ctx, t.store, userauth.CatalogKey(id), t.stderr)
	if err != nil {
		return nil, err
	}
	h, err := buildServeHandlerFrom(src, "", "", "", "", "", "", "", "", nil)
	if err != nil {
		return nil, err
	}
	t.handlers[id] = h
	return h, nil
}

// openTenantStore opens --tenants: s3://бакет или каталог на диске (локальная
// проверка на нескольких людях).
func openTenantStore(location, s3TokenFile string) (blob.Store, error) {
	rest, isS3 := strings.CutPrefix(location, "s3://")
	if !isS3 {
		if s3TokenFile != "" {
			return nil, errors.New("--s3-token-file needs --tenants s3://bucket")
		}
		return blobfs.New(location)
	}
	bucket := strings.TrimSuffix(rest, "/")
	if bucket == "" || strings.ContainsAny(bucket, "/?#") {
		return nil, fmt.Errorf("--tenants %s: нужен s3://бакет без ключа", location)
	}
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

// runServeTenants is serve --tenants.
func runServeTenants(location, s3TokenFile, addr, pprofAddr, tlsCert, tlsKey string, allowOpen bool, set map[string]bool, stdout, stderr io.Writer) int {
	var clash []string
	for name, on := range set {
		if on {
			clash = append(clash, name)
		}
	}
	if len(clash) > 0 {
		slices.Sort(clash)
		fmt.Fprintf(stderr, "serve: --tenants несовместим с %s: базы людей берутся из хранилища, вход — по токенам из %s, а личные файлы этого Мака сервер отдал бы всем\n",
			strings.Join(clash, ", "), userauth.RegistryKey)
		return 2
	}
	_, tlsCfg, err := serveAccess(addr, pprofAddr, "", tlsCert, tlsKey, allowOpen, true)
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 2
	}
	store, err := openTenantStore(location, s3TokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	h := tenantsHandler(context.Background(), store, stderr)
	srv := newServeServer(addr, h, "", tlsCfg)
	fmt.Fprintf(stdout, "kbengine: serving people's bases on %s (tenants %s; tokens %s, TLS %s)\n", addr, location, userauth.RegistryKey, onOff(tlsCfg != nil))
	startPprof(pprofAddr, stdout, stderr)
	if err := listenAndServe(srv); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}
