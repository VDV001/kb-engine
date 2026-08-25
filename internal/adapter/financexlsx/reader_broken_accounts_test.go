package financexlsx_test

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
	"github.com/xuri/excelize/v2"
)

// Отсутствие листа «Счета» и невозможность его прочитать — разные события, а
// readAccounts отвечал на оба одинаково: пустым списком и никакой ошибкой.
// Сузить глотание до ErrSheetNotExist правильно независимо от того, какие
// ошибки excelize отдаёт сегодня: намерение в коде было узким, а условие — нет.
//
// ⚠️ Замер 2026-08-25, из-за которого этот тест выглядит не так, как задумывался:
// на ПОВРЕЖДЁННОМ XML листа excelize не возвращает ошибку вовсе — GetRows даёт
// ноль строк и nil. Поэтому сценарий «повреждённая книга» ловится не проверкой
// типа ошибки, а тем, что ниже: лист есть в книге, а строк нет.
func TestRead_missingAccountsSheetStaysOptional(t *testing.T) {
	led, err := financexlsx.Read(workbookWithoutAccounts(t), writeClock)
	if err != nil {
		t.Fatalf("книга без листа «Счета» обязана читаться: %v", err)
	}
	if len(led.Accounts) != 0 {
		t.Errorf("счетов %d, ожидалось 0", len(led.Accounts))
	}
}

// Характеристический тест: он не требует поведения, он ЗАПИСЫВАЕТ чужое.
// excelize молча отдаёт пустой лист вместо ошибки разбора, поэтому «лист пуст»
// и «лист не разобрался» на этом уровне неразличимы — и различить их проверкой
// ошибки нельзя. Тест стоит здесь, чтобы смена версии библиотеки не прошла
// незамеченной: если однажды ошибка появится, он покраснеет и укажет, что
// починку #299 можно доводить до конца.
func TestRead_brokenAccountsSheetIsSilentInExcelize(t *testing.T) {
	path := workbookWithBrokenAccountsSheet(t)

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("книга не открылась вовсе — фикстура портит больше, чем задумано: %v", err)
	}
	defer func() { _ = f.Close() }()

	if idx, err := f.GetSheetIndex("Счета"); err != nil || idx < 0 {
		t.Fatalf("лист «Счета» пропал из книги: idx=%d err=%v", idx, err)
	}
	rows, err := f.GetRows("Счета", excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("excelize НАУЧИЛСЯ сообщать о повреждённом листе (%v) — "+
			"значит readAccounts теперь может отличить повреждение от отсутствия, "+
			"и #299 закрывается проверкой типа ошибки", err)
	}
	if len(rows) != 0 {
		t.Fatalf("строк %d, ожидалось 0 — фикстура перестала ломать лист", len(rows))
	}

	// Контроль: отсутствие листа excelize называет типизированной ошибкой,
	// то есть errors.As в readAccounts различает именно тот случай, ради
	// которого глотание и было написано.
	if _, err := f.GetRows("НетТакогоЛиста"); err == nil {
		t.Fatal("отсутствие листа обязано быть ошибкой")
	} else {
		var missing excelize.ErrSheetNotExist
		if !errors.As(err, &missing) {
			t.Errorf("тип ошибки %T, ожидался excelize.ErrSheetNotExist", err)
		}
	}
}

// Замер, который стоит дороже самой починки #299: повреждение листа с
// расходами НЕ обнуляет книгу, а РЕЖЕТ её — три транзакции превращаются в
// одну, ошибки нет ни одной. Частичная потеря правдоподобна, и именно этим
// опасна: отчёт покажет меньше расходов, а fin sync сравнит журнал с книгой,
// которая молча похудела.
//
// Тест не требует поведения — он записывает чужое, чтобы смена версии
// библиотеки не прошла незамеченной. Дешёвого детектора здесь нет:
// объявленный <dimension> в детекторы не годится, потому что xlsxdim.Sync
// намеренно только расширяет объявление и никогда не сужает, то есть
// «объявлено больше, чем прочитано» — законное состояние.
func TestRead_brokenExpensesSheetLosesRowsSilently(t *testing.T) {
	src := workbookWithExtraColumn(t)
	whole, err := financexlsx.Read(src, writeClock)
	if err != nil {
		t.Fatalf("целая книга обязана читаться: %v", err)
	}
	if len(whole.Transactions) < 2 {
		t.Fatalf("фикстура даёт %d транзакций — на ней потерю не увидеть",
			len(whole.Transactions))
	}

	damaged, err := financexlsx.Read(brokenSheet(t, src, "Расходы"), writeClock)
	if err != nil {
		t.Fatalf("excelize НАУЧИЛСЯ сообщать о повреждённом листе (%v) — "+
			"потеря строк перестала быть тихой, детектор можно строить на ошибке", err)
	}
	if len(damaged.Transactions) >= len(whole.Transactions) {
		t.Fatalf("транзакций %d при %d в целой книге — фикстура перестала ломать лист",
			len(damaged.Transactions), len(whole.Transactions))
	}
	t.Logf("повреждение листа «Расходы»: %d транзакций вместо %d, ошибки нет",
		len(damaged.Transactions), len(whole.Transactions))
}

// workbookWithBrokenAccountsSheet портит XML именно того листа, где лежат
// счета. Имя листа связано с файлом через workbook.xml.rels, а не через
// порядок создания; искать лист по содержимому нельзя — excelize выносит
// строки в sharedStrings.xml, и в самом листе имени «Банк» нет.
func workbookWithBrokenAccountsSheet(t *testing.T) string {
	t.Helper()
	return brokenSheet(t, workbookWithExtraColumn(t), "Счета")
}

// brokenSheet портит XML именно того листа, который назван. Имя связано с
// файлом через workbook.xml.rels, а не через порядок создания; искать лист по
// содержимому нельзя — excelize выносит строки в sharedStrings.xml.
func brokenSheet(t *testing.T, src, sheet string) string {
	t.Helper()

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
	rid := attrAfter(t, string(byName["xl/workbook.xml"]), `name="`+sheet+`"`, "r:id=\"")
	relTarget := attrAfter(t, string(byName["xl/_rels/workbook.xml.rels"]),
		`Id="`+rid+`"`, "Target=\"")
	target = "xl/" + strings.TrimPrefix(relTarget, "/xl/")
	for i, p := range parts {
		if p.name == target {
			parts[i].body = []byte(`<?xml version="1.0"?><worksheet><sheetData><row`)
		}
	}
	if _, ok := byName[target]; !ok {
		t.Fatalf("лист %q (%s) не найден внутри книги — фикстура изменилась, "+
			"и тест проверял бы не то", sheet, target)
	}

	dst := filepath.Join(t.TempDir(), "broken-"+sheet+".xlsx")
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
