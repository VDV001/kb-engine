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
	return kbserver.Client{URL: url, Token: f.token(id), HTTP: f.mailHTTP}, nil
}

func mailErr(err error) error { return humanErr{"kb-сервер: " + serverMessage(err), err} }

// mailKey returns the chat key for the next phone; created — ключ новый, и
// откат обязан его убрать. Телефонов на сервере нет — ключ всегда новый,
// как бы ни ушли прежние (отвязка без чтения списка, users revoke, другое окно,
// другой сервер под тем же id): иначе новый телефон получил бы ключ, который
// лежит в отвязанном (ревью #633). Испорченный ключ при нуле телефонов тоже
// просто заменяется.
func (f *folderSettings) mailKey(id string, phones int) (mailcrypt.Key, bool, error) {
	acct := mailKeyAccount(id)
	if phones > 0 {
		stored, err := f.tokens.Get(acct)
		switch {
		case errors.Is(err, keychain.ErrNotFound):
			return mailcrypt.Key{}, false, errors.New("ключа чата нет в Связке ключей, а телефоны привязаны — отвяжите их и привяжите заново")
		case err != nil:
			return mailcrypt.Key{}, false, humanErr{keychainNote, err}
		}
		k, err := mailcrypt.ParseKey(stored)
		if err != nil {
			return mailcrypt.Key{}, false, fmt.Errorf("ключ чата в Связке ключей испорчен — отвяжите все телефоны и привяжите заново: %w", err)
		}
		return k, false, nil
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
	draw := f.qr
	if draw == nil {
		if !qrAvailable() {
			return phonePairing{}, errQRUnsupported // до ключа и телефона: отказ известен заранее
		}
		draw = qrPNG
	}
	c, err := f.mailClient(id)
	if err != nil {
		return phonePairing{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()
	have, err := c.Phones(ctx)
	if err != nil {
		return phonePairing{}, mailErr(err)
	}
	key, created, err := f.mailKey(id, len(have))
	if err != nil {
		return phonePairing{}, err
	}
	undoKey := func() {
		if created {
			_ = f.tokens.Delete(mailKeyAccount(id))
		}
	}
	dev, tok, err := c.AddPhone(ctx)
	if err != nil {
		undoKey()
		return phonePairing{}, mailErr(err)
	}
	png, err := draw(strings.TrimRight(c.URL, "/") + "/m/#pair=" + tok + "." + key.Encode())
	if err != nil {
		// Токен, которого никто не увидит, не должен открывать ящик. Свой срок:
		// AddPhone мог потратить почти весь общий (ревью #633).
		undoCtx, undoCancel := context.WithTimeout(context.Background(), mailTimeout)
		defer undoCancel()
		if rerr := c.RevokePhone(undoCtx, dev); rerr != nil {
			return phonePairing{}, humanErr{fmt.Sprintf("QR не нарисован, и телефон %s остался привязан — отвяжите его в списке", dev), errors.Join(err, rerr)}
		}
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
