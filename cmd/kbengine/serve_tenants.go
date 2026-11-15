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
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/mailbox"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
	"golang.org/x/time/rate"
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
	lw := &lockedWriter{w: stderr}
	phones := userauth.NewRegistry(ctx, store, userauth.WithKey(userauth.DevicesKey))
	return &tenants{
		ctx: ctx, store: store, stderr: lw,
		reg:      userauth.NewRegistry(ctx, store),
		phones:   phones,
		handlers: map[domain.UserID]http.Handler{},
		limits:   map[string]*rate.Limiter{},
		now:      time.Now,
		mail: httpapi.MailHandler(mail.Service{Box: mailbox.Store{Blob: store, Seen: &mailbox.Seen{}}, Phones: freshPhones{userauth.Phones{Store: store}, phones}, Now: time.Now},
			func(f string, a ...any) { fmt.Fprintf(lw, "serve: "+f+"\n", a...) }),
	}
}

// freshPhones drops the phones registry cache after this process changed it:
// привязанный телефон входит сразу, отвязанный сразу перестает (ревью #630).
type freshPhones struct {
	userauth.Phones
	reg *userauth.Registry
}

func (p freshPhones) Add(ctx context.Context, id domain.UserID) (string, string, error) {
	defer p.reg.Invalidate()
	return p.Phones.Add(ctx, id)
}

func (p freshPhones) Revoke(ctx context.Context, id domain.UserID, device string) error {
	defer p.reg.Invalidate()
	return p.Phones.Revoke(ctx, id, device)
}

type tenants struct {
	ctx    context.Context
	store  blob.Store
	stderr io.Writer
	reg    *userauth.Registry
	phones *userauth.Registry // токены телефонов (#622): пускают только в ящик
	mail   http.Handler

	limitsMu gosync.Mutex
	limits   map[string]*rate.Limiter // человек|сторона → предел частоты ящика
	now      func() time.Time         // часы предела; тест их подменяет

	mu       gosync.Mutex
	handlers map[domain.UserID]http.Handler
}

func (t *tenants) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/mail" || strings.HasPrefix(r.URL.Path, "/api/mail/") {
		t.serveMail(w, r)
		return
	}
	if tok, ok := httpapi.RequestToken(r); ok {
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

// serveMail serves the phone chat mailbox (#622): токен человека — сторона
// компьютера, токен телефона — сторона телефона; сторону не выбирает клиент.
func (t *tenants) serveMail(w http.ResponseWriter, r *http.Request) {
	if tok, ok := httpapi.RequestToken(r); ok {
		side := domain.MailDesk
		id, ok := t.reg.Lookup(tok)
		if !ok {
			id, ok = t.phones.Lookup(tok)
			ok = ok && t.reg.Has(id) // телефон входит, пока жив хозяин (ревью #630)
			side = domain.MailPhone
		}
		if ok {
			if !t.allowMail(id.String() + "|" + string(side)) {
				w.Header().Set("Retry-After", "10")
				http.Error(w, "too many mailbox requests; poll every 10 s", http.StatusTooManyRequests)
				return
			}
			t.mail.ServeHTTP(w, r.WithContext(httpapi.WithMailCaller(r.Context(), id, side)))
			return
		}
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="kbengine"`)
	http.Error(w, "bearer token required", http.StatusUnauthorized)
}

// Предел частоты ящика на человека и сторону: опрос раз в 10 с и отправка —
// с большим запасом; цикл сломанного или чужого клиента — 429 (ревью #630, V2).
//
// ponytail: предел — в памяти экземпляра; при нескольких экземплярах он
// умножается на их число. Путь апгрейда — предел в API Gateway.
const (
	mailRate  = rate.Limit(1) // запросов в секунду в среднем
	mailBurst = 30
)

func (t *tenants) allowMail(key string) bool {
	t.limitsMu.Lock()
	l, ok := t.limits[key]
	if !ok {
		l = rate.NewLimiter(mailRate, mailBurst)
		t.limits[key] = l
	}
	t.limitsMu.Unlock()
	return l.AllowN(t.now(), 1)
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
