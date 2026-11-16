// Package webpush sends a Web Push message to a phone (#622): шифрование тела
// по RFC 8291 (aes128gcm, RFC 8188) и подпись сервера VAPID (RFC 8292).
//
// Текст уведомления читает только телефон: сервис push (Google, Apple,
// Mozilla, Microsoft) видит адрес и байты. Сервер шлет в уведомлении лишь
// «Компьютер ответил» — переписку он не знает и знать не должен.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrTooLong — текст не помещается в одну запись 4096 байт.
var ErrTooLong = errors.New("webpush: message is too long")

// ErrGone — сервис push забыл подписку (404, 410): ее пора удалить.
var ErrGone = errors.New("webpush: subscription is gone")

// ErrRejected — сервис push не принял сообщение.
var ErrRejected = errors.New("webpush: push service rejected the message")

// ErrBadVAPID — ключ сервера не P-256 или контакт не mailto:/https:.
var ErrBadVAPID = errors.New("webpush: bad VAPID key or subject")

// Тело — одна запись: заголовок 86 байт (соль 16, rs 4, длина ключа 1, ключ
// 65), текст, разделитель 0x02 и тег GCM 16. Сервисы push берут до 4096 байт.
const (
	recordSize   = 4096
	headerSize   = 16 + 4 + 1 + 65
	maxPlaintext = recordSize - headerSize - 1 - 16
)

// Encrypt seals plaintext for the subscription with a fresh key and salt.
func Encrypt(sub domain.PushSubscription, plaintext []byte) ([]byte, error) {
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(sub, plaintext, as, salt)
}

// encrypt is RFC 8291 section 3 with the sender's key and salt given.
func encrypt(sub domain.PushSubscription, plaintext []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > maxPlaintext {
		return nil, ErrTooLong
	}
	ua, err := ecdh.P256().NewPublicKey(sub.P256dh())
	if err != nil {
		// Домен проверил только форму: точка вне кривой видна лишь здесь.
		return nil, fmt.Errorf("webpush: phone key: %w", err)
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	info := append(append([]byte("WebPush: info\x00"), ua.Bytes()...), asPub...)
	ikm, err := hkdf.Key(sha256.New, secret, sub.Auth(), string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, headerSize+len(plaintext)+1+16)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asPub)))
	out = append(out, asPub...)
	return g.Seal(out, nonce, append(append([]byte(nil), plaintext...), 0x02), nil), nil
}

// VAPID signs requests to push services as this server.
type VAPID struct {
	key     *ecdsa.PrivateKey
	public  string // несжатая точка, base64url — k= в заголовке
	subject string
}

// NewVAPID takes the server's P-256 key and a contact: mailto: или https:.
func NewVAPID(key *ecdsa.PrivateKey, subject string) (*VAPID, error) {
	if key == nil || key.Curve != elliptic.P256() {
		return nil, ErrBadVAPID
	}
	if u, err := url.Parse(subject); err != nil || (u.Scheme != "mailto" && u.Scheme != "https") || (u.Opaque == "" && u.Host == "") {
		return nil, ErrBadVAPID
	}
	pub, err := key.PublicKey.Bytes()
	if err != nil {
		return nil, ErrBadVAPID
	}
	return &VAPID{key: key, public: base64.RawURLEncoding.EncodeToString(pub), subject: subject}, nil
}

// vapidLife — срок подписи: RFC 8292 разрешает до 24 ч, половина оставляет
// запас на расхождение часов с сервисом push.
const vapidLife = 12 * time.Hour

// Header is the Authorization value for a request to endpoint.
func (v *VAPID) Header(endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("webpush: endpoint: %w", ErrRejected)
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}{u.Scheme + "://" + u.Host, now.Add(vapidLife).Unix(), v.subject})
	if err != nil {
		return "", err
	}
	signing := head + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, v.key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64) // JWS ES256: r||s по 32 байта, не DER
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + base64.RawURLEncoding.EncodeToString(sig) + ", k=" + v.public, nil
}

// Sender posts encrypted messages to push services.
type Sender struct {
	vapid  *VAPID
	client *http.Client
	now    func() time.Time
}

// NewSender builds a sender; rt nil means http.DefaultTransport.
func NewSender(v *VAPID, rt http.RoundTripper, now func() time.Time) *Sender {
	return &Sender{vapid: v, now: now, client: &http.Client{
		Transport: rt,
		// Адрес прислал телефон, домен пропустил только сервисы push; ответ
		// «302 → куда угодно» не исполняем, иначе проверка ничего не стоит.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// pushTTL — сколько сервис push держит сообщение для спящего телефона.
const pushTTL = "3600"

// Send encrypts payload and posts it; the deadline is the caller's ctx.
func (s *Sender) Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error {
	body, err := Encrypt(sub, payload)
	if err != nil {
		return err
	}
	auth, err := s.vapid.Header(sub.Endpoint(), s.now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", pushTTL)
	req.Header.Set("Urgency", "high")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	default:
		// Тело ответа в ошибку не берем: его пишет чужой сервис.
		return fmt.Errorf("%w: %s", ErrRejected, strings.TrimSpace(resp.Status))
	}
}
