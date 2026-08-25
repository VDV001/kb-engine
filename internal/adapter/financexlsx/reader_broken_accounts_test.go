package financexlsx_test

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
)

// Отсутствие листа «Счета» и невозможность его прочитать — разные события, а
// движок отвечал на оба одинаково: пустым списком счетов и никакой ошибкой.
// Пустой список не нарушает инварианта «расчёт может только занижать» — он
// убирает предмет проверки целиком, и на счетах при этом стоит баланс.
func TestRead_brokenAccountsSheetIsAnError(t *testing.T) {
	path := workbookWithBrokenAccountsSheet(t)

	_, err := financexlsx.Read(path, writeClock)
	if err == nil {
		t.Fatal("Read вернул nil при нечитаемом листе «Счета» — " +
			"повреждённая книга неотличима от книги без листа")
	}
	if !strings.Contains(err.Error(), "Счета") {
		t.Errorf("ошибка не называет лист: %v", err)
	}
}

// Контроль в обратную сторону: лист действительно необязателен, и книга без
// него по-прежнему читается. Без этой половины «чинить» можно было бы отказом
// на любой книге, и проверка выше прошла бы.
func TestRead_missingAccountsSheetStaysOptional(t *testing.T) {
	led, err := financexlsx.Read(workbookWithoutAccounts(t), writeClock)
	if err != nil {
		t.Fatalf("книга без листа «Счета» обязана читаться: %v", err)
	}
	if len(led.Accounts) != 0 {
		t.Errorf("счетов %d, ожидалось 0", len(led.Accounts))
	}
}

// workbookWithBrokenAccountsSheet портит XML именно того листа, где лежат
// счета. Имя листа связано с файлом через workbook.xml.rels, а не через
// порядок создания; искать лист по содержимому нельзя — excelize выносит
// строки в sharedStrings.xml, и в самом листе имени «Банк» нет.
func workbookWithBrokenAccountsSheet(t *testing.T) string {
	t.Helper()
	src := workbookWithExtraColumn(t)

	zr, err := zip.OpenReader(src)
	if err != nil {
		t.Fatalf("open xlsx as zip: %v", err)
	}
	type part struct {
		name string
		body []byte
	}
	var parts []part
	target := ""
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		parts = append(parts, part{f.Name, body})
	}
	_ = zr.Close()

	byName := map[string][]byte{}
	for _, p := range parts {
		byName[p.name] = p.body
	}
	rid := attrAfter(t, string(byName["xl/workbook.xml"]), `name="Счета"`, "r:id=\"")
	relTarget := attrAfter(t, string(byName["xl/_rels/workbook.xml.rels"]),
		`Id="`+rid+`"`, "Target=\"")
	target = "xl/" + strings.TrimPrefix(relTarget, "/xl/")
	for i, p := range parts {
		if p.name == target {
			parts[i].body = []byte(`<?xml version="1.0"?><worksheet><sheetData><row`)
		}
	}
	if _, ok := byName[target]; !ok {
		t.Fatalf("лист счетов %q не найден внутри книги — фикстура изменилась, "+
			"и тест проверял бы не то", target)
	}

	dst := filepath.Join(t.TempDir(), "broken.xlsx")
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(out)
	for _, p := range parts {
		w, err := zw.Create(p.name)
		if err != nil {
			t.Fatalf("create %s: %v", p.name, err)
		}
		if _, err := w.Write(p.body); err != nil {
			t.Fatalf("write %s: %v", p.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return dst
}

// attrAfter достаёт значение атрибута, стоящего после якоря в той же строке
// XML. Полноценный разбор здесь не нужен: это фикстура над файлом, который
// только что собрал excelize, а не чужой ввод.
// ponytail: поиск подстрокой без разбора XML — потолок в том, что якорь обязан
// быть уникальным; при переходе на настоящий разбор менять здесь, а не в тесте.
func attrAfter(t *testing.T, xml, anchor, attr string) string {
	t.Helper()
	i := strings.Index(xml, anchor)
	if i < 0 {
		t.Fatalf("якорь %q не найден", anchor)
	}
	rest := xml[i:]
	j := strings.Index(rest, attr)
	if j < 0 {
		t.Fatalf("атрибут %q не найден после %q", attr, anchor)
	}
	rest = rest[j+len(attr):]
	k := strings.Index(rest, `"`)
	if k < 0 {
		t.Fatalf("атрибут %q не закрыт", attr)
	}
	return rest[:k]
}
