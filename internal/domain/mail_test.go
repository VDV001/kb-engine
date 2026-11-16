package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Менее точное имя не заменяет точное: Chrome шлет модель не в каждом запросе,
// и «Samsung SM-S918B» не должен прыгать в «Android» и обратно (ревью #652).
func TestBetterPhoneName(t *testing.T) {
	for _, c := range []struct{ prev, next, want string }{
		{"", "iPhone", "iPhone"},
		{"Samsung SM-S918B", domain.PhoneAndroid, "Samsung SM-S918B"},
		{"Samsung SM-S918B", "", "Samsung SM-S918B"},
		{domain.PhoneAndroid, "Samsung SM-S918B", "Samsung SM-S918B"},
		{"Samsung SM-S918B", "Pixel 8", "Pixel 8"},
		{domain.PhoneIPhone, domain.PhoneIPadOrMac, domain.PhoneIPadOrMac},
		{"Pixel 8", domain.PhoneIPhone, "Pixel 8"},
	} {
		if got := domain.BetterPhoneName(c.prev, c.next); got != c.want {
			t.Errorf("BetterPhoneName(%q, %q) = %q, want %q", c.prev, c.next, got, c.want)
		}
	}
}

// Сообщение ящика (#622): сервер хранит только шифротекст, а правила — чей он,
// в каком чате и какого размера — живут здесь.
func TestNewMailMessage(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ok := []byte("шифротекст")
	for _, c := range []struct {
		name   string
		chat   string
		from   domain.MailSide
		sealed []byte
		want   error
	}{
		{"phone", "c-1a2b", domain.MailPhone, ok, nil},
		{"desk", "chat-9", domain.MailDesk, ok, nil},
		{"empty chat", "", domain.MailPhone, ok, domain.ErrMailChat},
		{"chat with slash", "a/b", domain.MailPhone, ok, domain.ErrMailChat},
		{"chat upper", "Chat", domain.MailPhone, ok, domain.ErrMailChat},
		{"chat too long", strings.Repeat("a", 65), domain.MailPhone, ok, domain.ErrMailChat},
		{"unknown side", "c", domain.MailSide("server"), ok, domain.ErrMailSide},
		{"empty", "c", domain.MailDesk, nil, domain.ErrMailEmpty},
		{"too large", "c", domain.MailDesk, make([]byte, domain.MaxMailSealed+1), domain.ErrMailTooLarge},
		{"at the limit", "c", domain.MailDesk, make([]byte, domain.MaxMailSealed), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, err := domain.NewMailMessage(c.chat, c.from, c.sealed, at)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if err == nil && (m.Chat() != c.chat || m.From() != c.from || !m.At().Equal(at) || len(m.Sealed()) != len(c.sealed) || m.Seq() != 0) {
				t.Errorf("message %+v", m)
			}
		})
	}
}

// Номер дает журнал; сообщение с номером — та же запись, а не новая.
func TestMailMessage_withSeq(t *testing.T) {
	m, err := domain.NewMailMessage("c", domain.MailPhone, []byte("x"), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	n := m.WithSeq(7)
	if n.Seq() != 7 || m.Seq() != 0 || n.Chat() != "c" {
		t.Errorf("WithSeq: %d, original %d", n.Seq(), m.Seq())
	}
}

func TestParseMailSide(t *testing.T) {
	for in, want := range map[string]error{"phone": nil, "desk": nil, "": domain.ErrMailSide, "Phone": domain.ErrMailSide} {
		if _, err := domain.ParseMailSide(in); !errors.Is(err, want) {
			t.Errorf("ParseMailSide(%q) = %v, want %v", in, err, want)
		}
	}
}

// «devices», «push», «notify» — маршруты ящика, а не чаты (ревью #630, #622).
func TestNewMailMessage_reservedChat(t *testing.T) {
	for _, chat := range []string{"devices", "push", "notify"} {
		if _, err := domain.NewMailMessage(chat, domain.MailDesk, []byte("x"), time.Unix(1, 0)); !errors.Is(err, domain.ErrMailChat) {
			t.Errorf("%s: err = %v", chat, err)
		}
	}
}

// Записанное раньше читается и после того, как правила ужесточат: одна старая
// строка не роняет весь ящик (ревью #630).
func TestRestoreMailMessage(t *testing.T) {
	m, err := domain.RestoreMailMessage(5, "Old/Chat", domain.MailPhone, make([]byte, domain.MaxMailSealed+10), time.Unix(1, 0))
	if err != nil || m.Seq() != 5 || m.Chat() != "Old/Chat" {
		t.Fatalf("restore = %+v, %v", m, err)
	}
	if _, err := domain.RestoreMailMessage(1, "c", domain.MailSide("server"), []byte("x"), time.Unix(1, 0)); !errors.Is(err, domain.ErrMailSide) {
		t.Errorf("unknown side restored: %v", err)
	}
}
