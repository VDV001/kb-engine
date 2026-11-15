package kbserver

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNoMailbox — у сервера нет ящика чата с телефона (#622): одна база без
// людей (не serve --tenants) или сервер старше ящика. Адрес и токен верные.
var ErrNoMailbox = errors.New("kbserver: no mailbox")

// Отказы ящика по делу — обертки ErrRejected со своими словами в Describe:
// общий совет ErrRejected «обновите до одной версии» здесь неверен (ревью #633).
var (
	ErrMailRefused = fmt.Errorf("%w: mailbox", ErrRejected)
	ErrNoSuchPhone = fmt.Errorf("%w: no such phone", ErrRejected)
)

// Phone is a phone paired with the person's mailbox.
type Phone struct {
	Device string    `json:"device"`
	At     time.Time `json:"at"`
}

// AddPhone pairs a new phone and returns its device id and token. Токен
// телефона пускает только в ящик; окно кладет его в QR и больше не хранит.
func (c Client) AddPhone(ctx context.Context) (string, string, error) {
	var out struct{ Device, Token string }
	if err := c.mail(ctx, mailReq{method: http.MethodPost, path: "/api/mail/devices", want: http.StatusCreated}, &out); err != nil {
		return "", "", err
	}
	if out.Device == "" || out.Token == "" {
		return "", "", fmt.Errorf("%w: /api/mail/devices: no device or token", ErrNotKB)
	}
	return out.Device, out.Token, nil
}

// Phones lists the person's paired phones.
func (c Client) Phones(ctx context.Context) ([]Phone, error) {
	var out struct {
		Devices []Phone `json:"devices"`
	}
	if err := c.mail(ctx, mailReq{method: http.MethodGet, path: "/api/mail/devices", want: http.StatusOK}, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// RevokePhone unpairs the phone: его токен перестает открывать ящик сразу.
func (c Client) RevokePhone(ctx context.Context, device string) error {
	// 404 здесь — «такого телефона нет», а не «нет ящика»: путь ящика сервер знает.
	return c.mail(ctx, mailReq{method: http.MethodDelete, path: "/api/mail/devices/" + url.PathEscape(device), want: http.StatusNoContent,
		notFound: ErrNoSuchPhone}, nil)
}

// MailMessage is one sealed message of the mailbox; сторону назвал токен отправителя.
type MailMessage struct {
	Seq    int64     `json:"seq"`
	Chat   string    `json:"chat"`
	From   string    `json:"from"`
	At     time.Time `json:"at"`
	Sealed []byte    `json:"sealed"` // base64 в JSON
}

// MailPage is an answer of MailSince: First больше after+1 — часть истории
// ушла из журнала; More — за этой страницей есть еще.
type MailPage struct {
	Messages []MailMessage `json:"messages"`
	First    int64         `json:"first"`
	More     bool          `json:"more"`
}

// MailSince reads the person's mailbox after seq, по всем чатам.
func (c Client) MailSince(ctx context.Context, after int64) (MailPage, error) {
	var p MailPage
	err := c.mail(ctx, mailReq{method: http.MethodGet, path: "/api/mail", query: url.Values{"after": {strconv.FormatInt(after, 10)}},
		want: http.StatusOK, limit: maxSearchBody}, &p) // страница — до 200 шифротекстов по 64 КБ
	return p, err
}

// MailPost puts a sealed message into chat and returns its number in the log.
func (c Client) MailPost(ctx context.Context, chat string, sealed []byte) (int64, error) {
	body, err := json.Marshal(map[string][]byte{"sealed": sealed})
	if err != nil {
		return 0, err
	}
	var out struct {
		Seq int64 `json:"seq"`
	}
	if err := c.mail(ctx, mailReq{method: http.MethodPost, path: "/api/mail/" + url.PathEscape(chat), body: body, want: http.StatusCreated}, &out); err != nil {
		return 0, err
	}
	return out.Seq, nil
}

// mailReq is one mailbox request: want — код успеха, notFound — что значит 404
// (nil — ErrNoMailbox), limit — предел ответа (0 — maxBody).
type mailReq struct {
	method, path string
	query        url.Values
	body         []byte
	want         int
	notFound     error
	limit        int64
}

// mail makes one mailbox request.
func (c Client) mail(ctx context.Context, r mailReq, into any) error {
	target, err := url.JoinPath(c.URL, r.path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	var header http.Header
	if r.body != nil {
		header = http.Header{"Content-Type": {"application/json"}}
	}
	resp, err := c.send(ctx, r.method, target, r.path, r.body, header)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readLimited(resp, r.path, cmp.Or(r.limit, maxBody))
	if err != nil {
		return err
	}
	switch code := resp.StatusCode; code {
	case r.want:
	case http.StatusNotFound:
		return cmp.Or(r.notFound, ErrNoMailbox)
	case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge:
		// Слова сервера: «too many phones» — человеку есть что сделать.
		return fmt.Errorf("%w: %s", ErrMailRefused, strings.TrimSpace(string(body)))
	default:
		if err := StatusErr(r.path, code); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s answered %d", ErrNotKB, r.path, code)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotKB, r.path, err)
	}
	return nil
}

// describeMail is Describe for the mailbox: отказы по делу, без общего совета
// ErrRejected про версии (ревью #633).
func describeMail(err error) string {
	switch {
	case errors.Is(err, ErrNoSuchPhone):
		return "такого телефона нет: его уже отвязали — обновите список"
	case errors.Is(err, ErrMailRefused) && strings.Contains(err.Error(), "too many phones"):
		return "привязано наибольшее число телефонов — отвяжите ненужный"
	case errors.Is(err, ErrMailRefused):
		return "ящик kb-сервера отказал: " + strings.TrimSpace(strings.TrimPrefix(err.Error(), ErrMailRefused.Error()+":")) + " — повторите"
	default:
		return "на kb-сервере нет ящика для телефона — нужен сервер с людьми (serve --tenants), обновленный до версии с ящиком"
	}
}
