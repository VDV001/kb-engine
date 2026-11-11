package versions_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/versions"
)

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 12, 0, 0, 0, time.UTC) }

func file(path, hash, text string, mod time.Time) versions.File {
	return versions.File{Path: path, Size: int64(len(text)), Mod: mod, Hash: hash, Text: text}
}

func TestGroup_OneFamilyAcrossFormatsAndFolders(t *testing.T) {
	fams := versions.Group([]versions.File{
		file("/Downloads/КП_Ромашка_2026_11_10.pptx", "h2", "Цена 100\nСрок 5 дней\nГарантия", day(11, 10)),
		file("/Проект/КП_Ромашка_2026_11_05.pptx", "h1", "Цена 100\nСрок 5 дней", day(11, 5)),
		file("/Проект/Другое_ТЗ.docx", "h9", "Совсем другое", day(11, 6)),
	})
	if len(fams) != 2 {
		t.Fatalf("семейств %d, ждали 2: %+v", len(fams), fams)
	}
	var kp versions.Family
	for _, f := range fams {
		if len(f.Versions) == 2 {
			kp = f
		}
	}
	if len(kp.Versions) != 2 {
		t.Fatalf("КП не собралось в одно семейство: %+v", fams)
	}
	if !strings.HasSuffix(kp.Versions[0].Path, "05.pptx") || !strings.HasSuffix(kp.Versions[1].Path, "10.pptx") {
		t.Errorf("лента не по времени: %v, %v", kp.Versions[0].Path, kp.Versions[1].Path)
	}
	if c := kp.Versions[0].Change; c.Kind != versions.KindFirst {
		t.Errorf("первая версия: %+v", c)
	}
	if c := kp.Versions[1].Change; c.Kind != versions.KindDiff || !slices.Equal(c.Added, []string{"Гарантия"}) || len(c.Removed) != 0 {
		t.Errorf("что нового: %+v", c)
	}
}

func TestGroup_NameNoise(t *testing.T) {
	for _, names := range [][]string{
		{"Смета.xlsx", "Смета v2.xlsx"},
		{"Смета.xlsx", "Смета (1).xlsx"},
		{"Смета.xlsx", "Смета - копия.xlsx"},
		{"Смета.xlsx", "Смета_финал.xlsx"},
		{"Смета_2026-11-05.xlsx", "Смета_05.11.2026.xlsx"},
		{"Отчет.docx", "Отчет.pdf"},
	} {
		fs := []versions.File{
			file("/a/"+names[0], "x", "один", day(11, 1)),
			file("/b/"+names[1], "y", "два", day(11, 2)),
		}
		if got := versions.Group(fs); len(got) != 1 || len(got[0].Versions) != 2 {
			t.Errorf("%v не собрались в одно семейство: %+v", names, got)
		}
	}
}

func TestGroup_DifferentNamesStayApart(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/Смета.xlsx", "x", "один", day(11, 1)),
		file("/a/Договор.xlsx", "y", "два", day(11, 2)),
	})
	if len(got) != 2 {
		t.Fatalf("разные документы склеились: %+v", got)
	}
}

func TestGroup_ByteIdenticalCopyIsDupNotVersion(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/Проект/КП.pdf", "same", "Цена 100", day(11, 5)),
		file("/Downloads/КП (1).pdf", "same", "Цена 100", day(11, 7)),
	})
	if len(got) != 1 || len(got[0].Versions) != 1 {
		t.Fatalf("дубль стал версией: %+v", got)
	}
	v := got[0].Versions[0]
	if v.Path != "/Проект/КП.pdf" || len(v.Dups) != 1 || v.Dups[0] != "/Downloads/КП (1).pdf" {
		t.Errorf("дубль не привязан к самой ранней: %+v", v)
	}
}

func TestGroup_LayoutOnlyChangeIsNotUnchanged(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/КП_2026_11_05.pptx", "h1", "Цена 100", day(11, 5)),
		file("/a/КП_2026_11_10.pptx", "h2", "Цена 100", day(11, 10)),
	})
	if c := got[0].Versions[1].Change; c.Kind != versions.KindLayoutOnly {
		t.Errorf("правка вёрстки не названа: %+v", c)
	}
}

func TestGroup_NoteComparesWithSameFormat(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/КП_2026_11_05.pptx", "h1", "Цена 100", day(11, 5)),
		file("/a/КП_2026_11_06.pdf", "h2", "Цена 100 PDF-шум", day(11, 6)),
		file("/a/КП_2026_11_10.pptx", "h3", "Цена 100\nГарантия", day(11, 10)),
	})
	vs := got[0].Versions
	if c := vs[1].Change; c.Kind != versions.KindNewFormat || c.Format != "pdf" {
		t.Errorf("новый формат не назван: %+v", c)
	}
	if c := vs[2].Change; c.Kind != versions.KindDiff || !slices.Equal(c.Added, []string{"Гарантия"}) || len(c.Removed) != 0 {
		t.Errorf("дифф должен идти от предыдущего pptx: %+v", c)
	}
}

func TestGroup_MissingText(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/Скан_2026_11_05.pdf", "h1", "", day(11, 5)),
		file("/a/Скан_2026_11_10.pdf", "h2", "", day(11, 10)),
	})
	if c := got[0].Versions[1].Change; c.Kind != versions.KindNoText {
		t.Errorf("нет текста: %+v", c)
	}
}

func TestGroup_NameMadeOfNoiseStaysApart(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/2026-11-10.docx", "x", "один", day(11, 1)),
		file("/b/v2.docx", "y", "два", day(11, 2)),
		file("/c/Копия.docx", "z", "три", day(11, 3)),
	})
	if len(got) != 3 {
		t.Fatalf("документы без имени склеились в %d семейств: %+v", len(got), got)
	}
}

func TestGroup_NumbersAreNotDates(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/Смета_1000_12_5.xlsx", "x", "один", day(11, 1)),
		file("/a/Смета_2000_3_4.xlsx", "y", "два", day(11, 2)),
		file("/a/Смета_2026_13_40.xlsx", "z", "три", day(11, 3)), // месяца 13 нет
	})
	if len(got) != 3 {
		t.Fatalf("номера приняты за даты: %d семейств, ждали 3: %+v", len(got), got)
	}
}

func TestGroup_BigTextsDiffFast(t *testing.T) {
	var a, b strings.Builder
	for i := range 30000 {
		fmt.Fprintf(&a, "строка a%d\n", i)
		fmt.Fprintf(&b, "строка b%d\n", i)
	}
	start := time.Now()
	got := versions.Group([]versions.File{
		file("/a/Таблица_2026_11_05.xlsx", "x", a.String(), day(11, 5)),
		file("/a/Таблица_2026_11_10.xlsx", "y", b.String(), day(11, 10)),
	})
	if d := time.Since(start); d > time.Second {
		t.Errorf("сравнение двух текстов по 30 тыс. строк заняло %v", d)
	}
	if c := got[0].Versions[1].Change; len(c.Added) != 30000 || len(c.Removed) != 30000 {
		t.Errorf("добавлено %d, убрано %d, ждали по 30000", len(c.Added), len(c.Removed))
	}
}

func TestWhen_PrefersNameDateOverMtime(t *testing.T) {
	f := file("/a/КП_2026_11_05.docx", "x", "t", day(12, 31))
	if got := versions.When(f); got.Day() != 5 || got.Month() != time.November {
		t.Errorf("дата из имени проиграла времени изменения: %v", got)
	}
}

func TestGroup_TooBigFileIsNamedNotSkipped(t *testing.T) {
	big := file("/a/Проект_2026_11_05.pptx", "", "", day(11, 5))
	big.TooBig = true
	got := versions.Group([]versions.File{big, file("/a/Проект_2026_11_10.pptx", "h", "Текст", day(11, 10))})
	if len(got) != 1 || len(got[0].Versions) != 2 {
		t.Fatalf("большой файл выпал из семейства: %+v", got)
	}
	if c := got[0].Versions[0].Change; c.Kind != versions.KindTooBig {
		t.Errorf("большой файл не назван: %+v", c)
	}
	if c := got[0].Versions[1].Change; c.Kind != versions.KindNoText {
		t.Errorf("сравнивать с непрочитанным нельзя: %+v", c)
	}
}
