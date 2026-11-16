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

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNoMailbox — у сервера нет ящика чата с телефона (#622): одна база без
// людей (не serve --tenants) или сервер старше ящика. Адрес и токен верные.
var ErrNoMailbox = errors.New("kbserver: no mailbox")

// Отказы ящика по делу — обертки ErrRejected со своими словами в Describe:
// общий совет ErrRejected «обновите до одной версии» здесь неверен (ревью #633).
var (
	ErrMailRefused = fmt.Errorf("%w: mailbox", ErrRejected)
	ErrNoSuchPhone = fmt.Errorf("%w: no such phone", ErrRejected)
	// ErrBadPhoneName — сервер не принял имя телефона: пустое, длинное, непечатное.
	ErrBadPhoneName = fmt.Errorf("%w: bad phone name", ErrRejected)
)

// Phone is a phone paired with the person's mailbox.
type Phone struct {
	Device string    `json:"device"`
	At     time.Time `json:"at"`
	Seen   time.Time `json:"seen"` // ноль — телефон еще не входил
	Name   string    `json:"name"` // по браузеру телефона; пусто до первого входа
	// SeenKnown — сервер прочитал входы: ноль в Seen значит «не входил». Нет —
	// входы не прочитались или сервер старше #652.
	SeenKnown bool `json:"seen_known"`
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

// RenamePhone gives the phone the name the person chose.
func (c Client) RenamePhone(ctx context.Context, device, name string) error {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	return c.mail(ctx, mailReq{method: http.MethodPut, path: "/api/mail/devices/" + url.PathEscape(device) + "/name", body: body,
		want: http.StatusNoContent, notFound: ErrNoSuchPhone, unprocessable: ErrBadPhoneName}, nil)
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
	// Имя чата — до запроса: «files» и прочие занятые адреса — маршруты ящика.
	if err := domain.ValidateMailChat(chat); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrMailRefused, err)
	}
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

// ErrNoSuchMailFile — файла в ящике нет: забран, удален по сроку (#658).
var ErrNoSuchMailFile = fmt.Errorf("%w: no such mailbox file", ErrRejected)

// ErrNoMailFiles — у сервера ящик есть, а файлов нет: сервер старше #658 или
// собран без хранилища, которое удаляет.
var ErrNoMailFiles = errors.New("kbserver: no mailbox files")

// maxMailFile bounds the answer with a piece: domain.MaxMailFile и запас.
const maxMailFile = 4 << 20

// MailFilePut keeps a sealed file piece and returns its id (#658).
func (c Client) MailFilePut(ctx context.Context, sealed []byte) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.mail(ctx, mailReq{method: http.MethodPost, path: "/api/mail/files", body: sealed, raw: true, want: http.StatusCreated}, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("%w: /api/mail/files: no id", ErrNotKB)
	}
	return out.ID, nil
}

// MailFileGet reads a sealed file piece.
func (c Client) MailFileGet(ctx context.Context, id string) ([]byte, error) {
	var out []byte
	err := c.mail(ctx, mailReq{method: http.MethodGet, path: "/api/mail/files/" + url.PathEscape(id), want: http.StatusOK,
		notFound: ErrNoSuchMailFile, limit: maxMailFile}, &out)
	return out, err
}

// MailFileDelete removes a piece the desk has taken; удаленный — не ошибка.
func (c Client) MailFileDelete(ctx context.Context, id string) error {
	return c.mail(ctx, mailReq{method: http.MethodDelete, path: "/api/mail/files/" + url.PathEscape(id), want: http.StatusNoContent, notFound: ErrNoSuchMailFile}, nil)
}

// mailReq is one mailbox request: want — код успеха, notFound — что значит 404
// (nil — ErrNoMailbox), limit — предел ответа (0 — maxBody).
type mailReq struct {
	method, path  string
	query         url.Values
	body          []byte
	want          int
	notFound      error
	unprocessable error // 422 этого запроса: сервер понял запрос и отказал по делу
	limit         int64
	raw           bool // тело — байты файла, не JSON (#658)
}

// refusal names the error of answer code; ожидаемый код — nil.
func (r mailReq) refusal(code int, body []byte) error {
	switch code {
	case r.want:
		return nil
	case http.StatusNotFound:
		return cmp.Or(r.notFound, ErrNoMailbox)
	case http.StatusUnprocessableEntity:
		if r.unprocessable != nil {
			return fmt.Errorf("%w: %s", r.unprocessable, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("%w: %s", ErrMailRefused, strings.TrimSpace(string(body)))
	case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		// Слова сервера: «too many phones» — человеку есть что сделать.
		return fmt.Errorf("%w: %s", ErrMailRefused, strings.TrimSpace(string(body)))
	}
	if err := StatusErr(r.path, code); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s answered %d", ErrNotKB, r.path, code)
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
	switch {
	case r.raw:
		header = http.Header{"Content-Type": {"application/octet-stream"}}
	case r.body != nil:
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
	if err := r.refusal(resp.StatusCode, body); err != nil {
		return err
	}
	if into == nil {
		return nil
	}
	if b, ok := into.(*[]byte); ok { // кусок файла — как есть
		*b = body
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

// Notify asks the server to push the phones and returns how many got it;
// push на сервере выключен — 0 без ошибки.
func (c Client) Notify(ctx context.Context) (int, error) {
	var out struct {
		Sent int `json:"sent"`
	}
	err := c.mail(ctx, mailReq{method: http.MethodPost, path: "/api/mail/notify", want: http.StatusOK, notFound: errPushOff}, &out)
	// 400 — сервер до push: он принимает notify за чат без тела. Это тоже
	// «push выключен», а не сбой после каждого ответа (ревью #643).
	if errors.Is(err, errPushOff) || errors.Is(err, ErrMailRefused) {
		return 0, nil
	}
	return out.Sent, err
}

// errPushOff is 404 of notify: на сервере нет KB_PUSH_CONTACT.
var errPushOff = errors.New("kbserver: push is off on the server")
