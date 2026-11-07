package main

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
)

// Подписи факта и раздела собирает окно из кода происхождения и даты факта:
// в домене и в файле портрета слов для человека нет (ревью #482/#483).
func TestViewFacts_originWords(t *testing.T) {
	at := time.Date(2026, 11, 5, 21, 0, 0, 0, time.FixedZone("UZT", 5*3600))
	for _, c := range []struct {
		origin domain.FactOrigin
		want   string
	}{
		{domain.OriginManual, "добавлено вручную"},
		{domain.OriginEdited, "исправлено вручную"},
		{domain.OriginCompressed, "сжато"},
		{domain.OriginChat, "из чата 05.11"},
		{domain.OriginChatEdited, "из чата 05.11, исправлено"},
	} {
		f, err := domain.NewFact("f1", domain.SectionPrefs, "Отвечать коротко", c.origin, domain.FactApproved, at)
		if err != nil {
			t.Fatal(err)
		}
		v := viewFacts([]domain.Fact{f})[0]
		if v.Source != c.want || v.Origin != string(c.origin) || v.Title != "Предпочтения" {
			t.Errorf("%q: подпись %q, код %q, раздел %q", c.origin, v.Source, v.Origin, v.Title)
		}
	}
}

// «Вернуть» после «Забыть» отдает в Go вид факта целиком: происхождение
// восстанавливается по коду, а не по подписи.
func TestChat_PortraitRestoreKeepsOrigin(t *testing.T) {
	d, _, _ := newPortraitDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", MemoryDir: t.TempDir()}, ""); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC)
	f, _ := domain.NewFact("x7", domain.SectionPrefs, "Отвечать коротко", domain.OriginChatEdited, domain.FactApproved, at)
	v := viewFacts([]domain.Fact{f})[0]
	if err := d.PortraitRestore(v); err != nil {
		t.Fatal(err)
	}
	got, err := d.Portrait()
	if err != nil || len(got.Facts) != 1 || got.Facts[0].Origin != string(domain.OriginChatEdited) || got.Facts[0].Source != "из чата 05.11, исправлено" {
		t.Fatalf("вернулось: %+v %v", got.Facts, err)
	}
}

// У каждого раздела портрета есть подпись в окне.
func TestSectionTitles_coverEverySection(t *testing.T) {
	for _, sec := range domain.FactSections() {
		if sectionTitles[sec] == "" {
			t.Errorf("раздел %q без подписи в окне", sec)
		}
	}
}

// «Вернуть» с незнакомым кодом (страница старой сборки) возвращает факт как
// ручной, а не отказывает с чужой причиной «пустой или длинный текст».
func TestChat_PortraitRestoreUnknownOrigin(t *testing.T) {
	d, _, _ := newPortraitDesk(t)
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3", MemoryDir: t.TempDir()}, ""); err != nil {
		t.Fatal(err)
	}
	v := factView{ID: "x8", Section: "work", Text: "Пишет на Go", Origin: "", At: time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC)}
	if err := d.PortraitRestore(v); err != nil {
		t.Fatal(err)
	}
	got, err := d.Portrait()
	if err != nil || len(got.Facts) != 1 || got.Facts[0].Origin != string(domain.OriginManual) {
		t.Fatalf("вернулось: %+v %v", got.Facts, err)
	}
}
