package main

// Телефоны чата (#622): окно привязывает телефон к ящику своего kb-сервера QR-кодом,
// показывает привязанные и отвязывает. Ключ чата рождается здесь и живет в
// Связке ключей; на сервер уходит только токен телефона, ключ — во фрагменте
// «#» адреса QR, который браузер телефона серверу не шлет.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
)

// mailTimeout bounds one pairing step: привязка ждет человека, а не сеть.
const mailTimeout = 15 * time.Second

var errMailNoServer = errors.New("kb-сервера с таким id нет в «Источниках» — обновите экран")

// phonePairing is what the window shows: картинка QR, без токена и ключа текстом.
type phonePairing struct {
	Device string `json:"device"`
	QR     string `json:"qr"` // data:image/png;base64,…
}

// phoneRow is a paired phone in the list.
type phoneRow struct {
	Device string `json:"device"`
	At     string `json:"at"` // «15.11.2026 12:40», местное время
}

// mailKeyAccount is the keychain account of the chat key of server id.
func mailKeyAccount(id string) string { return "mail:" + id + ":key" }

func (f *folderSettings) mailClient(id string) (kbserver.Client, error) {
	url := f.savedURL(id)
	if url == "" {
		return kbserver.Client{}, errMailNoServer
	}
	hc := f.mailHTTP
	if hc == nil {
		hc = &http.Client{}
	}
	return kbserver.Client{URL: url, Token: f.token(id), HTTP: hc}, nil
}

func mailErr(err error) error { return humanErr{"kb-сервер: " + serverMessage(err), err} }

// mailKey returns the chat key of server id, creating it on first pairing;
// created — ключ новый, и откат обязан его убрать.
func (f *folderSettings) mailKey(id string) (mailcrypt.Key, bool, error) {
	acct := mailKeyAccount(id)
	stored, err := f.tokens.Get(acct)
	switch {
	case err == nil:
		k, err := mailcrypt.ParseKey(stored)
		if err != nil {
			return mailcrypt.Key{}, false, fmt.Errorf("ключ чата в Связке ключей испорчен — отвяжите все телефоны и привяжите заново: %w", err)
		}
		return k, false, nil
	case !errors.Is(err, keychain.ErrNotFound):
		return mailcrypt.Key{}, false, humanErr{keychainNote, err}
	}
	k := mailcrypt.NewKey()
	if err := f.tokens.Set(acct, k.Encode()); err != nil {
		return mailcrypt.Key{}, false, humanErr{keychainNote, err}
	}
	return k, true, nil
}

// MailPairPhone pairs a new phone with the mailbox of server id and returns
// its QR. Сбой на любом шаге откатывает то, что этот вызов создал: новый ключ и
// токен телефона на сервере; ключ, которым читают уже привязанные, не трогается.
func (f *folderSettings) MailPairPhone(id string) (phonePairing, error) {
	f.mailMu.Lock() // две привязки разом не родят два ключа
	defer f.mailMu.Unlock()
	c, err := f.mailClient(id)
	if err != nil {
		return phonePairing{}, err
	}
	key, created, err := f.mailKey(id)
	if err != nil {
		return phonePairing{}, err
	}
	undoKey := func() {
		if created {
			_ = f.tokens.Delete(mailKeyAccount(id))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()
	dev, tok, err := c.AddPhone(ctx)
	if err != nil {
		undoKey()
		return phonePairing{}, mailErr(err)
	}
	draw := f.qr
	if draw == nil {
		draw = qrPNG
	}
	png, err := draw(strings.TrimRight(c.URL, "/") + "/m/#pair=" + tok + "." + key.Encode())
	if err != nil {
		_ = c.RevokePhone(ctx, dev) // токен, которого никто не увидит, не должен открывать ящик
		undoKey()
		return phonePairing{}, err
	}
	return phonePairing{Device: dev, QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}, nil
}

// MailPhones lists the phones paired with the mailbox of server id.
func (f *folderSettings) MailPhones(id string) ([]phoneRow, error) {
	c, err := f.mailClient(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()
	list, err := c.Phones(ctx)
	if err != nil {
		return nil, mailErr(err)
	}
	rows := make([]phoneRow, 0, len(list))
	for _, p := range list {
		rows = append(rows, phoneRow{Device: p.Device, At: p.At.Local().Format("02.01.2006 15:04")})
	}
	return rows, nil
}

// MailRevokePhone unpairs the phone. Отвязан последний — ключ чата стирается:
// у отвязанного телефона он остался, и следующий телефон получит новый.
//
// ponytail: пока привязан хоть один телефон, ключ прежний — отвязанный может
// прочесть то, что ляжет в ящик дальше, если добудет и токен живого телефона.
// Потолок — отвязка одного из нескольких; путь — смена ключа с пересылкой
// нового живым телефонам (этап 3, когда у окна есть работник ящика).
func (f *folderSettings) MailRevokePhone(id, device string) error {
	f.mailMu.Lock()
	defer f.mailMu.Unlock()
	c, err := f.mailClient(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()
	if err := c.RevokePhone(ctx, device); err != nil {
		return mailErr(err)
	}
	left, err := c.Phones(ctx)
	if err != nil {
		return humanErr{"телефон отвязан, но список телефонов не прочитан — ключ чата пока прежний; обновите экран", err}
	}
	if len(left) == 0 {
		if err := f.tokens.Delete(mailKeyAccount(id)); err != nil && !errors.Is(err, keychain.ErrNotFound) {
			return humanErr{"телефон отвязан, но ключ чата не стерт из Связки ключей — " + keychainNote, err}
		}
	}
	return nil
}
