package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

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

// «devices» — маршрут ящика, а не чат (ревью #630).
func TestNewMailMessage_reservedChat(t *testing.T) {
	if _, err := domain.NewMailMessage("devices", domain.MailDesk, []byte("x"), time.Unix(1, 0)); !errors.Is(err, domain.ErrMailChat) {
		t.Errorf("err = %v", err)
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
