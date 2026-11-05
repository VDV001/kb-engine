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
		ctx: ctx, store: store, stderr: &lockedWriter{w: stderr},
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
	// Пробы без токена: процесс жив и готов, ничего о базах. За токеном под
	// в Kubernetes перезапускался бы по кругу (как в httpapi.RequireBearer).
	switch r.URL.Path {
	case "/healthz":
		_, _ = io.WriteString(w, "ok\n")
		return
	case "/readyz":
		_, _ = io.WriteString(w, "ready\n")
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="kbengine"`)
	http.Error(w, "bearer token required", http.StatusUnauthorized)
}

// handlerFor returns the person's handler, building it on first use. Сборка
// (чтение каталога из хранилища) идет вне замка: медленная или сломанная база
// одного человека не держит запросы остальных. Ошибка не запоминается —
// следующий запрос попробует снова.
//
// ponytail: два одновременных первых входа одного человека собирают обработчик
// дважды, и в карте остается первый. Потолок — лишнее чтение каталога. Путь
// апгрейда — ожидание сборки в полете, как в catalogjson.StoreLoader.
//
// ponytail: обработчики не выгружаются, и обработчик отозванного человека живет
// до перезапуска. Войти по отозванному токену нельзя (его не пускает реестр).
// Потолок — память на каталог каждого, кто входил. Путь апгрейда — выгрузка по
// давности использования.
func (t *tenants) handlerFor(id domain.UserID) (http.Handler, error) {
	t.mu.Lock()
	h, ok := t.handlers[id]
	t.mu.Unlock()
	if ok {
		return h, nil
	}
	src, err := storeCatalogSource(t.ctx, t.store, userauth.CatalogKey(id), t.stderr)
	if err != nil {
		return nil, err
	}
	built, err := buildServeHandlerFrom(src, "", "", "", "", "", "", "", "", nil)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if h, ok := t.handlers[id]; ok {
		return h, nil
	}
	t.handlers[id] = built
	return built, nil
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
	return openS3Store(bucket, s3TokenFile)
}

// runServeTenants is serve --tenants.
func runServeTenants(location, s3TokenFile, addr, pprofAddr, tlsCert, tlsKey string, allowOpen bool, set map[string]bool, stdout, stderr io.Writer) int {
	var clash []string
	for name, on := range set {
		if on {
			clash = append(clash, name)
		}
	}
	if len(clash) == 0 && s3TokenFile != "" && !strings.HasPrefix(location, "s3://") {
		clash = append(clash, "--s3-token-file (нужен --tenants s3://bucket)")
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

// lockedWriter serializes writes: обработчики людей собираются параллельно и
// пишут предупреждения в один журнал сервера, а io.Writer вызывающего
// (bytes.Buffer в тестах) одновременных записей не выдерживает.
type lockedWriter struct {
	mu gosync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
