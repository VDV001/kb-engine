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
)

// DefaultKeep is how many last messages a log keeps.
//
// ponytail: журнал — один объект, и каждая запись перечитывает его целиком
// (до 2000 × 64 КБ в худшем случае). Потолок — длинная история; путь апгрейда —
// нарезка журнала по номерам.
const DefaultKeep = 2000

// Store reads and appends the logs in Blob.
type Store struct {
	Blob blob.Store
	Keep int // 0 — DefaultKeep
}

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
	keep := s.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	var out domain.MailMessage
	add := func(raw []byte) ([]byte, error) {
		lines, err := decode(raw)
		if err != nil {
			return nil, err
		}
		var last int64
		if len(lines) > 0 {
			last = lines[len(lines)-1].Seq
		}
		out = m.WithSeq(last + 1)
		lines = append(lines, lineDTO{Seq: out.Seq(), Chat: m.Chat(), From: string(m.From()), At: m.At().UTC(), Sealed: m.Sealed()})
		if len(lines) > keep {
			lines = lines[len(lines)-keep:]
		}
		return encode(lines)
	}
	first, err := add(nil)
	if err != nil {
		return domain.MailMessage{}, err
	}
	_, err = s.Blob.Put(ctx, key(id), first, "") // первый раз журнала нет: создаем
	if errors.Is(err, blob.ErrConflict) {
		err = blob.Update(ctx, s.Blob, key(id), add)
	}
	if err != nil {
		return domain.MailMessage{}, fmt.Errorf("mailbox: append: %w", err)
	}
	return out, nil
}

// Since returns the messages after number after, по всем чатам.
func (s Store) Since(ctx context.Context, id domain.UserID, after int64) ([]domain.MailMessage, error) {
	o, err := s.Blob.Get(ctx, key(id))
	if errors.Is(err, blob.ErrNotFound) {
		return []domain.MailMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mailbox: read: %w", err)
	}
	lines, err := decode(o.Data)
	if err != nil {
		return nil, err
	}
	out := []domain.MailMessage{}
	for _, l := range lines {
		if l.Seq <= after {
			continue
		}
		side, err := domain.ParseMailSide(l.From)
		if err != nil {
			return nil, fmt.Errorf("mailbox: line %d: %w", l.Seq, err)
		}
		m, err := domain.NewMailMessage(l.Chat, side, l.Sealed, l.At)
		if err != nil {
			return nil, fmt.Errorf("mailbox: line %d: %w", l.Seq, err)
		}
		out = append(out, m.WithSeq(l.Seq))
	}
	return out, nil
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
