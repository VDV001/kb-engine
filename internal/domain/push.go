package domain

import "errors"

// ErrBadPushSubscription — подписка телефона на push не принята: адрес не
// сервиса push или ключи не той длины.
var ErrBadPushSubscription = errors.New("push subscription is not valid")

// PushSubscription is a phone's Web Push subscription (#622): адрес сервиса
// push браузера и ключи шифрования сообщения. Сервер ходит только на адреса
// известных сервисов push — иначе подписка, присланная телефоном, заставила бы
// его сходить куда угодно, хоть во внутреннюю сеть облака.
type PushSubscription struct {
	endpoint     string
	p256dh, auth []byte
}

// NewPushSubscription checks the subscription the browser gave.
func NewPushSubscription(endpoint string, p256dh, auth []byte) (PushSubscription, error) {
	return PushSubscription{}, ErrBadPushSubscription
}

// Endpoint is the push service address.
func (s PushSubscription) Endpoint() string { return s.endpoint }

// P256dh is the browser's public key, несжатая точка P-256 (65 байт).
func (s PushSubscription) P256dh() []byte { return append([]byte(nil), s.p256dh...) }

// Auth is the browser's authentication secret (16 байт).
func (s PushSubscription) Auth() []byte { return append([]byte(nil), s.auth...) }
