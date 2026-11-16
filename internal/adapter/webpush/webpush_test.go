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
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Вектор RFC 8291, раздел 5 и приложение A (взят из rfc-editor.org, пробелы
// переноса строк убраны). Ключи и секрет напечатаны в самом RFC.
const (
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg" // gitleaks:allow — публичный вектор RFC 8291, не секрет
	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcUAPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcBody      = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rfcSub(t *testing.T) domain.PushSubscription {
	t.Helper()
	sub, err := domain.NewPushSubscription("https://fcm.googleapis.com/fcm/send/x", b64(t, rfcUAPublic), b64(t, rfcAuth))
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestEncrypt_RFC8291Vector(t *testing.T) {
	as, err := ecdh.P256().NewPrivateKey(b64(t, rfcASPrivate))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encrypt(rfcSub(t), []byte(rfcPlaintext), as, b64(t, rfcSalt))
	if err != nil {
		t.Fatal(err)
	}
	if want := b64(t, rfcBody); !bytes.Equal(got, want) {
		t.Fatalf("тело не совпало с RFC 8291:\n got %s\nwant %s", base64.RawURLEncoding.EncodeToString(got), rfcBody)
	}
}

// decrypt is the phone's side of RFC 8291, independent of encrypt.
func decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	ua, err := ecdh.P256().NewPrivateKey(b64(t, rfcUAPrivate))
	if err != nil {
		t.Fatal(err)
	}
	salt, idlen := body[:16], int(body[20])
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ua.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPub.Bytes()...)
	ikm, err := hkdf.Key(sha256.New, secret, b64(t, rfcAuth), string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	g, _ := cipher.NewGCM(block)
	plain, err := g.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain[len(plain)-1] != 0x02 {
		t.Fatalf("нет разделителя последней записи: %x", plain)
	}
	return plain[:len(plain)-1]
}

func TestEncrypt_FreshKeyAndSalt(t *testing.T) {
	a, err := Encrypt(rfcSub(t), []byte("Компьютер ответил"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encrypt(rfcSub(t), []byte("Компьютер ответил"))
	if bytes.Equal(a[:16], b[:16]) || bytes.Equal(a[21:86], b[21:86]) {
		t.Fatal("соль или ключ повторились")
	}
	if got := decrypt(t, a); string(got) != "Компьютер ответил" {
		t.Fatalf("телефон прочел %q", got)
	}
}

func TestEncrypt_TooLong(t *testing.T) {
	if _, err := Encrypt(rfcSub(t), make([]byte, 3993)); err != nil {
		t.Fatalf("3993 байта помещаются в 4096: %v", err)
	}
	if _, err := Encrypt(rfcSub(t), make([]byte, 3994)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("3994 байта: %v", err)
	}
}

func newVAPID(t *testing.T) (*VAPID, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVAPID(key, "mailto:hi@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return v, key
}

func TestNewVAPID_Subject(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for _, s := range []string{"", "hi@example.com", "http://example.com"} {
		if _, err := NewVAPID(key, s); err == nil {
			t.Errorf("%q: принят", s)
		}
	}
	other, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if _, err := NewVAPID(other, "mailto:hi@example.com"); err == nil {
		t.Error("ключ P-384 принят")
	}
}

func TestVAPID_Header(t *testing.T) {
	v, key := newVAPID(t)
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	h, err := v.Header("https://fcm.googleapis.com/fcm/send/abc?x=1", now)
	if err != nil {
		t.Fatal(err)
	}
	tok, k, ok := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
	if !strings.HasPrefix(h, "vapid t=") || !ok {
		t.Fatalf("заголовок %q", h)
	}
	pub, _ := key.PublicKey.Bytes()
	if !bytes.Equal(b64(t, k), pub) {
		t.Fatal("k — не открытый ключ сервера")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT %q", tok)
	}
	var head map[string]string
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(b64(t, parts[0]), &head); err != nil || head["alg"] != "ES256" || head["typ"] != "JWT" {
		t.Fatalf("заголовок JWT %v %v", head, err)
	}
	if err := json.Unmarshal(b64(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != "https://fcm.googleapis.com" || claims.Sub != "mailto:hi@example.com" {
		t.Fatalf("claims %+v", claims)
	}
	// RFC 8292: не дальше 24 ч; берем 12 ч, чтобы расхождение часов не отбило.
	if claims.Exp != now.Add(12*time.Hour).Unix() {
		t.Fatalf("exp %d", claims.Exp)
	}
	sig := b64(t, parts[2])
	if len(sig) != 64 {
		t.Fatalf("подпись %d байт, а JWS ES256 — r||s по 32", len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("подпись не сходится")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(code int, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(""))}
}

func TestSender_Send(t *testing.T) {
	v, _ := newVAPID(t)
	var got *http.Request
	var body []byte
	s := NewSender(v, roundTrip(func(r *http.Request) (*http.Response, error) {
		got = r
		body, _ = io.ReadAll(r.Body)
		return reply(http.StatusCreated, nil), nil
	}), func() time.Time { return time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC) })
	if err := s.Send(context.Background(), rfcSub(t), []byte("Компьютер ответил")); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodPost || got.URL.String() != "https://fcm.googleapis.com/fcm/send/x" {
		t.Fatalf("%s %s", got.Method, got.URL)
	}
	for k, want := range map[string]string{
		"Content-Encoding": "aes128gcm",
		"Content-Type":     "application/octet-stream",
		"Urgency":          "high",
		"TTL":              "3600",
	} {
		if got.Header.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, got.Header.Get(k), want)
		}
	}
	if !strings.HasPrefix(got.Header.Get("Authorization"), "vapid t=") {
		t.Errorf("Authorization %q", got.Header.Get("Authorization"))
	}
	if string(decrypt(t, body)) != "Компьютер ответил" {
		t.Error("тело не читается телефоном")
	}
}

// Редирект сервиса push не уводит сервер на другой адрес: подписку прислал
// телефон, и проверка адреса в домене ничего бы не стоила, если бы ответ
// «302 → 169.254.169.254» исполнялся.
func TestSender_DoesNotFollowRedirects(t *testing.T) {
	v, _ := newVAPID(t)
	calls := 0
	s := NewSender(v, roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return reply(http.StatusFound, http.Header{"Location": {"http://169.254.169.254/latest"}}), nil
	}), time.Now)
	err := s.Send(context.Background(), rfcSub(t), []byte("x"))
	if calls != 1 || !errors.Is(err, ErrRejected) {
		t.Fatalf("запросов %d, err %v", calls, err)
	}
}

func TestSender_Status(t *testing.T) {
	v, _ := newVAPID(t)
	for code, want := range map[int]error{
		http.StatusOK:              nil,
		http.StatusCreated:         nil,
		http.StatusNotFound:        ErrGone,
		http.StatusGone:            ErrGone,
		http.StatusTooManyRequests: ErrRejected,
		http.StatusForbidden:       ErrRejected,
	} {
		s := NewSender(v, roundTrip(func(*http.Request) (*http.Response, error) { return reply(code, nil), nil }), time.Now)
		if err := s.Send(context.Background(), rfcSub(t), []byte("x")); !errors.Is(err, want) {
			t.Errorf("%d: %v, want %v", code, err, want)
		}
	}
}

func TestCheckSubject(t *testing.T) {
	for s, ok := range map[string]bool{"mailto:hi@example.com": true, "https://example.com": true, "": false, "admin@example.com": false, "http://example.com": false, "mailto:": false} {
		if err := CheckSubject(s); (err == nil) != ok {
			t.Errorf("CheckSubject(%q) = %v", s, err)
		}
	}
}

// Сетевой сбой не несет адрес подписки в журнал: адрес — ключ к чужому
// телефону (ревью безопасности #643).
func TestSender_ErrorHidesEndpoint(t *testing.T) {
	v, _ := newVAPID(t)
	s := NewSender(v, roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") }), time.Now)
	err := s.Send(context.Background(), rfcSub(t), []byte("x"))
	if err == nil || strings.Contains(err.Error(), "fcm/send") {
		t.Fatalf("err %v", err)
	}
}

// Ключ телефона вне кривой — подписка непригодна навсегда: ErrGone, чтобы ее
// стерли, а не повторяли каждым ответом (ревью безопасности #643).
func TestEncrypt_KeyOffCurveIsGone(t *testing.T) {
	sub, err := domain.NewPushSubscription("https://fcm.googleapis.com/fcm/send/x", append([]byte{4}, bytes.Repeat([]byte{1}, 64)...), bytes.Repeat([]byte{2}, 16))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Encrypt(sub, []byte("x")); !errors.Is(err, ErrGone) {
		t.Fatalf("err %v", err)
	}
}
