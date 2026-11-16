package domain_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// Сервер ходит только к известным сервисам push: подписку присылает телефон, и
// без проверки адреса она заставила бы сервер сходить куда угодно (#622).
func TestNewPushSubscription(t *testing.T) {
	key := append([]byte{4}, bytes.Repeat([]byte{1}, 64)...)
	auth := bytes.Repeat([]byte{2}, 16)
	for name, c := range map[string]struct {
		endpoint     string
		p256dh, auth []byte
		ok           bool
	}{
		"Chrome (FCM)":          {"https://fcm.googleapis.com/fcm/send/abc:def", key, auth, true},
		"Safari (Apple)":        {"https://web.push.apple.com/QAbc", key, auth, true},
		"Firefox":               {"https://updates.push.services.mozilla.com/wpush/v2/abc", key, auth, true},
		"Edge (WNS)":            {"https://wns2-par02p.notify.windows.com/w/?token=abc", key, auth, true},
		"http":                  {"http://fcm.googleapis.com/fcm/send/abc", key, auth, false},
		"чужой хост":            {"https://evil.example/fcm/send/abc", key, auth, false},
		"хост с хвостом":        {"https://fcm.googleapis.com.evil.example/x", key, auth, false},
		"внутренняя сеть":       {"https://169.254.169.254/latest", key, auth, false},
		"логин в адресе":        {"https://user@fcm.googleapis.com/fcm/send/abc", key, auth, false},
		"чужой порт":            {"https://fcm.googleapis.com:8443/fcm/send/abc", key, auth, false},
		"ключ сжатый":           {"https://fcm.googleapis.com/fcm/send/abc", key[:33], auth, false},
		"ключ без 0x04":         {"https://fcm.googleapis.com/fcm/send/abc", append([]byte{3}, key[1:]...), auth, false},
		"секрет короткий":       {"https://fcm.googleapis.com/fcm/send/abc", key, auth[:8], false},
		"адрес длиннее предела": {"https://fcm.googleapis.com/" + strings.Repeat("a", 2100), key, auth, false},
		"apple без поддомена — не наш": {"https://push.apple.com.evil/x", key, auth, false},
	} {
		s, err := domain.NewPushSubscription(c.endpoint, c.p256dh, c.auth)
		if c.ok && (err != nil || s.Endpoint() != c.endpoint || !bytes.Equal(s.P256dh(), c.p256dh) || !bytes.Equal(s.Auth(), c.auth)) {
			t.Errorf("%s: %v, %+v", name, err, s)
		}
		if !c.ok && !errors.Is(err, domain.ErrBadPushSubscription) {
			t.Errorf("%s: принято (%v)", name, err)
		}
	}
}
