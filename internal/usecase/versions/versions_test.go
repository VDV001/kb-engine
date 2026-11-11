package versions_test

import (
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
	if kp.Versions[0].Note != "первая версия" {
		t.Errorf("первая версия: %q", kp.Versions[0].Note)
	}
	if n := kp.Versions[1].Note; !strings.Contains(n, "Гарантия") || !strings.Contains(n, "добавлено 1") {
		t.Errorf("что нового: %q", n)
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
	n := got[0].Versions[1].Note
	if !strings.Contains(n, "текст не менялся") || strings.Contains(n, "без изменений") {
		t.Errorf("правка вёрстки не названа: %q", n)
	}
}

func TestGroup_NoteComparesWithSameFormat(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/КП_2026_11_05.pptx", "h1", "Цена 100", day(11, 5)),
		file("/a/КП_2026_11_06.pdf", "h2", "Цена 100 PDF-шум", day(11, 6)),
		file("/a/КП_2026_11_10.pptx", "h3", "Цена 100\nГарантия", day(11, 10)),
	})
	vs := got[0].Versions
	if !strings.Contains(vs[1].Note, "pdf") {
		t.Errorf("новый формат не назван: %q", vs[1].Note)
	}
	if strings.Contains(vs[2].Note, "PDF-шум") || !strings.Contains(vs[2].Note, "Гарантия") {
		t.Errorf("дифф должен идти от предыдущего pptx: %q", vs[2].Note)
	}
}

func TestGroup_MissingText(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/Скан_2026_11_05.pdf", "h1", "", day(11, 5)),
		file("/a/Скан_2026_11_10.pdf", "h2", "", day(11, 10)),
	})
	if n := got[0].Versions[1].Note; !strings.Contains(n, "текст недоступен") {
		t.Errorf("нет текста: %q", n)
	}
}
