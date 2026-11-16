// Package mailcrypt seals phone chat messages end to end (#622): ключ знают
// только окно и привязанные телефоны, kb-сервер хранит и отдает байты.
//
// Формат запечатанного, версия 1 (его же повторяет телефон на WebCrypto):
//
//	версия (1) | id ключа (4) | отправитель (8) | счетчик (8, big-endian) | нонс (12) | AES-256-GCM: текст и тег (16)
//
// AAD = "kbmail\x00" + весь заголовок (33 байта) + чат + "\x00" + сторона.
// Чат и сторону сервер видит открыто, но подменить их, как и любое поле
// заголовка, не может: Open откажет — ErrNotAuthentic, а за правку версии и
// id ключа — ErrFormat и ErrWrongKey. Id ключа — первые 4 байта
// SHA-256("kbmail key id\x00" + ключ): получатель, у которого после смены
// ключа остались старые, выбирает нужный, не перебирая (KeyIDOf). ErrWrongKey
// значит «ключа с этим id у меня нет», а не «ключ сменился»: id мог подменить
// сервер, и повода менять ключ в этом нет.
//
// Счетчик пишется во все 8 байт big-endian: на JS — BigInt и setBigUint64
// (второй контрольный вектор держит это). Нонс случайный: при потере
// состояния телефон начнет счетчик заново, и нонс из счетчика повторился бы.
// Предел случайного нонса — около 2^32 сообщений на ключ (NIST SP 800-38D).
//
// Отправитель — 8 случайных байт устройства, счетчик у каждого свой. Сам пакет
// повторы не ловит: Open примет то же сообщение дважды. Пропуски и повторы
// видит получатель, который помнит последний счетчик по ключу (сторона,
// отправитель) в чате (работник окна, этап 3 #622); до него сервер может
// повторить или удержать сообщение, но не подделать и не прочесть. Отправителя
// выбирает само устройство: держатель ключа может назваться чужим — защита
// от сервера, не друг от друга.
package mailcrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/daniil/kb-engine/internal/domain"
)

var (
	// ErrFormat means: байты не похожи на запечатанное сообщение или ключ.
	ErrFormat = errors.New("mailcrypt: malformed")
	// ErrWrongKey means: сообщение закрыто другим ключом (id в заголовке не тот).
	ErrWrongKey = errors.New("mailcrypt: sealed with another key")
	// ErrNotAuthentic means: сообщение, чат, сторону, отправителя или счетчик правили.
	ErrNotAuthentic = errors.New("mailcrypt: message is not authentic")
	// ErrKeySerialized means: ключ попытались выдать в JSON.
	ErrKeySerialized = errors.New("mailcrypt: the key is never serialized; use Encode on purpose")
)

const (
	version    = 1
	nonceSize  = 12
	headerSize = 1 + 4 + 8 + 8 + nonceSize
	// Overhead is what sealing adds to the text.
	Overhead = headerSize + 16
	// MaxPlain is the longest text whose sealed form fits the mailbox.
	MaxPlain = domain.MaxMailSealed - Overhead
)

// Key is the chat key. Байты лежат в замыкании: fmt печатает функцию адресом
// и внутрь не заглядывает — даже вложенным полем и неподходящим глаголом
// (указатель не спасал: %!s(*T=&{…}) печатал байты). Сам Key печатается
// заглушкой; json и slog его не выдают.
type Key struct{ s func() *keyState }

type keyState struct {
	raw  [32]byte
	id   KeyID
	aead cipher.AEAD // собирается один раз: ключ живет долго
}

// KeyID names a key in a sealed message.
type KeyID [4]byte

// Sender names the device that sealed a message: 8 случайных байт, свои у
// окна и у каждого телефона.
type Sender [8]byte

// Meta is what a sealed message is bound to.
type Meta struct {
	Chat    string
	From    domain.MailSide
	Sender  Sender
	Counter uint64
}

// NewKey returns a random key.
func NewKey() Key {
	var raw [32]byte
	_, _ = rand.Read(raw[:]) // с Go 1.24 Read не возвращает ошибку
	return newKey(raw)
}

// NewSender returns a random sender id for a device.
func NewSender() Sender {
	var s Sender
	_, _ = rand.Read(s[:])
	return s
}

func newKey(raw [32]byte) Key {
	block, err := aes.NewCipher(raw[:])
	if err != nil {
		panic(err) // длина задана типом: 32 байта
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(append([]byte("kbmail key id\x00"), raw[:]...))
	st := &keyState{raw: raw, id: KeyID(sum[:4]), aead: g}
	return Key{func() *keyState { return st }}
}

// ParseKey reads Encode's form.
func ParseKey(s string) (Key, error) {
	// Длина до разбора: Strict в stdlib пропускает \r и \n, а телефон их не примет.
	if len(s) != keyTextLen {
		return Key{}, fmt.Errorf("%w: key", ErrFormat)
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != 32 {
		return Key{}, fmt.Errorf("%w: key", ErrFormat)
	}
	return newKey([32]byte(b)), nil
}

// Encode is base64url without padding: так ключ ложится во фрагмент адреса QR.
func (k Key) Encode() string {
	if k.s == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(k.s().raw[:])
}

// ID names the key; у пустого ключа — нули.
func (k Key) ID() KeyID {
	if k.s == nil {
		return KeyID{}
	}
	return k.s().id
}

// Equal compares keys in constant time.
func (k Key) Equal(o Key) bool {
	if k.s == nil || o.s == nil {
		return k.s == nil && o.s == nil
	}
	return subtle.ConstantTimeCompare(k.s().raw[:], o.s().raw[:]) == 1
}

const (
	redacted   = "mailcrypt.Key(…)"
	keyTextLen = 43 // base64url 32 байт без «=»
)

// Format prints a placeholder for every verb: %v, %d, %x — ключа не видно.
func (Key) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redacted) }

// LogValue keeps the key out of slog.
func (Key) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON refuses: ключ не уходит в JSON даже случайно.
func (Key) MarshalJSON() ([]byte, error) { return nil, ErrKeySerialized }

// KeyIDOf reads which key sealed the message.
func KeyIDOf(sealed []byte) (KeyID, error) {
	if len(sealed) < Overhead || sealed[0] != version {
		return KeyID{}, fmt.Errorf("%w: sealed message", ErrFormat)
	}
	return KeyID(sealed[1:5]), nil
}

func aad(header []byte, chat string, from domain.MailSide) []byte {
	b := make([]byte, 0, 7+len(header)+len(chat)+1+len(from))
	b = append(b, "kbmail\x00"...)
	b = append(b, header...)
	b = append(b, chat...)
	b = append(b, 0)
	return append(b, from...)
}

// Seal encrypts plain for the meta: чат и сторона проверяются до шифрования,
// иначе сервер отбил бы их после того, как отправитель потратил счетчик.
func Seal(k Key, m Meta, plain []byte) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	_, _ = rand.Read(nonce)
	return sealWithNonce(k, m, plain, nonce)
}

func sealWithNonce(k Key, m Meta, plain, nonce []byte) ([]byte, error) {
	if k.s == nil {
		return nil, fmt.Errorf("%w: no key", ErrFormat)
	}
	if err := domain.ValidateMailChat(m.Chat); err != nil {
		return nil, err
	}
	if _, err := domain.ParseMailSide(string(m.From)); err != nil {
		return nil, err
	}
	if len(plain) == 0 {
		return nil, domain.ErrMailEmpty // запечатанным пустое весит 49 байт, и сервер бы его пропустил
	}
	if len(plain) > MaxPlain {
		return nil, fmt.Errorf("%w: %d bytes of text, limit %d", domain.ErrMailTooLarge, len(plain), MaxPlain)
	}
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("%w: nonce", ErrFormat)
	}
	out := make([]byte, headerSize, Overhead+len(plain))
	out[0] = version
	st := k.s()
	copy(out[1:5], st.id[:])
	copy(out[5:13], m.Sender[:])
	binary.BigEndian.PutUint64(out[13:21], m.Counter)
	copy(out[21:headerSize], nonce)
	return st.aead.Seal(out, nonce, plain, aad(out, m.Chat, m.From)), nil
}

// Open checks and decrypts a message the server says came to chat from the side.
func Open(k Key, chat string, from domain.MailSide, sealed []byte) (Meta, []byte, error) {
	id, err := KeyIDOf(sealed)
	switch {
	case err != nil:
		return Meta{}, nil, err
	case k.s == nil:
		return Meta{}, nil, fmt.Errorf("%w: no key", ErrFormat)
	case id != k.s().id:
		return Meta{}, nil, ErrWrongKey
	}
	header := sealed[:headerSize]
	plain, err := k.s().aead.Open(nil, header[21:], sealed[headerSize:], aad(header, chat, from))
	if err != nil {
		return Meta{}, nil, ErrNotAuthentic
	}
	m := Meta{Chat: chat, From: from, Sender: Sender(header[5:13]), Counter: binary.BigEndian.Uint64(header[13:21])}
	return m, plain, nil
}

// Файл ящика (#658), версия 1: версия (1) | id ключа (4) | нонс (12) | AES-256-GCM: файл и тег (16).
// AAD = "kbmail file\x00" + заголовок: сообщение не откроется как файл, и
// наоборот. С сообщением в журнале файл связывает хеш, который несет само
// сообщение: подмену куска сервером видит получатель.
const (
	fileHeaderSize = 1 + 4 + nonceSize
	// FileOverhead is what sealing adds to a file piece.
	FileOverhead = fileHeaderSize + 16
	// MaxFilePlain is the longest piece whose sealed form fits domain.MaxMailFile.
	MaxFilePlain = domain.MaxMailFile - FileOverhead
)

// SealFile encrypts one file piece.
func SealFile(k Key, plain []byte) ([]byte, error) {
	switch {
	case k.s == nil:
		return nil, fmt.Errorf("%w: no key", ErrFormat)
	case len(plain) == 0:
		return nil, domain.ErrMailEmpty
	case len(plain) > MaxFilePlain:
		return nil, fmt.Errorf("%w: %d bytes of file, limit %d", domain.ErrMailTooLarge, len(plain), MaxFilePlain)
	}
	st := k.s()
	out := make([]byte, fileHeaderSize, FileOverhead+len(plain))
	out[0] = version
	copy(out[1:5], st.id[:])
	_, _ = rand.Read(out[5:fileHeaderSize])
	return st.aead.Seal(out, out[5:fileHeaderSize], plain, fileAAD(out)), nil
}

// OpenFile checks and decrypts one file piece.
func OpenFile(k Key, sealed []byte) ([]byte, error) {
	switch {
	case len(sealed) < FileOverhead || sealed[0] != version:
		return nil, fmt.Errorf("%w: sealed file", ErrFormat)
	case k.s == nil:
		return nil, fmt.Errorf("%w: no key", ErrFormat)
	case KeyID(sealed[1:5]) != k.s().id:
		return nil, ErrWrongKey
	}
	header := sealed[:fileHeaderSize]
	plain, err := k.s().aead.Open(nil, header[5:], sealed[fileHeaderSize:], fileAAD(header))
	if err != nil {
		return nil, ErrNotAuthentic
	}
	return plain, nil
}

func fileAAD(header []byte) []byte {
	return append([]byte("kbmail file\x00"), header[:fileHeaderSize]...)
}
