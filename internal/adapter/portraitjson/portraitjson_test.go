package portraitjson_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/portraitjson"
	"github.com/daniil/kb-engine/internal/domain"
)

func doc(source string) []byte {
	return []byte(`{"facts":[{"id":"f1","section":"work","text":"Пишет на Go","source":"` + source +
		`","status":"approved","at":"2026-11-05T12:00:00+05:00"}]}`)
}

// В файле и в бакете — код происхождения, а не слова для человека: подписи
// собирает окно (ревью #482/#483).
func TestEncode_writesOriginCode(t *testing.T) {
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	f, err := domain.NewFact("f1", domain.SectionWork, "Пишет на Go", domain.OriginChatEdited, domain.FactApproved, at)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := domain.NewPortrait([]domain.Fact{f})
	raw, err := portraitjson.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"source": "chat-edited"`) {
		t.Fatalf("в файле нет кода происхождения:\n%s", raw)
	}
	back, err := portraitjson.Decode(raw)
	if err != nil || back.Facts()[0].Origin() != domain.OriginChatEdited {
		t.Fatalf("обратно: %+v %v", back.Facts(), err)
	}
}

// Портреты, записанные до кодов (0.36.0), хранят слова: они читаются в коды,
// дата «из чата DD.MM» живет в at факта. Чужие слова — испорченный файл.
func TestDecode_legacySources(t *testing.T) {
	for _, c := range []struct {
		source string
		want   domain.FactOrigin
	}{
		{"manual", domain.OriginManual},
		{"compressed", domain.OriginCompressed},
		{"добавлено вручную", domain.OriginManual},
		{"исправлено вручную", domain.OriginEdited},
		{"сжато", domain.OriginCompressed},
		{"из чата 05.11", domain.OriginChat},
		{"из чата 05.11, исправлено", domain.OriginChatEdited},
	} {
		p, err := portraitjson.Decode(doc(c.source))
		if err != nil || p.Facts()[0].Origin() != c.want {
			t.Errorf("%q: %+v %v", c.source, p.Facts(), err)
		}
	}
	for _, bad := range []string{"", "с телефона", "из чатика"} {
		if _, err := portraitjson.Decode(doc(bad)); !errors.Is(err, portraitjson.ErrCorrupt) {
			t.Errorf("%q принят: %v", bad, err)
		}
	}
}
