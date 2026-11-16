package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

// Ящик чата с телефона (#622). Кто пишет — человек и сторона — решает верхний
// слой по токену (WithMailCaller); обработчик берет их только из контекста, тело
// запроса сторону не выбирает.

type mailCallerKey struct{}

type mailCaller struct {
	id     domain.UserID
	side   domain.MailSide
	device string // телефон токена; у компьютера пусто
}

// WithMailCaller puts the person and the side of the token into ctx.
func WithMailCaller(ctx context.Context, id domain.UserID, side domain.MailSide) context.Context {
	return context.WithValue(ctx, mailCallerKey{}, mailCaller{id: id, side: side})
}

// WithMailDevice names the phone of the token (WithMailCaller уже в ctx):
// подписка на push — этого телефона, а не того, что назван в теле.
func WithMailDevice(ctx context.Context, device string) context.Context {
	c, ok := ctx.Value(mailCallerKey{}).(mailCaller)
	if !ok {
		return ctx
	}
	c.device = device
	return context.WithValue(ctx, mailCallerKey{}, c)
}

// maxMailBody bounds a request: шифротекст в base64 и поля JSON.
const maxMailBody = 2 * domain.MaxMailSealed

type mailDTO struct {
	Seq    int64     `json:"seq"`
	Chat   string    `json:"chat"`
	From   string    `json:"from"`
	At     time.Time `json:"at"`
	Sealed []byte    `json:"sealed"`
}

// MailHandler serves /api/mail; без WithMailCaller — 401.
func MailHandler(svc mail.Service, logf func(string, ...any)) http.Handler {
	h := mailRoutes{svc: svc, logf: logf}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/mail/devices", h.with(h.addPhone))
	mux.HandleFunc("GET /api/mail/devices", h.with(h.listPhones))
	mux.HandleFunc("DELETE /api/mail/devices/{device}", h.with(h.revokePhone))
	mux.HandleFunc("GET /api/mail/push/key", h.with(h.pushKey))
	mux.HandleFunc("PUT /api/mail/push", h.with(h.subscribe))
	mux.HandleFunc("DELETE /api/mail/push", h.with(h.unsubscribe))
	mux.HandleFunc("POST /api/mail/notify", h.with(h.notify))
	mux.HandleFunc("POST /api/mail/{chat}", h.with(h.post))
	mux.HandleFunc("GET /api/mail", h.with(h.since))
	return mux
}

type mailRoutes struct {
	svc  mail.Service
	logf func(string, ...any)
}

// with passes the caller from the context; нет его — 401.
func (h mailRoutes) with(next func(http.ResponseWriter, *http.Request, mailCaller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := r.Context().Value(mailCallerKey{}).(mailCaller)
		if !ok {
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		next(w, r, c)
	}
}

func (h mailRoutes) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrMailChat), errors.Is(err, domain.ErrMailEmpty), errors.Is(err, domain.ErrMailSide):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, domain.ErrMailTooLarge):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
	case errors.Is(err, domain.ErrPushOff):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, domain.ErrBadPushSubscription):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, domain.ErrMailDeskOnly), errors.Is(err, domain.ErrMailPhoneOnly):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, domain.ErrMailTooManyPhones):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, blob.ErrConflict):
		http.Error(w, "the mailbox kept changing under the write; retry", http.StatusConflict)
	case errors.Is(err, domain.ErrNoSuchUser):
		http.Error(w, "no such phone", http.StatusNotFound)
	default:
		h.logf("mail: %v", err) // причина — в журнал: в ней ключ хранилища
		http.Error(w, "mailbox unavailable", http.StatusServiceUnavailable)
	}
}

func (h mailRoutes) addPhone(w http.ResponseWriter, r *http.Request, c mailCaller) {
	dev, tok, err := h.svc.AddPhone(r.Context(), c.id, c.side)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]string{"device": dev, "token": tok})
}

func (h mailRoutes) listPhones(w http.ResponseWriter, r *http.Request, c mailCaller) {
	list, err := h.svc.ListPhones(r.Context(), c.id, c.side)
	if err != nil {
		h.fail(w, err)
		return
	}
	type phoneDTO struct {
		Device string    `json:"device"`
		At     time.Time `json:"at"`
	}
	out := make([]phoneDTO, 0, len(list))
	for _, p := range list {
		out = append(out, phoneDTO{Device: p.Device, At: p.At})
	}
	writeJSONStatus(w, http.StatusOK, map[string][]phoneDTO{"devices": out})
}

func (h mailRoutes) revokePhone(w http.ResponseWriter, r *http.Request, c mailCaller) {
	if err := h.svc.RevokePhone(r.Context(), c.id, c.side, r.PathValue("device")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h mailRoutes) post(w http.ResponseWriter, r *http.Request, c mailCaller) {
	var in struct {
		Sealed []byte `json:"sealed"` // base64; поле from не читается: сторону называет токен
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMailBody)).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "body over the mailbox limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "body: {\"sealed\": base64}", http.StatusBadRequest)
		return
	}
	m, err := h.svc.Post(r.Context(), c.id, c.side, r.PathValue("chat"), in.Sealed)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]int64{"seq": m.Seq()})
}

func (h mailRoutes) since(w http.ResponseWriter, r *http.Request, c mailCaller) {
	var after int64
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "after: number of the last seen message", http.StatusBadRequest)
			return
		}
		after = n
	}
	p, err := h.svc.Since(r.Context(), c.id, after, 0)
	if err != nil {
		h.fail(w, err)
		return
	}
	out := make([]mailDTO, 0, len(p.Messages))
	for _, m := range p.Messages {
		out = append(out, mailDTO{Seq: m.Seq(), Chat: m.Chat(), From: string(m.From()), At: m.At(), Sealed: m.Sealed()})
	}
	writeJSONStatus(w, http.StatusOK, struct {
		Messages []mailDTO `json:"messages"`
		First    int64     `json:"first"` // больше after+1 — часть истории ушла из журнала
		More     bool      `json:"more"`
	}{out, p.First, p.More})
}

// maxPushBody bounds a subscription: адрес до 2048 и два ключа.
const maxPushBody = 4 << 10

func (h mailRoutes) pushKey(w http.ResponseWriter, r *http.Request, _ mailCaller) {
	k, err := h.svc.PushKey(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]string{"key": k})
}

// subscribe takes PushSubscription.toJSON() of the browser: ключи — base64url.
func (h mailRoutes) subscribe(w http.ResponseWriter, r *http.Request, c mailCaller) {
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody)).Decode(&in); err != nil {
		http.Error(w, `body: PushSubscription.toJSON()`, http.StatusBadRequest)
		return
	}
	p256dh, err1 := decodeB64URL(in.Keys.P256dh)
	auth, err2 := decodeB64URL(in.Keys.Auth)
	if err1 != nil || err2 != nil {
		http.Error(w, "keys: base64url", http.StatusBadRequest)
		return
	}
	if err := h.svc.SubscribePush(r.Context(), c.id, c.side, c.device, in.Endpoint, p256dh, auth); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeB64URL reads base64url with or without padding (браузеры шлют без).
func decodeB64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

func (h mailRoutes) unsubscribe(w http.ResponseWriter, r *http.Request, c mailCaller) {
	if err := h.svc.UnsubscribePush(r.Context(), c.id, c.side, c.device); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h mailRoutes) notify(w http.ResponseWriter, r *http.Request, c mailCaller) {
	n, err := h.svc.Notify(r.Context(), c.id, c.side)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]int{"sent": n})
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// phoneName names a phone by its browser: модель из Client Hints (Chrome на
// Android после Accept-CH страницы /m/), иначе устройство из User-Agent.
func phoneName(userAgent, model string) string { return "" }
