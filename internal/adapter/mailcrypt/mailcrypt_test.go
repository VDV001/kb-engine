package mailcrypt_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
	"github.com/daniil/kb-engine/internal/domain"
)

// keyBytes is the vector's key: байты 00..1f, не секрет.
var keyBytes = func() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}()

var keyText = base64.RawURLEncoding.EncodeToString(keyBytes)

func testKey(t *testing.T) mailcrypt.Key {
	t.Helper()
	k, err := mailcrypt.ParseKey(keyText)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var sender = mailcrypt.Sender{0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58}

func meta(chat string, from domain.MailSide, counter uint64) mailcrypt.Meta {
	return mailcrypt.Meta{Chat: chat, From: from, Sender: sender, Counter: counter}
}

// Вектор посчитан WebCrypto в Node (тот же API, что у телефона): ключ 00..1f,
// отправитель 51..58, счетчик 7, нонс a0..ab, чат c-1, сторона phone.
const (
	golden   = "0162e400aa51525354555657580000000000000007a0a1a2a3a4a5a6a7a8a9aaab36a7adad9573d20db2d056512b5a84883b2660a75009881d7981425d4404bb281f"
	goldenID = "62e400aa"
)

func TestSealMatchesWebCrypto(t *testing.T) {
	k := testKey(t)
	if id := k.ID(); hex.EncodeToString(id[:]) != goldenID {
		t.Errorf("key id %x, want %s", id, goldenID)
	}
	nonce := []byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab}
	got, err := mailcrypt.SealWithNonce(k, meta("c-1", domain.MailPhone, 7), []byte("привет, DVK"), nonce)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != golden {
		t.Fatalf("sealed\n got %x\nwant %s", got, golden)
	}
	raw, _ := hex.DecodeString(golden)
	m, plain, err := mailcrypt.Open(k, "c-1", domain.MailPhone, raw)
	if err != nil || m != meta("c-1", domain.MailPhone, 7) || string(plain) != "привет, DVK" {
		t.Fatalf("open golden: %+v %q %v", m, plain, err)
	}
}

// Второй вектор — счетчик во все 8 байт: телефон на JS обязан писать его
// BigInt (setBigUint64), младшие 32 бита (setUint32) совпали бы на счетчике 7.
const goldenWide = "0162e400aa51525354555657580102030405060708a0a1a2a3a4a5a6a7a8a9aaab3799adaa957ed33db3e2576bd7c06f1f71b66ce2927efc6db417f0225e71"

func TestSealWideCounterMatchesWebCrypto(t *testing.T) {
	nonce := []byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab}
	got, err := mailcrypt.SealWithNonce(testKey(t), meta("c-1", domain.MailPhone, 0x0102030405060708), []byte("счетчик"), nonce)
	if err != nil || hex.EncodeToString(got) != goldenWide {
		t.Fatalf("sealed %x, %v\nwant %s", got, err, goldenWide)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := testKey(t)
	a, err := mailcrypt.Seal(k, meta("c-1", domain.MailDesk, 42), []byte("итог задачи"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := mailcrypt.Seal(k, meta("c-1", domain.MailDesk, 42), []byte("итог задачи"))
	if bytes.Equal(a, b) {
		t.Error("two seals of the same text are equal: nonce is not random")
	}
	m, plain, err := mailcrypt.Open(k, "c-1", domain.MailDesk, a)
	if err != nil || m != meta("c-1", domain.MailDesk, 42) || string(plain) != "итог задачи" {
		t.Fatalf("open: %+v %q %v", m, plain, err)
	}
}

// Сервер видит чат, сторону и байты; любая его правка — отказ, а не другой текст.
// Заголовок: версия 0, id ключа 1-4, отправитель 5-12, счетчик 13-20, нонс 21-32.
func TestOpenRefusesTampering(t *testing.T) {
	k := testKey(t)
	sealed, err := mailcrypt.Seal(k, meta("c-1", domain.MailPhone, 7), []byte("секрет"))
	if err != nil || len(sealed) < mailcrypt.Overhead {
		t.Fatalf("seal: %d bytes, %v", len(sealed), err)
	}
	other := mailcrypt.NewKey()
	flip := func(i int) []byte { b := bytes.Clone(sealed); b[i] ^= 1; return b }
	cases := []struct {
		name   string
		key    mailcrypt.Key
		chat   string
		from   domain.MailSide
		sealed []byte
		want   error
	}{
		{"another chat", k, "c-2", domain.MailPhone, sealed, mailcrypt.ErrNotAuthentic},
		{"another side", k, "c-1", domain.MailDesk, sealed, mailcrypt.ErrNotAuthentic},
		{"another key", other, "c-1", domain.MailPhone, sealed, mailcrypt.ErrWrongKey},
		{"key id changed", k, "c-1", domain.MailPhone, flip(1), mailcrypt.ErrWrongKey},
		{"sender changed", k, "c-1", domain.MailPhone, flip(5), mailcrypt.ErrNotAuthentic},
		{"counter changed", k, "c-1", domain.MailPhone, flip(20), mailcrypt.ErrNotAuthentic},
		{"nonce changed", k, "c-1", domain.MailPhone, flip(21), mailcrypt.ErrNotAuthentic},
		{"text changed", k, "c-1", domain.MailPhone, flip(len(sealed) - 20), mailcrypt.ErrNotAuthentic},
		{"tag changed", k, "c-1", domain.MailPhone, flip(len(sealed) - 1), mailcrypt.ErrNotAuthentic},
		{"unknown version", k, "c-1", domain.MailPhone, flip(0), mailcrypt.ErrFormat},
		{"truncated", k, "c-1", domain.MailPhone, sealed[:mailcrypt.Overhead-1], mailcrypt.ErrFormat},
		{"empty", k, "c-1", domain.MailPhone, nil, mailcrypt.ErrFormat},
		{"no key", mailcrypt.Key{}, "c-1", domain.MailPhone, sealed, mailcrypt.ErrFormat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, plain, err := mailcrypt.Open(c.key, c.chat, c.from, c.sealed)
			if !errors.Is(err, c.want) || plain != nil {
				t.Errorf("got %q, %v; want %v", plain, err, c.want)
			}
		})
	}
}

// Получатель держит старые ключи ради истории: id в заголовке говорит, каким
// ключом закрыто, до попытки открыть (ревью #632).
func TestKeyIDOf(t *testing.T) {
	k := testKey(t)
	sealed, _ := mailcrypt.Seal(k, meta("c-1", domain.MailPhone, 1), []byte("x"))
	id, err := mailcrypt.KeyIDOf(sealed)
	if err != nil || id != k.ID() {
		t.Fatalf("KeyIDOf: %x %v, want %x", id, err, k.ID())
	}
	if other := mailcrypt.NewKey(); other.ID() == k.ID() {
		t.Error("two random keys share an id")
	}
	if _, err := mailcrypt.KeyIDOf([]byte{1, 2}); !errors.Is(err, mailcrypt.ErrFormat) {
		t.Errorf("short: %v", err)
	}
}

// Неверные чат и сторона отбиваются до шифрования: сервер отбил бы их уже
// после того, как отправитель потратил счетчик (ревью #632).
func TestSealValidatesMeta(t *testing.T) {
	k := testKey(t)
	cases := []struct {
		name string
		m    mailcrypt.Meta
		want error
	}{
		{"upper case chat", meta("Chat", domain.MailPhone, 1), domain.ErrMailChat},
		{"reserved chat", meta("devices", domain.MailPhone, 1), domain.ErrMailChat},
		{"nul in chat", meta("a\x00b", domain.MailPhone, 1), domain.ErrMailChat},
		{"unknown side", meta("c-1", domain.MailSide("Phone"), 1), domain.ErrMailSide},
		{"nul in side", meta("c-1", domain.MailSide("phone\x00x"), 1), domain.ErrMailSide},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if s, err := mailcrypt.Seal(k, c.m, []byte("x")); !errors.Is(err, c.want) || s != nil {
				t.Errorf("got %d bytes, %v; want %v", len(s), err, c.want)
			}
		})
	}
	if _, err := mailcrypt.Seal(mailcrypt.Key{}, meta("c-1", domain.MailPhone, 1), []byte("x")); !errors.Is(err, mailcrypt.ErrFormat) {
		t.Errorf("zero key: %v, want ErrFormat", err)
	}
	// Пустое сообщение запечатанным весит 49 байт, и проверка сервера «пусто» его
	// бы пропустила: правило домена держит Seal (ревью #632).
	if s, err := mailcrypt.Seal(k, meta("c-1", domain.MailPhone, 1), nil); !errors.Is(err, domain.ErrMailEmpty) || s != nil {
		t.Errorf("empty text: %d bytes, %v; want ErrMailEmpty", len(s), err)
	}
}

// Запечатанное укладывается в предел ящика, иначе сервер отбил бы его после шифрования.
func TestSealRespectsMailboxLimit(t *testing.T) {
	k := testKey(t)
	s, err := mailcrypt.Seal(k, meta("c-1", domain.MailPhone, 1), make([]byte, mailcrypt.MaxPlain))
	if err != nil || len(s) != domain.MaxMailSealed {
		t.Fatalf("max plain: %d bytes, %v; want exactly %d", len(s), err, domain.MaxMailSealed)
	}
	if _, err := mailcrypt.Seal(k, meta("c-1", domain.MailPhone, 1), make([]byte, mailcrypt.MaxPlain+1)); !errors.Is(err, domain.ErrMailTooLarge) {
		t.Errorf("one byte over: %v, want ErrMailTooLarge", err)
	}
}

func TestKeyEncoding(t *testing.T) {
	k := mailcrypt.NewKey()
	enc := k.Encode()
	back, err := mailcrypt.ParseKey(enc)
	if err != nil || !back.Equal(k) || back.ID() != k.ID() {
		t.Fatalf("parse(encode): %v", err)
	}
	if testKey(t).Equal(k) || (mailcrypt.Key{}).Equal(k) {
		t.Error("different keys are equal")
	}
	if strings.ContainsAny(enc, "+/=") || len(enc) != 43 {
		t.Errorf("key %q is not 43 chars of base64url without padding: breaks the QR fragment", enc)
	}
	// Strict в stdlib пропускает \r и \n: телефон такого не примет, Go тоже не должен.
	for _, bad := range []string{"", "AAEC", enc + "AA", strings.Repeat("!", 43), enc + "\n", enc[:20] + "\r\n" + enc[20:]} {
		if _, err := mailcrypt.ParseKey(bad); !errors.Is(err, mailcrypt.ErrFormat) {
			t.Errorf("ParseKey(%q): %v, want ErrFormat", bad, err)
		}
	}
}

// Ключ не выходит ни одним путем печати: глаголы fmt, вложенное поле, JSON,
// slog (ревью #632: %d, вложенное поле, json и slog печатали все 32 байта).
func TestKeyIsNeverPrinted(t *testing.T) {
	k := testKey(t)
	leaks := func(out string) bool {
		return strings.Contains(out, keyText) || strings.Contains(out, keyText[:8]) ||
			strings.Contains(out, hex.EncodeToString(keyBytes)) ||
			strings.Contains(out, "0 1 2 3 4 5") || strings.Contains(out, "0,1,2,3,4,5") ||
			strings.Contains(out, "0x0, 0x1, 0x2")
	}
	type holder struct{ key mailcrypt.Key }
	type Exported struct{ Key mailcrypt.Key }
	for _, f := range []string{"%v", "%s", "%+v", "%#v", "%d", "%x", "%X", "%q"} {
		for _, v := range []any{k, &k, holder{k}, &holder{k}, Exported{k}, []mailcrypt.Key{k}} {
			if out := fmt.Sprintf(f, v); leaks(out) {
				t.Errorf("%s of %T prints the key: %s", f, v, out)
			}
		}
	}
	if b, err := json.Marshal(Exported{k}); !errors.Is(err, mailcrypt.ErrKeySerialized) {
		t.Errorf("json.Marshal: %s, %v; want ErrKeySerialized", b, err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "key", k, "holder", holder{k})
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "key", k, "holder", holder{k})
	if leaks(buf.String()) {
		t.Errorf("slog prints the key: %s", buf.String())
	}
}

// Файл ящика (#658): туда и обратно; сервер не прочтет и не подменит;
// чужой ключ и сообщение вместо файла — отказ; пустой и сверх предела — нет.
func TestSealFile(t *testing.T) {
	k := mailcrypt.NewKey()
	plain := []byte("RIFF....WAVEfmt голос")
	sealed, err := mailcrypt.SealFile(k, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain[:8]) || len(sealed) != len(plain)+mailcrypt.FileOverhead {
		t.Fatalf("sealed %d bytes, plain visible: %t", len(sealed), bytes.Contains(sealed, plain[:8]))
	}
	if got, err := mailcrypt.OpenFile(k, sealed); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open: %q, %v", got, err)
	}
	bad := bytes.Clone(sealed)
	bad[len(bad)-1] ^= 1
	if _, err := mailcrypt.OpenFile(k, bad); !errors.Is(err, mailcrypt.ErrNotAuthentic) {
		t.Errorf("tampered: %v", err)
	}
	if _, err := mailcrypt.OpenFile(mailcrypt.NewKey(), sealed); !errors.Is(err, mailcrypt.ErrWrongKey) {
		t.Errorf("other key: %v", err)
	}
	msg, err := mailcrypt.Seal(k, mailcrypt.Meta{Chat: "c", From: domain.MailPhone}, plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mailcrypt.OpenFile(k, msg); err == nil {
		t.Error("a message opened as a file")
	}
	if _, err := mailcrypt.SealFile(k, nil); !errors.Is(err, domain.ErrMailEmpty) {
		t.Errorf("empty: %v", err)
	}
	if _, err := mailcrypt.SealFile(k, make([]byte, mailcrypt.MaxFilePlain+1)); !errors.Is(err, domain.ErrMailTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if s, err := mailcrypt.SealFile(k, make([]byte, mailcrypt.MaxFilePlain)); err != nil || len(s) != domain.MaxMailFile {
		t.Errorf("at the limit: %d bytes, %v", len(s), err)
	}
}
