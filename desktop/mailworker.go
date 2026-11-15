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
	dir      string // где лежат mail-<id>.json
	changed  func() // ответ записан в чат окна: странице перечитать список
	logf     func(string, ...any)
}

func newMailWorker(s *folderSettings, ask askFn, dir string) *mailWorker {
	return &mailWorker{settings: s, ask: ask, dir: dir, changed: func() {}, logf: log.Printf}
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

func (w *mailWorker) tickServer(ctx context.Context, id string) error {
	stored, err := w.settings.tokens.Get(mailKeyAccount(id))
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		return nil // телефоны не привязаны: сервер не опрашивается
	case err != nil:
		return fmt.Errorf("связка ключей: %w", err)
	}
	key, err := mailcrypt.ParseKey(stored)
	if err != nil {
		return fmt.Errorf("ключ чата испорчен: %w", err)
	}
	c, err := w.settings.mailClient(id)
	if err != nil {
		return err
	}
	if c.HTTP == nil {
		c.HTTP = mailWorkerHTTP
	}
	file := &mailStateFile{path: filepath.Join(w.dir, "mail-"+url.PathEscape(id)+".json")}
	sender, err := file.sender()
	if err != nil {
		return err
	}
	relay := mailrelay.Relay{
		Box:    mailBox{c},
		Crypto: mailSeal{key: key, sender: sender},
		Asker:  askerFunc(w.askAndTell),
		Store:  file,
	}
	return relay.Tick(ctx)
}

func (w *mailWorker) askAndTell(ctx context.Context, chat, question string) (string, string, error) {
	id, answer, err := w.ask(ctx, chat, question)
	if err == nil {
		w.changed()
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
func chatAsker(d *chatDesk) askFn {
	return func(_ context.Context, chat, question string) (string, string, error) {
		rec, err := d.ask(askReq{ID: chat, Question: question}, nil, nil)
		if chat != "" && errors.Is(err, fs.ErrNotExist) {
			rec, err = d.ask(askReq{Question: question}, nil, nil)
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
		out.Messages = append(out.Messages, mailrelay.Message{Seq: m.Seq, Chat: m.Chat, From: domain.MailSide(m.From), Sealed: m.Sealed})
	}
	return out, nil
}

func (b mailBox) Post(ctx context.Context, chat string, sealed []byte) (int64, error) {
	return b.c.MailPost(ctx, chat, sealed)
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

// mailStateFile keeps the relay state and the window's sender id per server.
type mailStateFile struct{ path string }

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
		return mailcrypt.Sender(b), nil
	}
	s = mailcrypt.NewSender()
	d.Sender = hex.EncodeToString(s[:])
	return s, f.write(d)
}

func (f *mailStateFile) Load() (mailrelay.State, error) {
	d, err := f.read()
	return d.State, err
}

func (f *mailStateFile) Save(st mailrelay.State) error {
	d, err := f.read()
	if err != nil {
		return err
	}
	d.State = st
	return f.write(d)
}
