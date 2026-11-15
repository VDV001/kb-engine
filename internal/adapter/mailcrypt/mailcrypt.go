// Package mailcrypt seals phone chat messages end to end (#622).
package mailcrypt

import (
	"errors"

	"github.com/daniil/kb-engine/internal/domain"
)

var (
	ErrFormat       = errors.New("mailcrypt: malformed")
	ErrNotAuthentic = errors.New("mailcrypt: message is not authentic")
)

const MaxPlain = 0

type Key [32]byte

func NewKey() (Key, error)         { return Key{}, nil }
func ParseKey(string) (Key, error) { return Key{}, nil }
func (k Key) Encode() string       { return "" }

func Seal(Key, string, domain.MailSide, uint64, []byte) ([]byte, error) { return nil, nil }

func sealWithNonce(Key, string, domain.MailSide, uint64, []byte, []byte) ([]byte, error) {
	return nil, nil
}

func Open(Key, string, domain.MailSide, []byte) (uint64, []byte, error) { return 0, nil, nil }
