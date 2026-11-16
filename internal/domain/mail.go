package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Ящик чата с телефона (#622): телефон и компьютер дописывают сообщения в один
// журнал на сервере. Сервер видит только шифротекст; что можно положить в ящик,
// решает этот тип.

// Ошибки сообщения ящика.
var (
	ErrMailChat     = errors.New("mail: invalid chat id")
	ErrMailSide     = errors.New("mail: unknown side")
	ErrMailEmpty    = errors.New("mail: empty message")
	ErrMailTooLarge = errors.New("mail: message too large")
	// ErrMailDeskOnly — телефоны привязывает и отвязывает только компьютер:
	// украденный телефон не выпустит себе второй токен и не отвяжет хозяина.
	ErrMailDeskOnly = errors.New("mail: only the desk manages phones")
	// ErrMailTooManyPhones — у человека уже предельное число телефонов.
	ErrMailTooManyPhones = errors.New("mail: too many phones")
	// ErrMailPhoneOnly — на push подписывается сам телефон: подписка его.
	ErrMailPhoneOnly = errors.New("mail: only a phone subscribes to push")
	// ErrPushGone — сервис push забыл подписку (404, 410): ее пора удалить.
	ErrPushGone = errors.New("push subscription is gone")
	// ErrPushOff — push на этом сервере не настроен (нет контакта VAPID).
	ErrPushOff = errors.New("push is not configured")
)

// Имена телефона по браузеру, когда модель неизвестна: Safari модель не
// сообщает, iPad в Safari притворяется Mac (iPadOS 13+), урезанный Chrome пишет «K».
const (
	PhoneIPhone    = "iPhone"
	PhoneIPad      = "iPad"
	PhoneIPadOrMac = "iPad или Mac"
	PhoneAndroid   = "Android"
	PhoneWindows   = "Windows"
)

// BetterPhoneName is the name to keep when a phone named prev comes in as next.
func BetterPhoneName(prev, next string) string { return next }

// mailReserved — имена, занятые маршрутами ящика (/api/mail/devices, /push, /notify).
var mailReserved = map[string]bool{"devices": true, "push": true, "notify": true}

// MaxMailSealed bounds one sealed message: ход задачи и итог — текст, вложения
// в ящик не кладутся.
const MaxMailSealed = 64 << 10

// maxMailChat bounds the chat id: он стоит в каждой записи журнала.
const maxMailChat = 64

// MailSide is who wrote a message: телефон или компьютер. Сторону называет
// токен, которым пришел запрос, а не сам клиент.
type MailSide string

// Стороны ящика.
const (
	MailPhone MailSide = "phone"
	MailDesk  MailSide = "desk"
)

// ParseMailSide validates s.
func ParseMailSide(s string) (MailSide, error) {
	if side := MailSide(s); side == MailPhone || side == MailDesk {
		return side, nil
	}
	return "", fmt.Errorf("%w: %q", ErrMailSide, s)
}

// MailMessage is one sealed message of a chat.
type MailMessage struct {
	seq    int64
	chat   string
	from   MailSide
	sealed []byte
	at     time.Time
}

// ValidateMailChat checks a chat id. Чат — строчные латинские буквы, цифры и
// дефис: его выбирает клиент, и он не должен значить ничего, кроме имени.
func ValidateMailChat(chat string) error {
	if chat == "" || len(chat) > maxMailChat || mailReserved[chat] || slices.ContainsFunc([]rune(chat), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-'
	}) {
		return fmt.Errorf("%w: %q", ErrMailChat, chat)
	}
	return nil
}

// NewMailMessage validates a message before it gets its number in the log.
func NewMailMessage(chat string, from MailSide, sealed []byte, at time.Time) (MailMessage, error) {
	if err := ValidateMailChat(chat); err != nil {
		return MailMessage{}, err
	}
	if _, err := ParseMailSide(string(from)); err != nil {
		return MailMessage{}, err
	}
	switch {
	case len(sealed) == 0:
		return MailMessage{}, ErrMailEmpty
	case len(sealed) > MaxMailSealed:
		return MailMessage{}, fmt.Errorf("%w: %d bytes, limit %d", ErrMailTooLarge, len(sealed), MaxMailSealed)
	}
	return MailMessage{chat: chat, from: from, sealed: slices.Clone(sealed), at: at}, nil
}

// RestoreMailMessage rebuilds a message already in the log: правила входа (чат,
// размер) не перепроверяются — записанное по прежним правилам читается и после
// их ужесточения, и одна старая строка не роняет весь ящик (ревью #630).
func RestoreMailMessage(seq int64, chat string, from MailSide, sealed []byte, at time.Time) (MailMessage, error) {
	if _, err := ParseMailSide(string(from)); err != nil {
		return MailMessage{}, err
	}
	return MailMessage{seq: seq, chat: chat, from: from, sealed: sealed, at: at}, nil
}

// WithSeq is the message under its number in the log.
func (m MailMessage) WithSeq(seq int64) MailMessage { m.seq = seq; return m }

// Seq is the number in the log; 0 — еще не записано.
func (m MailMessage) Seq() int64 { return m.seq }

// Chat is the chat id.
func (m MailMessage) Chat() string { return m.chat }

// From is the side that wrote it.
func (m MailMessage) From() MailSide { return m.from }

// Sealed is the ciphertext.
func (m MailMessage) Sealed() []byte { return slices.Clone(m.sealed) }

// At is when the server took it.
func (m MailMessage) At() time.Time { return m.at }
