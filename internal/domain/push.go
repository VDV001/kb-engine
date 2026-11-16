package domain

import (
	"errors"
	"net/url"
	"strings"
)

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
	if len(endpoint) > maxPushEndpoint || !knownPushService(endpoint) {
		return PushSubscription{}, ErrBadPushSubscription
	}
	if len(p256dh) != 65 || p256dh[0] != 0x04 || len(auth) != 16 {
		return PushSubscription{}, ErrBadPushSubscription
	}
	return PushSubscription{
		endpoint: endpoint,
		p256dh:   append([]byte(nil), p256dh...),
		auth:     append([]byte(nil), auth...),
	}, nil
}

// maxPushEndpoint — адреса сервисов push короче; длиннее — не подписка.
const maxPushEndpoint = 2048

// pushHosts — сервисы push браузеров: точное имя или суффикс с точкой впереди,
// чтобы «fcm.googleapis.com.evil» и «push.apple.com.evil» не прошли.
var pushHosts = []string{
	"fcm.googleapis.com",                // Chrome, Android
	".push.apple.com",                   // Safari
	"updates.push.services.mozilla.com", // Firefox
	".notify.windows.com",               // Edge
}

func knownPushService(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h || (h[0] == '.' && strings.HasSuffix(host, h)) {
			return true
		}
	}
	return false
}

// Endpoint is the push service address.
func (s PushSubscription) Endpoint() string { return s.endpoint }

// P256dh is the browser's public key, несжатая точка P-256 (65 байт).
func (s PushSubscription) P256dh() []byte { return append([]byte(nil), s.p256dh...) }

// Auth is the browser's authentication secret (16 байт).
func (s PushSubscription) Auth() []byte { return append([]byte(nil), s.auth...) }
