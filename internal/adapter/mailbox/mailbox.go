// Package mailbox keeps the phone chat log of each person (#622): телефон и
// компьютер дописывают шифротекст в users/<id>/mail/log.jsonl, номер дает
// журнал. Сервер не может прочитать ни строки: ключ шифрования есть только у
// телефона и окна.
package mailbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

// DefaultKeep is how many last messages a log keeps.
const DefaultKeep = 2000

// DefaultMaxBytes is the byte budget of a log: старые строки уходят, пока журнал
// не войдет в бюджет. Без него 767 сообщений по 64 КБ упирались в предел объекта
// хранилища (64 МБ), и ящик заклинивало навсегда (ревью #630).
//
// ponytail: журнал — один объект, и каждый опрос читает его целиком (до 8 МБ).
// Потолок — частый опрос длинной истории; путь апгрейда — GetIfChanged по ETag
// на человека и нарезка журнала по номерам (до выкатки на прод).
const DefaultMaxBytes = 8 << 20

// DefaultPage bounds one answer of Since.
const DefaultPage = 200

// Store reads and appends the logs in Blob.
type Store struct {
	Blob     blob.Store
	Keep     int // 0 — DefaultKeep
	MaxBytes int // 0 — DefaultMaxBytes
}

// Page is mail.Page: First больше «после» + 1 — часть истории ушла из журнала.
type Page = mail.Page

type lineDTO struct {
	Seq    int64     `json:"seq"`
	Chat   string    `json:"chat"`
	From   string    `json:"from"`
	At     time.Time `json:"at"`
	Sealed []byte    `json:"sealed"` // base64 в JSON
}

func key(id domain.UserID) string { return "users/" + id.String() + "/mail/log.jsonl" }

// Append writes m under the next number and returns it with the number.
func (s Store) Append(ctx context.Context, id domain.UserID, m domain.MailMessage) (domain.MailMessage, error) {
	keep, budget := s.Keep, s.MaxBytes
	if keep <= 0 {
		keep = DefaultKeep
	}
	if budget <= 0 {
		budget = DefaultMaxBytes
	}
	var out domain.MailMessage
	add := func(raw []byte) ([]byte, error) {
		doc, got, err := appendLine(raw, m, keep, budget)
		out = got
		return doc, err
	}
	first, err := add(nil)
	if err != nil {
		return domain.MailMessage{}, err
	}
	_, err = s.Blob.Put(ctx, key(id), first, "") // первый раз журнала нет: создаем
	// Создан кем-то или неизвестно, легла ли запись, — перечитать и решить заново.
	if errors.Is(err, blob.ErrConflict) || errors.Is(err, blob.ErrTransient) {
		err = blob.Update(ctx, s.Blob, key(id), add)
	}
	if err != nil {
		return domain.MailMessage{}, fmt.Errorf("mailbox: append: %w", err)
	}
	return out, nil
}

// appendLine adds m to log raw and returns the new log and m with its number;
// m уже в журнале — журнал как есть: повтор после сбоя хранилища или клиента
// не дублирует, шифротекст с нонсом не повторяется (ревью #630).
func appendLine(raw []byte, m domain.MailMessage, keep, budget int) ([]byte, domain.MailMessage, error) {
	lines, err := decode(raw)
	if err != nil {
		return nil, domain.MailMessage{}, err
	}
	for _, l := range lines {
		if l.Chat == m.Chat() && l.From == string(m.From()) && bytes.Equal(l.Sealed, m.Sealed()) {
			return raw, m.WithSeq(l.Seq), nil
		}
	}
	var last int64
	if len(lines) > 0 {
		last = lines[len(lines)-1].Seq
	}
	out := m.WithSeq(last + 1)
	lines = append(lines, lineDTO{Seq: out.Seq(), Chat: m.Chat(), From: string(m.From()), At: m.At().UTC(), Sealed: m.Sealed()})
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	for { // бюджет байт: старое уходит, последнее остается всегда
		doc, err := encode(lines)
		if err != nil || len(doc) <= budget || len(lines) == 1 {
			return doc, out, err
		}
		lines = lines[1:]
	}
}

// Since returns up to limit messages after number after, по всем чатам; 0 — DefaultPage.
func (s Store) Since(ctx context.Context, id domain.UserID, after int64, limit int) (Page, error) {
	if limit <= 0 {
		limit = DefaultPage
	}
	p := Page{Messages: []domain.MailMessage{}}
	o, err := s.Blob.Get(ctx, key(id))
	if errors.Is(err, blob.ErrNotFound) {
		return p, nil
	}
	if err != nil {
		return Page{}, fmt.Errorf("mailbox: read: %w", err)
	}
	lines, err := decode(o.Data)
	if err != nil {
		return Page{}, err
	}
	if len(lines) > 0 {
		p.First = lines[0].Seq
	}
	for _, l := range lines {
		if l.Seq <= after {
			continue
		}
		if len(p.Messages) == limit {
			p.More = true
			break
		}
		m, err := domain.RestoreMailMessage(l.Seq, l.Chat, domain.MailSide(l.From), l.Sealed, l.At)
		if err != nil {
			continue // строка не наша (сторона неизвестна) — не роняет ящик
		}
		p.Messages = append(p.Messages, m)
	}
	return p, nil
}

func decode(raw []byte) ([]lineDTO, error) {
	var lines []lineDTO
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(nil, 4*domain.MaxMailSealed) // base64 и поля строки
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l lineDTO
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("mailbox: broken log line: %w", err)
		}
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mailbox: read log: %w", err)
	}
	return lines, nil
}

func encode(lines []lineDTO) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}
