package main

// Работник ящика (#622, этап 3): раз в 10 с окно забирает новое из ящика
// каждого kb-сервера, к которому привязаны телефоны (есть ключ чата в Связке),
// и отвечает на вопросы телефона ходом модели — сама логика в
// usecase/mailrelay, здесь адаптеры: ящик, шифр, файл состояния, чат окна.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

// mailEvery — шаг опроса ящика: решение 15.11 (короткий опрос раз в 10 с;
// предел сервера — раз в 5 с).
const mailEvery = 10 * time.Second

// mailWorkerHTTP bounds one mailbox request: зависший запрос не держит круг.
var mailWorkerHTTP = &http.Client{Timeout: 30 * time.Second}

type askFn func(ctx context.Context, chat, question string) (string, string, error)

type mailWorker struct {
	settings *folderSettings
	ask      askFn
	dir      string            // где лежат mail-<id>.json
	changed  func(chat string) // ответ записан в чат окна: странице перечитать список и чат
	logf     func(string, ...any)
	creds    map[string]mailCreds // только горутина работника
}

// mailCreds is what the worker read from the keychain for a server: ключ чата
// и клиент с токеном. Связка — не каждые 10 с (два вызова security на сервер):
// заново после привязки и отвязки в этом окне (mailGen) и раз в mailCredsTTL.
type mailCreds struct {
	key    mailcrypt.Key
	client kbserver.Client
	none   bool // ключа нет: телефоны не привязаны
	gen    uint64
	at     time.Time
}

const mailCredsTTL = 5 * time.Minute

func newMailWorker(s *folderSettings, ask askFn, dir string) *mailWorker {
	return &mailWorker{settings: s, ask: ask, dir: dir, changed: func(string) {}, logf: log.Printf, creds: map[string]mailCreds{}}
}

// run ticks until ctx ends; круги не перекрываются — одна горутина.
func (w *mailWorker) run(ctx context.Context) {
	t := time.NewTicker(mailEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.tick(ctx); err != nil {
				w.logf("mail: %v", err)
			}
		}
	}
}

// tick answers every server with paired phones; сбой одного не держит другие.
func (w *mailWorker) tick(ctx context.Context) error {
	var errs []error
	for _, id := range w.servers() {
		if err := w.tickServer(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// servers lists the enabled kb servers of the sources.
func (w *mailWorker) servers() []string {
	w.settings.mu.Lock()
	defer w.settings.mu.Unlock()
	var ids []string
	for _, s := range w.settings.cfg.WithSources().Sources {
		if s.Kind == desktopconfig.KindServer && s.Enabled {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// credsFor returns the server's key and client, из памяти, пока Связка не менялась.
func (w *mailWorker) credsFor(id string) (mailCreds, error) {
	gen := w.settings.mailGen.Load()
	if c, ok := w.creds[id]; ok && c.gen == gen && time.Since(c.at) < mailCredsTTL {
		return c, nil
	}
	c := mailCreds{gen: gen, at: time.Now()}
	stored, err := w.settings.tokens.Get(mailKeyAccount(id))
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		c.none = true // телефоны не привязаны: сервер не опрашивается
		w.creds[id] = c
		return c, nil
	case err != nil:
		return c, fmt.Errorf("связка ключей: %w", err)
	}
	if c.key, err = mailcrypt.ParseKey(stored); err != nil {
		return c, fmt.Errorf("ключ чата испорчен: %w", err)
	}
	if c.client, err = w.settings.mailClient(id); err != nil {
		return c, err
	}
	if c.client.HTTP == nil {
		c.client.HTTP = mailWorkerHTTP
	}
	w.creds[id] = c
	return c, nil
}

// mailMaxText — предел текста ответа: JSON-обертка и экранирование (кавычки,
// переводы строк — до 2 байт на знак) не выводят шифротекст за предел ящика.
const mailMaxText = (mailcrypt.MaxPlain - 64) / 2

func (w *mailWorker) tickServer(ctx context.Context, id string) error {
	c, err := w.credsFor(id)
	if err != nil || c.none {
		return err
	}
	file := &mailStateFile{path: filepath.Join(w.dir, "mail-"+url.PathEscape(id)+".json")}
	sender, err := file.sender()
	if err != nil {
		return err
	}
	keyID := c.key.ID()
	relay := mailrelay.Relay{
		Box:     mailBox{c.client},
		Crypto:  mailSeal{key: c.key, sender: sender},
		Asker:   askerFunc(w.askAndTell),
		Store:   file,
		KeyID:   hex.EncodeToString(keyID[:]),
		MaxText: mailMaxText,
	}
	return relay.Tick(ctx)
}

func (w *mailWorker) askAndTell(ctx context.Context, chat, question string) (string, string, error) {
	id, answer, err := w.ask(ctx, chat, question)
	if err == nil {
		w.changed(id)
	}
	return id, answer, err
}

type askerFunc askFn

func (f askerFunc) Ask(ctx context.Context, chat, question string) (string, string, error) {
	return f(ctx, chat, question)
}

// chatAsker runs a quiet turn in the window's chat: без кусков на страницу —
// у человека может быть открыт другой чат. Чат окна удалили — новый: отказ
// чтения случается до хода модели, повтор не платный.
//
// ponytail: ход идет под общим замком чатов окна — пока модель отвечает
// телефону (до askTimeout), вопрос человека в окне ждет со своим «думаю».
// Путь апгрейда — замок на чат, а не на все чаты.
func chatAsker(d *chatDesk) askFn {
	return func(ctx context.Context, chat, question string) (string, string, error) {
		quiet := askOut{worker: true}
		rec, err := d.ask(ctx, askReq{ID: chat, Question: question}, quiet)
		if chat != "" && errors.Is(err, fs.ErrNotExist) {
			rec, err = d.ask(ctx, askReq{Question: question}, quiet)
		}
		if err != nil {
			return "", "", err
		}
		answer := ""
		if n := len(rec.Messages); n > 0 {
			answer = rec.Messages[n-1].Text
		}
		return rec.ID, answer, nil
	}
}

// mailBox is the kb server's mailbox for the relay.
type mailBox struct{ c kbserver.Client }

func (b mailBox) Since(ctx context.Context, after int64) (mailrelay.Page, error) {
	p, err := b.c.MailSince(ctx, after)
	if err != nil {
		return mailrelay.Page{}, err
	}
	out := mailrelay.Page{More: p.More}
	for _, m := range p.Messages {
		out.Messages = append(out.Messages, mailrelay.Message{Seq: m.Seq, Chat: m.Chat, From: domain.MailSide(m.From), At: m.At, Sealed: m.Sealed})
	}
	return out, nil
}

// Post marks a refusal for good (неверный чат, велико) as mailrelay.ErrRefused:
// такой ответ уходит из исходящих, а не затыкает ящик (ревью #635).
func (b mailBox) Post(ctx context.Context, chat string, sealed []byte) (int64, error) {
	seq, err := b.c.MailPost(ctx, chat, sealed)
	if errors.Is(err, kbserver.ErrRejected) {
		return 0, fmt.Errorf("%w: %w", mailrelay.ErrRefused, err)
	}
	return seq, err
}

// mailSeal opens with the chat key and seals as the window's sender.
type mailSeal struct {
	key    mailcrypt.Key
	sender mailcrypt.Sender
}

func (s mailSeal) Open(chat string, from domain.MailSide, sealed []byte) (mailrelay.Opened, error) {
	m, plain, err := mailcrypt.Open(s.key, chat, from, sealed)
	if err != nil {
		return mailrelay.Opened{}, err
	}
	return mailrelay.Opened{Sender: hex.EncodeToString(m.Sender[:]), Counter: m.Counter, Plain: plain}, nil
}

func (s mailSeal) Seal(chat string, counter uint64, plain []byte) ([]byte, error) {
	return mailcrypt.Seal(s.key, mailcrypt.Meta{Chat: chat, From: domain.MailDesk, Sender: s.sender, Counter: counter}, plain)
}

// mailStateFile keeps the relay state and the window's sender id per server;
// отправитель читается раз (sender), Save пишет файл, не перечитывая его.
type mailStateFile struct {
	path      string
	senderHex string
}

type mailStateDTO struct {
	Sender string          `json:"sender"` // hex 8 байт: отправитель окна в ящике
	State  mailrelay.State `json:"state"`
}

func (f *mailStateFile) read() (mailStateDTO, error) {
	var d mailStateDTO
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("%s: %w", f.path, err)
	}
	return d, nil
}

func (f *mailStateFile) write(d mailStateDTO) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path) // целиком или никак: номер ящика не теряется наполовину
}

// sender returns the window's sender id, создавая его при первом круге.
func (f *mailStateFile) sender() (mailcrypt.Sender, error) {
	d, err := f.read()
	if err != nil {
		return mailcrypt.Sender{}, err
	}
	var s mailcrypt.Sender
	if b, err := hex.DecodeString(d.Sender); err == nil && len(b) == len(s) {
		f.senderHex = d.Sender
		return mailcrypt.Sender(b), nil
	}
	s = mailcrypt.NewSender()
	f.senderHex = hex.EncodeToString(s[:])
	d.Sender = f.senderHex
	return s, f.write(d)
}

func (f *mailStateFile) Load() (mailrelay.State, error) {
	d, err := f.read()
	return d.State, err
}

func (f *mailStateFile) Save(st mailrelay.State) error {
	return f.write(mailStateDTO{Sender: f.senderHex, State: st})
}
