package mailcrypt_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
	"github.com/daniil/kb-engine/internal/domain"
)

func testKey(t *testing.T) mailcrypt.Key {
	t.Helper()
	k, err := mailcrypt.ParseKey("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Вектор посчитан WebCrypto в Node (тот же API, что у телефона): ключ 00..1f,
// нонс a0..ab, чат c-1, сторона phone, счетчик 7. Go обязан дать те же байты.
const golden = "010000000000000007a0a1a2a3a4a5a6a7a8a9aaab36a7adad9573d20db2d056512b5a84883bcf901904705a4740d6254c6a926f60cb"

func TestSealMatchesWebCrypto(t *testing.T) {
	nonce := []byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab}
	got, err := mailcrypt.SealWithNonce(testKey(t), "c-1", domain.MailPhone, 7, []byte("привет, DVK"), nonce)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != golden {
		t.Fatalf("sealed\n got %x\nwant %s", got, golden)
	}
	raw, _ := hex.DecodeString(golden)
	ctr, plain, err := mailcrypt.Open(testKey(t), "c-1", domain.MailPhone, raw)
	if err != nil || ctr != 7 || string(plain) != "привет, DVK" {
		t.Fatalf("open golden: %d %q %v", ctr, plain, err)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := testKey(t)
	a, err := mailcrypt.Seal(k, "c-1", domain.MailDesk, 42, []byte("итог задачи"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := mailcrypt.Seal(k, "c-1", domain.MailDesk, 42, []byte("итог задачи"))
	if bytes.Equal(a, b) {
		t.Error("two seals of the same text are equal: nonce is not random")
	}
	ctr, plain, err := mailcrypt.Open(k, "c-1", domain.MailDesk, a)
	if err != nil || ctr != 42 || string(plain) != "итог задачи" {
		t.Fatalf("open: %d %q %v", ctr, plain, err)
	}
}

// Сервер видит чат, сторону и байты; любая его правка — отказ, а не другой текст.
func TestOpenRefusesTampering(t *testing.T) {
	k := testKey(t)
	sealed, err := mailcrypt.Seal(k, "c-1", domain.MailPhone, 7, []byte("секрет"))
	if err != nil || len(sealed) < 40 {
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
		{"another key", other, "c-1", domain.MailPhone, sealed, mailcrypt.ErrNotAuthentic},
		{"counter changed", k, "c-1", domain.MailPhone, flip(8), mailcrypt.ErrNotAuthentic},
		{"nonce changed", k, "c-1", domain.MailPhone, flip(9), mailcrypt.ErrNotAuthentic},
		{"text changed", k, "c-1", domain.MailPhone, flip(len(sealed) - 20), mailcrypt.ErrNotAuthentic},
		{"tag changed", k, "c-1", domain.MailPhone, flip(len(sealed) - 1), mailcrypt.ErrNotAuthentic},
		{"unknown version", k, "c-1", domain.MailPhone, flip(0), mailcrypt.ErrFormat},
		{"truncated", k, "c-1", domain.MailPhone, sealed[:20], mailcrypt.ErrFormat},
		{"empty", k, "c-1", domain.MailPhone, nil, mailcrypt.ErrFormat},
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

// Запечатанное укладывается в предел ящика, иначе сервер отбил бы его после шифрования.
func TestSealRespectsMailboxLimit(t *testing.T) {
	k := testKey(t)
	fit := mailcrypt.MaxPlain
	s, err := mailcrypt.Seal(k, "c-1", domain.MailPhone, 1, make([]byte, fit))
	if err != nil || len(s) != domain.MaxMailSealed {
		t.Fatalf("max plain: %d bytes, %v; want exactly %d", len(s), err, domain.MaxMailSealed)
	}
	if _, err := mailcrypt.Seal(k, "c-1", domain.MailPhone, 1, make([]byte, fit+1)); !errors.Is(err, domain.ErrMailTooLarge) {
		t.Errorf("one byte over: %v, want ErrMailTooLarge", err)
	}
}

func TestKeyEncodingAndRedaction(t *testing.T) {
	k := mailcrypt.NewKey()
	enc := k.Encode()
	back, err := mailcrypt.ParseKey(enc)
	if err != nil || back != k {
		t.Fatalf("parse(encode): %v", err)
	}
	if strings.ContainsAny(enc, "+/=") {
		t.Errorf("key %q is not base64url without padding: breaks the QR fragment", enc)
	}
	for _, f := range []string{"%v", "%s", "%+v", "%#v"} {
		if out := fmt.Sprintf(f, k); strings.Contains(out, enc) || strings.Contains(out, fmt.Sprint([32]byte(k))) {
			t.Errorf("%s prints the key: %s", f, out)
		}
	}
	for _, bad := range []string{"", "AAEC", enc + "AA", strings.Repeat("!", 43)} {
		if _, err := mailcrypt.ParseKey(bad); !errors.Is(err, mailcrypt.ErrFormat) {
			t.Errorf("ParseKey(%q): %v, want ErrFormat", bad, err)
		}
	}
}
