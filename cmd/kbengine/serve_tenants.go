package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	gosync "sync" // имя sync в пакете main занято помощником теста `fin sync` (sync_test.go)
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/adapter/mailbox"
	"github.com/daniil/kb-engine/internal/adapter/phonepage"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/adapter/webpush"
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
// tenantOption configures tenantsHandler (tests).
type tenantOption func(*pushConfig)

// pushConfig is how the server reaches push services (#622).
type pushConfig struct {
	contact   string            // KB_PUSH_CONTACT: mailto: или https: для VAPID; пусто — push выключен
	transport http.RoundTripper // nil — http.DefaultTransport
}

// withPush replaces the push contact and transport (tests).
func withPush(contact string, rt http.RoundTripper) tenantOption {
	return func(c *pushConfig) { c.contact, c.transport = contact, rt }
}

func tenantsHandler(ctx context.Context, store blob.Store, stderr io.Writer, opts ...tenantOption) http.Handler {
	lw := &lockedWriter{w: stderr}
	logf := func(f string, a ...any) { fmt.Fprintf(lw, "serve: "+f+"\n", a...) }
	phones := userauth.NewRegistry(ctx, store, userauth.WithKey(userauth.DevicesKey))
	cfg := pushConfig{contact: os.Getenv("KB_PUSH_CONTACT")}
	for _, o := range opts {
		o(&cfg)
	}
	svc := mail.Service{Box: mailbox.Store{Blob: store, Seen: &mailbox.Seen{}}, Phones: freshPhones{userauth.Phones{Store: store}, phones},
		Subs: userauth.Phones{Store: store}, Now: time.Now, Logf: logf}
	if cfg.contact != "" { // без контакта push выключен: Push nil, маршруты — 404
		svc.Push = &lazyPusher{store: store, contact: cfg.contact, transport: cfg.transport}
	}
	return &tenants{
		ctx: ctx, store: store, stderr: lw,
		reg:       userauth.NewRegistry(ctx, store),
		phones:    phones,
		handlers:  map[domain.UserID]http.Handler{},
		limits:    map[mailKey]*rate.Limiter{},
		phonePage: phonepage.Handler(),
		// Приложение Android (mobile/android): «пакет:отпечаток ключа» из окружения
		// сервера; не задано — 404, как без приложения.
		assetLinks: phonepage.AssetLinks(os.Getenv("KB_ANDROID_APP")),
		now:        time.Now,
		mail:       httpapi.MailHandler(svc, logf),
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
	// phonePage — страница телефона /m/ (#622).
	phonePage http.Handler
	// assetLinks — /.well-known/assetlinks.json приложения Android (#622).
	assetLinks http.Handler

	limitsMu gosync.Mutex
	limits   map[mailKey]*rate.Limiter // токен и вид запроса → предел частоты ящика
	now      func() time.Time          // часы предела; тест их подменяет

	mu       gosync.Mutex
	handlers map[domain.UserID]http.Handler
}

func (t *tenants) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Страница телефона (#622) — статика без данных: без токена. Токен телефона
	// и ключ переписки приходят ей во фрагменте адреса QR.
	if r.URL.Path == "/m" || strings.HasPrefix(r.URL.Path, "/m/") {
		t.phonePage.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/.well-known/assetlinks.json" {
		t.assetLinks.ServeHTTP(w, r)
		return
	}
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
		side, device := domain.MailDesk, ""
		id, ok := t.reg.Lookup(tok)
		if !ok {
			id, device, ok = t.phones.LookupDevice(tok)
			ok = ok && t.reg.Has(id) // телефон входит, пока жив хозяин (ревью #630)
			side = domain.MailPhone
		}
		if ok {
			k := mailKey{sum: sha256.Sum256([]byte(tok)), poll: r.Method == http.MethodGet && r.URL.Path == "/api/mail"}
			if wait, ok := t.allowMail(k); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				http.Error(w, "too many mailbox requests; poll every 10 s", http.StatusTooManyRequests)
				return
			}
			ctx := httpapi.WithMailDevice(httpapi.WithMailCaller(r.Context(), id, side), device)
			t.mail.ServeHTTP(w, r.WithContext(ctx))
			return
		}
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="kbengine"`)
	http.Error(w, "bearer token required", http.StatusUnauthorized)
}

// Предел частоты ящика на токен (у каждого телефона свой) и вид запроса:
// опрос и отправка не делят корзину — досылка очереди не глушит опрос (ревью #631).
// Опрос: договорной темп раз в 10 с, предел вдвое чаще, сразу — 10 страниц
// истории (2000 / DefaultPage) и запас. Отправка и телефоны: раз в секунду.
//
// ponytail: предел — в памяти экземпляра; при нескольких экземплярах он
// умножается на их число. Путь апгрейда — предел в API Gateway. Сломанный
// клиент с after=0 все равно читает журнал целиком раз в 5 с (до 8 МБ) —
// потолок снимет нарезка журнала (mailbox.DefaultMaxBytes).
var (
	mailPollRate = rate.Every(5 * time.Second)
	mailSendRate = rate.Limit(1)
)

const (
	mailPollBurst = 12
	mailSendBurst = 30
	mailLimitsCap = 4096 // корзин сверх этого — повод выбросить простаивающие
)

type mailKey struct {
	sum  [sha256.Size]byte // хэш токена: сам токен в памяти предела не держим
	poll bool
}

// allowMail takes a token from the key's bucket or says how long to wait.
func (t *tenants) allowMail(k mailKey) (time.Duration, bool) {
	now := t.now()
	t.limitsMu.Lock()
	if len(t.limits) >= mailLimitsCap {
		// Полная корзина неотличима от новой — выбросить ее без потерь.
		maps.DeleteFunc(t.limits, func(_ mailKey, l *rate.Limiter) bool {
			return l.TokensAt(now) >= float64(l.Burst())
		})
	}
	l, ok := t.limits[k]
	if !ok {
		l = rate.NewLimiter(mailSendRate, mailSendBurst)
		if k.poll {
			l = rate.NewLimiter(mailPollRate, mailPollBurst)
		}
		t.limits[k] = l
	}
	t.limitsMu.Unlock()
	res := l.ReserveN(now, 1)
	if wait := res.DelayFrom(now); wait > 0 {
		res.CancelAt(now)
		return wait, false
	}
	return 0, true
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

// lazyPusher reads the VAPID key on the first push call, not at start: сервер
// без бакета под рукой (тесты, локальный serve) стартует, а сбой чтения ключа
// повторится на следующем вызове, а не останется навсегда.
type lazyPusher struct {
	store     blob.Store
	contact   string
	transport http.RoundTripper

	mu     gosync.Mutex
	vapid  *webpush.VAPID
	sender *webpush.Sender
}

func (l *lazyPusher) load(ctx context.Context) (*webpush.VAPID, *webpush.Sender, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sender == nil {
		v, err := webpush.LoadVAPID(ctx, l.store, l.contact)
		if err != nil {
			return nil, nil, err
		}
		l.vapid, l.sender = v, webpush.NewSender(v, l.transport, time.Now)
	}
	return l.vapid, l.sender, nil
}

func (l *lazyPusher) Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error {
	_, s, err := l.load(ctx)
	if err != nil {
		return err
	}
	return s.Send(ctx, sub, payload)
}

func (l *lazyPusher) PublicKey(ctx context.Context) (string, error) {
	v, _, err := l.load(ctx)
	if err != nil {
		return "", err
	}
	return v.PublicKey(), nil
}
