// Package webpush sends a Web Push message to a phone (#622): шифрование тела
// по RFC 8291 (aes128gcm, RFC 8188) и подпись сервера VAPID (RFC 8292).
//
// Текст уведомления читает только телефон: сервис push (Google, Apple,
// Mozilla, Microsoft) видит адрес и байты. Сервер шлет в уведомлении лишь
// «Компьютер ответил» — переписку он не знает и знать не должен.
package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"errors"
	"net/http"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrTooLong — текст не помещается в одну запись 4096 байт.
var ErrTooLong = errors.New("webpush: message is too long")

// ErrGone — сервис push забыл подписку (404, 410): ее пора удалить.
var ErrGone = errors.New("webpush: subscription is gone")

// ErrRejected — сервис push не принял сообщение.
var ErrRejected = errors.New("webpush: push service rejected the message")

// Encrypt seals plaintext for the subscription with a fresh key and salt.
func Encrypt(sub domain.PushSubscription, plaintext []byte) ([]byte, error) {
	return nil, errors.ErrUnsupported
}

func encrypt(sub domain.PushSubscription, plaintext []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	return nil, errors.ErrUnsupported
}

// VAPID signs requests to push services as this server.
type VAPID struct {
	key     *ecdsa.PrivateKey
	subject string
}

// NewVAPID takes the server's P-256 key and a contact: mailto: или https:.
func NewVAPID(key *ecdsa.PrivateKey, subject string) (*VAPID, error) {
	return nil, errors.ErrUnsupported
}

// Header is the Authorization value for a request to endpoint.
func (v *VAPID) Header(endpoint string, now time.Time) (string, error) {
	return "", errors.ErrUnsupported
}

// Sender posts encrypted messages to push services.
type Sender struct {
	vapid  *VAPID
	client *http.Client
	now    func() time.Time
}

// NewSender builds a sender; rt nil means http.DefaultTransport.
func NewSender(v *VAPID, rt http.RoundTripper, now func() time.Time) *Sender {
	return &Sender{vapid: v, now: now}
}

// Send encrypts payload and posts it; the deadline is the caller's ctx.
func (s *Sender) Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error {
	return errors.ErrUnsupported
}
