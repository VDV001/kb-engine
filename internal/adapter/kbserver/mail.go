package kbserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNoMailbox — у сервера нет ящика чата с телефона (#622): одна база без
// людей (не serve --tenants) или сервер старше ящика. Адрес и токен верные.
var ErrNoMailbox = errors.New("kbserver: no mailbox")

// Phone is a phone paired with the person's mailbox.
type Phone struct {
	Device string    `json:"device"`
	At     time.Time `json:"at"`
}

// AddPhone pairs a new phone and returns its device id and token. Токен
// телефона пускает только в ящик; окно кладет его в QR и больше не хранит.
func (c Client) AddPhone(ctx context.Context) (string, string, error) {
	var out struct{ Device, Token string }
	if err := c.mail(ctx, http.MethodPost, "/api/mail/devices", http.StatusCreated, ErrNoMailbox, &out); err != nil {
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
	if err := c.mail(ctx, http.MethodGet, "/api/mail/devices", http.StatusOK, ErrNoMailbox, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// RevokePhone unpairs the phone: его токен перестает открывать ящик сразу.
func (c Client) RevokePhone(ctx context.Context, device string) error {
	// 404 здесь — «такого телефона нет», а не «нет ящика»: путь ящика сервер знает.
	return c.mail(ctx, http.MethodDelete, "/api/mail/devices/"+url.PathEscape(device), http.StatusNoContent,
		fmt.Errorf("%w: no such phone", ErrRejected), nil)
}

// mail makes one mailbox request; want — код успеха, notFound — что значит 404.
func (c Client) mail(ctx context.Context, method, path string, want int, notFound error, into any) error {
	target, err := url.JoinPath(c.URL, path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	resp, err := c.send(ctx, method, target, path, nil, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readLimited(resp, path, maxBody)
	if err != nil {
		return err
	}
	switch code := resp.StatusCode; code {
	case want:
	case http.StatusNotFound:
		return notFound
	case http.StatusBadRequest, http.StatusConflict:
		// Слова сервера: «too many phones» — человеку есть что сделать.
		return fmt.Errorf("%w: %s", ErrRejected, strings.TrimSpace(string(body)))
	default:
		if err := StatusErr(path, code); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s answered %d", ErrNotKB, path, code)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotKB, path, err)
	}
	return nil
}
