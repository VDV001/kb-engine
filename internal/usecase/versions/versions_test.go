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
		if i < 15000 {
			fmt.Fprintf(&b, "строка a%d\n", i) // половина общая: один документ, а не два
		} else {
			fmt.Fprintf(&b, "строка b%d\n", i)
		}
	}
	start := time.Now()
	got := versions.Group([]versions.File{
		file("/a/Таблица_2026_11_05.xlsx", "x", a.String(), day(11, 5)),
		file("/a/Таблица_2026_11_10.xlsx", "y", b.String(), day(11, 10)),
	})
	if d := time.Since(start); d > time.Second {
		t.Errorf("сравнение двух текстов по 30 тыс. строк заняло %v", d)
	}
	if len(got) != 1 || len(got[0].Versions) != 2 {
		t.Fatalf("ждали одно семейство из двух версий: %+v", len(got))
	}
	if c := got[0].Versions[1].Change; len(c.Added) != 15000 || len(c.Removed) != 15000 {
		t.Errorf("добавлено %d, убрано %d, ждали по 15000", len(c.Added), len(c.Removed))
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

func numbered(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s%d\n", prefix, i)
	}
	return b.String()
}

func TestGroup_SmallEditOfABigDocumentIsADiff(t *testing.T) {
	old := numbered("a", 100)
	edited := strings.Replace(old, "a7\n", "новая семерка\n", 1)
	got := versions.Group([]versions.File{
		file("/a/Отчет_2026_11_05.docx", "x", old, day(11, 5)),
		file("/a/Отчет_2026_11_10.docx", "y", edited, day(11, 10)),
	})
	if c := got[0].Versions[1].Change; c.Kind != versions.KindDiff || len(c.Added) != 1 || len(c.Removed) != 1 {
		t.Errorf("мелкая правка: %+v", c)
	}
}

func TestGroup_SameNameDifferentTextsAreDifferentFamilies(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/Уроки/T3.docx", "x", numbered("a", 40), day(11, 5)),
		file("/Работа/T3.docx", "y", numbered("b", 40), day(11, 10)),
	})
	if len(got) != 2 {
		t.Fatalf("два разных документа с одним именем склеились: %+v", got)
	}
	for _, f := range got {
		if len(f.Versions) != 1 || f.Versions[0].Change.Kind != versions.KindFirst {
			t.Errorf("каждый документ — своя первая версия: %+v", f)
		}
	}
}

func TestGroup_EditsKeepOneFamilyWhileAnotherStaysApart(t *testing.T) {
	a := numbered("a", 40)
	got := versions.Group([]versions.File{
		file("/p/T3_2026_11_01.docx", "h1", a, day(11, 1)),
		file("/q/T3.docx", "h2", numbered("b", 40), day(11, 3)),
		file("/p/T3_2026_11_05.docx", "h3", strings.Replace(a, "a7\n", "правка\n", 1), day(11, 5)),
	})
	if len(got) != 2 {
		t.Fatalf("ждали 2 семейства, есть %d: %+v", len(got), got)
	}
	var mine versions.Family
	for _, f := range got {
		if len(f.Versions) == 2 {
			mine = f
		}
	}
	if len(mine.Versions) != 2 || mine.Versions[1].Change.Kind != versions.KindDiff {
		t.Errorf("правки первого документа потерялись: %+v", got)
	}
}

func TestGroup_NoJudgmentKeepsFamilyTogether(t *testing.T) {
	for name, fs := range map[string][]versions.File{
		"короткие тексты": {
			file("/a/Записка_2026_11_05.docx", "x", "Привет\nМир", day(11, 5)),
			file("/b/Записка_2026_11_10.docx", "y", "Пока\nСвет", day(11, 10)),
		},
		"нет текста": {
			file("/a/Скан_2026_11_05.pdf", "x", "", day(11, 5)),
			file("/b/Скан_2026_11_10.pdf", "y", numbered("b", 40), day(11, 10)),
		},
		"другой формат": {
			file("/a/Отчет_2026_11_05.docx", "x", numbered("a", 40), day(11, 5)),
			file("/b/Отчет_2026_11_10.pdf", "y", numbered("b", 40), day(11, 10)),
		},
	} {
		if got := versions.Group(fs); len(got) != 1 || len(got[0].Versions) != 2 {
			t.Errorf("%s: семейство распалось без оснований: %+v", name, got)
		}
	}
}

// numberedRange возвращает prefix{from}..prefix{to-1}, по строке на номер.
func numberedRange(prefix string, from, to int) string {
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "%s%d\n", prefix, i)
	}
	return b.String()
}

func famWith(t *testing.T, fams []versions.Family, path string) versions.Family {
	t.Helper()
	for _, f := range fams {
		for _, v := range f.Versions {
			if v.Path == path {
				return f
			}
			if slices.Contains(v.Dups, path) {
				return f
			}
		}
	}
	t.Fatalf("%s не найден ни в одном семействе: %+v", path, fams)
	return versions.Family{}
}

func TestGroup_OtherFormatJoinsTheDocumentItResembles(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/T3.docx", "h1", numbered("a", 40), day(11, 1)),
		file("/b/T3.docx", "h2", numbered("b", 40), day(11, 3)),
		file("/a/T3.pdf", "h3", numbered("a", 40), day(11, 5)), // PDF документа A, пришёл позже B
	})
	if len(got) != 2 {
		t.Fatalf("ждали 2 семейства: %+v", got)
	}
	if f := famWith(t, got, "/a/T3.pdf"); famWith(t, got, "/a/T3.docx").Versions[0].Path != f.Versions[0].Path {
		t.Errorf("PDF документа A ушёл в чужое семейство: %+v", got)
	}
}

func TestGroup_FileWithoutTextJoinsTheDocumentInItsFolder(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/T3.docx", "h1", numbered("a", 40), day(11, 1)),
		file("/b/T3.docx", "h2", numbered("b", 40), day(11, 3)),
		file("/a/T3.pdf", "h3", "", day(11, 5)), // скан рядом с A
	})
	if f := famWith(t, got, "/a/T3.pdf"); !slices.ContainsFunc(f.Versions, func(v versions.Version) bool { return v.Path == "/a/T3.docx" }) {
		t.Errorf("скан ушёл не в семейство своей папки: %+v", got)
	}
}

func TestGroup_FileWithoutTextAndFolderClueJoinsTheNearestInTime(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/T3_2026_11_01.docx", "h1", numbered("a", 40), day(11, 1)),
		file("/b/T3_2026_11_03.docx", "h2", numbered("b", 40), day(11, 3)),
		file("/a/T3_2026_11_08.docx", "h3", numberedRange("a", 0, 39)+"правка\n", day(11, 8)),
		file("/c/T3_2026_11_10.pdf", "h4", "", day(11, 10)),
	})
	if f := famWith(t, got, "/c/T3_2026_11_10.pdf"); !slices.ContainsFunc(f.Versions, func(v versions.Version) bool { return strings.HasSuffix(v.Path, "11_08.docx") }) {
		t.Errorf("файл без подсказок ушёл не к ближайшему по времени документу: %+v", got)
	}
}

func TestGroup_TrimmedDocumentStaysInTheFamily(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/Спека_2026_11_01.docx", "h1", numbered("a", 200), day(11, 1)),
		file("/a/Спека_2026_11_05.docx", "h2", numbered("a", 50), day(11, 5)), // та же спека, урезанная
	})
	if len(got) != 1 || len(got[0].Versions) != 2 {
		t.Errorf("урезанный документ ушёл из семейства: %+v", got)
	}
}

func TestGroup_RevertToAnEarlierVersionJoinsItsFamily(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/Док_2026_11_01.docx", "h1", numberedRange("a", 0, 40), day(11, 1)),
		file("/a/Док_2026_11_02.docx", "h2", numberedRange("a", 0, 20)+numberedRange("c", 0, 20), day(11, 2)),
		file("/a/Док_2026_11_03.docx", "h3", numberedRange("c", 0, 20)+numberedRange("d", 0, 20), day(11, 3)),
		file("/a/Док_2026_11_04.docx", "h4", numberedRange("a", 0, 40), day(11, 4)), // откат к первой версии
	})
	if len(got) != 1 || len(got[0].Versions) != 4 {
		t.Errorf("откат к старой версии открыл новое семейство: %d семейств %+v", len(got), got)
	}
}

func TestGroup_JoinsTheCloserOfTwoDocuments(t *testing.T) {
	b := numbered("b", 40)
	got := versions.Group([]versions.File{
		file("/a/T3.docx", "h1", numbered("a", 40), day(11, 1)),
		file("/b/T3.docx", "h2", b, day(11, 3)),
		file("/b/T3_2026_11_05.docx", "h3", strings.Replace(b, "b7\n", "правка\n", 1), day(11, 5)),
	})
	if len(got) != 2 {
		t.Fatalf("ждали 2 семейства: %+v", got)
	}
	if f := famWith(t, got, "/b/T3_2026_11_05.docx"); len(f.Versions) != 2 || !slices.ContainsFunc(f.Versions, func(v versions.Version) bool { return v.Path == "/b/T3.docx" }) {
		t.Errorf("правка B ушла не к B: %+v", got)
	}
}

func TestGroup_ManyBigSameNameFilesAreFast(t *testing.T) {
	var fs []versions.File
	for i := range 8 {
		p := "a"
		if i%2 == 1 {
			p = "b"
		}
		fs = append(fs, file(fmt.Sprintf("/d%d/T3.docx", i), fmt.Sprint("h", i), numberedRange(p, 0, 20000)+numberedRange("u", i*10, i*10+5), day(11, i+1)))
	}
	start := time.Now()
	got := versions.Group(fs)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("8 файлов по 20 тыс. строк разведены за %v", d)
	}
	if len(got) != 2 {
		t.Errorf("ждали 2 документа, есть %d", len(got))
	}
}

func TestGroup_VersionAfterATooBigOneComparesWithTheLastReadableOne(t *testing.T) {
	big := file("/a/Проект_2026_11_06.pptx", "", "", day(11, 6))
	big.TooBig = true
	got := versions.Group([]versions.File{
		file("/a/Проект_2026_11_05.pptx", "h1", "Цена 100", day(11, 5)),
		big,
		file("/a/Проект_2026_11_10.pptx", "h3", "Цена 100\nГарантия", day(11, 10)),
	})
	vs := got[0].Versions
	if c := vs[2].Change; c.Kind != versions.KindDiff || !slices.Equal(c.Added, []string{"Гарантия"}) {
		t.Errorf("версия после слишком большой должна сравниваться с последней читаемой: %+v", c)
	}
}

func TestKey_DateIsNotCutOutOfALongerNumber(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"Акт 0220.05.2026.docx", "Акт 02.docx"},
		{"Заказ 12026_1_2.docx", "Заказ 1.docx"},
		{"Акт 20.05.20269.docx", "Акт.docx"},
	} {
		if versions.Key("/a/"+c.a) == versions.Key("/a/"+c.b) {
			t.Errorf("%q склеился с %q: часть номера принята за дату", c.a, c.b)
		}
	}
	if versions.Key("/a/Акт 20.05.2026.docx") != versions.Key("/a/Акт.docx") {
		t.Error("настоящая дата в имени должна по-прежнему вырезаться")
	}
}

func TestGroup_FamilyNameKeepsCaseAndDropsDatesAndNoise(t *testing.T) {
	got := versions.Group([]versions.File{
		file("/a/КП_Ромашка_2026_11_05.pptx", "h1", "а", day(11, 5)),
		file("/a/КП_Ромашка_2026_11_10 (копия).pptx", "h2", "б", day(11, 10)),
	})
	if got[0].Name != "КП Ромашка" {
		t.Errorf("имя семейства %q, ждали «КП Ромашка»", got[0].Name)
	}
}

func TestGroup_ComparisonSkippingAnUnreadableVersionSaysSo(t *testing.T) {
	big := file("/a/Проект_2026_11_06.pptx", "", "", day(11, 6))
	big.TooBig = true
	got := versions.Group([]versions.File{
		file("/a/Проект_2026_11_05.pptx", "h1", "Цена 100", day(11, 5)),
		big,
		file("/a/Проект_2026_11_10.pptx", "h3", "Цена 100\nГарантия", day(11, 10)),
	})
	if c := got[0].Versions[2].Change; c.Skipped != 1 {
		t.Errorf("пропущена одна версия без текста, а Skipped = %d", c.Skipped)
	}
	got = versions.Group([]versions.File{
		file("/a/Проект_2026_11_05.pptx", "h1", "Цена 100", day(11, 5)),
		file("/a/Проект_2026_11_10.pptx", "h3", "Цена 100\nГарантия", day(11, 10)),
	})
	if c := got[0].Versions[1].Change; c.Skipped != 0 {
		t.Errorf("ничего не пропущено, а Skipped = %d", c.Skipped)
	}
}
