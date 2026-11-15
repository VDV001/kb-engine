// Package mailcrypt seals phone chat messages end to end (#622): ключ знают
// только окно и привязанные телефоны, kb-сервер хранит и отдает байты.
//
// Формат запечатанного (его же повторяет телефон на WebCrypto):
//
//	версия (1 байт, 1) | счетчик (8 байт, big-endian) | нонс (12) | AES-256-GCM (текст и тег 16)
//
// AAD = "kbmail1\x00" + чат + "\x00" + сторона + "\x00" + счетчик (8 байт).
// Чат и сторону сервер видит открыто, но подменить их, как и счетчик, не может:
// Open откажет. Счетчик ведет отправитель; пропуски и повторы в нем ловит
// получатель — удалить или переставить сообщения незаметно сервер не может.
package mailcrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/daniil/kb-engine/internal/domain"
)

var (
	// ErrFormat means: байты не похожи на запечатанное сообщение или ключ.
	ErrFormat = errors.New("mailcrypt: malformed")
	// ErrNotAuthentic means: ключ не тот, или сообщение, чат, сторону, счетчик правили.
	ErrNotAuthentic = errors.New("mailcrypt: message is not authentic")
)

const (
	version    = 1
	nonceSize  = 12
	headerSize = 1 + 8 + nonceSize
	// Overhead is what sealing adds to the text.
	Overhead = headerSize + 16
	// MaxPlain is the longest text whose sealed form fits the mailbox.
	MaxPlain = domain.MaxMailSealed - Overhead
)

// Key is the chat key. fmt его не печатает: в журнал ключ не попадет случайно.
type Key [32]byte

// NewKey returns a random key.
func NewKey() Key {
	var k Key
	_, _ = rand.Read(k[:]) // с Go 1.24 Read не возвращает ошибку
	return k
}

// ParseKey reads Encode's form.
func ParseKey(s string) (Key, error) {
	var k Key
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != len(k) {
		return k, fmt.Errorf("%w: key", ErrFormat)
	}
	copy(k[:], b)
	return k, nil
}

// Encode is base64url without padding: так ключ ложится во фрагмент адреса QR.
func (k Key) Encode() string { return base64.RawURLEncoding.EncodeToString(k[:]) }

// String hides the key from %v and %s.
func (Key) String() string { return "mailcrypt.Key(…)" }

// GoString hides the key from %#v.
func (Key) GoString() string { return "mailcrypt.Key(…)" }

func (k Key) aead() cipher.AEAD {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		panic(err) // длина ключа задана типом: 32 байта
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return g
}

func aad(chat string, from domain.MailSide, counter uint64) []byte {
	b := make([]byte, 0, 8+len(chat)+len(from)+2+8)
	b = append(b, "kbmail1\x00"...)
	b = append(b, chat...)
	b = append(b, 0)
	b = append(b, from...)
	b = append(b, 0)
	return binary.BigEndian.AppendUint64(b, counter)
}

// Seal encrypts plain for chat from the side with the sender's counter.
func Seal(k Key, chat string, from domain.MailSide, counter uint64, plain []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	_, _ = rand.Read(nonce)
	return sealWithNonce(k, chat, from, counter, plain, nonce)
}

func sealWithNonce(k Key, chat string, from domain.MailSide, counter uint64, plain, nonce []byte) ([]byte, error) {
	if len(plain) > MaxPlain {
		return nil, fmt.Errorf("%w: %d bytes of text, limit %d", domain.ErrMailTooLarge, len(plain), MaxPlain)
	}
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("%w: nonce", ErrFormat)
	}
	out := make([]byte, headerSize, Overhead+len(plain))
	out[0] = version
	binary.BigEndian.PutUint64(out[1:9], counter)
	copy(out[9:headerSize], nonce)
	return k.aead().Seal(out, nonce, plain, aad(chat, from, counter)), nil
}

// Open checks and decrypts a message the server says came to chat from the side.
func Open(k Key, chat string, from domain.MailSide, sealed []byte) (uint64, []byte, error) {
	if len(sealed) < Overhead || sealed[0] != version {
		return 0, nil, fmt.Errorf("%w: sealed message", ErrFormat)
	}
	counter := binary.BigEndian.Uint64(sealed[1:9])
	plain, err := k.aead().Open(nil, sealed[9:headerSize], sealed[headerSize:], aad(chat, from, counter))
	if err != nil {
		return 0, nil, ErrNotAuthentic
	}
	return counter, plain, nil
}
