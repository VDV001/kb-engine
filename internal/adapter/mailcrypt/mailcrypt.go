// Package mailcrypt seals phone chat messages end to end (#622). Заготовка
// API формата после ревью #632: поведения нет, его вносит следующий коммит.
package mailcrypt

import (
	"errors"

	"github.com/daniil/kb-engine/internal/domain"
)

var (
	// ErrFormat means: байты не похожи на запечатанное сообщение или ключ.
	ErrFormat = errors.New("mailcrypt: malformed")
	// ErrWrongKey means: сообщение закрыто другим ключом.
	ErrWrongKey = errors.New("mailcrypt: sealed with another key")
	// ErrNotAuthentic means: сообщение, чат, сторону, отправителя или счетчик правили.
	ErrNotAuthentic = errors.New("mailcrypt: message is not authentic")
)

const (
	// Overhead is what sealing adds to the text.
	Overhead = 33 + 16
	// MaxPlain is the longest text whose sealed form fits the mailbox.
	MaxPlain = domain.MaxMailSealed - Overhead
)

// Key is the chat key.
type Key struct{ s *[32]byte }

// KeyID names a key in a sealed message.
type KeyID [4]byte

// Sender names the device that sealed a message.
type Sender [8]byte

// Meta is what a sealed message is bound to.
type Meta struct {
	Chat    string
	From    domain.MailSide
	Sender  Sender
	Counter uint64
}

// NewKey returns a random key.
func NewKey() Key { return Key{} }

// ParseKey reads Encode's form.
func ParseKey(string) (Key, error) { return Key{}, nil }

// Encode is the key as text.
func (k Key) Encode() string { return "" }

// ID names the key.
func (k Key) ID() KeyID { return KeyID{} }

// Equal compares keys.
func (k Key) Equal(Key) bool { return false }

// KeyIDOf reads the key id of a sealed message.
func KeyIDOf([]byte) (KeyID, error) { return KeyID{}, nil }

// Seal encrypts plain.
func Seal(Key, Meta, []byte) ([]byte, error) { return nil, nil }

func sealWithNonce(Key, Meta, []byte, []byte) ([]byte, error) { return nil, nil }

// Open checks and decrypts.
func Open(Key, string, domain.MailSide, []byte) (Meta, []byte, error) { return Meta{}, nil, nil }
