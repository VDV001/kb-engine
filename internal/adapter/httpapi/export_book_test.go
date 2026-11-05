package httpapi_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/xuri/excelize/v2"
)

func getBook(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, *excelize.File) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		return rec, nil
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("%s: Content-Type %q, want an xlsx type", path, ct)
	}
	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("%s does not open as a workbook: %v", path, err)
	}
	return rec, f
}

func sheetRows(t *testing.T, f *excelize.File, sheet string) [][]string {
	t.Helper()
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("sheet %q: %v (sheets %v)", sheet, err, f.GetSheetList())
	}
	return rows
}

// Вся книга человека с сервера, без страницы: операции и счета с расчетным
// остатком тем же расчетом, что у /api/finances. Человек скачивает свое и
// смотрит в Excel без движка.
func TestFinanceWorkbook(t *testing.T) {
	rec, f := getBook(t, newTestServer(), "/api/finances/export.xlsx")
	if f == nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".xlsx") {
		t.Errorf("Content-Disposition %q", cd)
	}
	ops := sheetRows(t, f, "Операции")
	if len(ops) != 2 || ops[0][0] != "Дата" || ops[1][4] != "Лавка" || ops[1][6] != "500" || ops[1][7] != "Сбербанк" {
		t.Fatalf("Операции %v", ops)
	}
	if typ, _ := f.GetCellType("Операции", "G2"); typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
		t.Errorf("the amount is text, the column cannot be summed")
	}
	accs := sheetRows(t, f, "Счета")
	if len(accs) != 2 || accs[0][0] != "Счёт" {
		t.Fatalf("Счета %v", accs)
	}
	head := strings.Join(accs[0], "|")
	for _, want := range []string{"Подтверждено", "Обновлено", "Сейчас", "Валюта"} {
		if !strings.Contains(head, want) {
			t.Errorf("the Счета header lacks %s: %v", want, accs[0])
		}
	}
	// 1000 подтверждено 28.07, трата 500 — 01.07, до подтверждения: банк ее уже
	// посчитал, расчетный остаток тот же.
	if accs[1][0] != "Сбербанк" || accs[1][1] != "1000" || accs[1][3] != "1000" {
		t.Errorf("Сбербанк %v", accs[1])
	}
}

func TestFinanceWorkbookRefusals(t *testing.T) {
	noFin := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, nil,
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil)
	if rec, _ := getBook(t, noFin, "/api/finances/export.xlsx"); rec.Code != http.StatusNotFound {
		t.Errorf("without finances: %d, want 404", rec.Code)
	}
	broken := httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{err: errors.New("users/u1/fin/journal.jsonl: broken")},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil)
	rec, _ := getBook(t, broken, "/api/finances/export.xlsx")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "users/") {
		t.Errorf("a broken journal: %d %q, want 500 without the path", rec.Code, rec.Body.String())
	}
}

// Каталог базы в Excel: каждая запись строкой, теги через запятую, ссылка как
// есть — то, что человек видит в витрине, без витрины.
func TestEntriesWorkbook(t *testing.T) {
	rec, f := getBook(t, newTestServer(), "/api/entries/export.xlsx")
	if f == nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	rows := sheetRows(t, f, "Записи")
	entries, _ := fakeQuery{}.Entries()
	if len(rows) != len(entries)+1 {
		t.Fatalf("%d rows for %d entries: %v", len(rows), len(entries), rows)
	}
	head := strings.Join(rows[0], "|")
	for _, want := range []string{"ID", "Заголовок", "Категория", "Вердикт", "Теги", "Добавлено", "Ссылка"} {
		if !strings.Contains(head, want) {
			t.Errorf("the header lacks %s: %v", want, rows[0])
		}
	}
	// По колонкам, а не подстрокой: «go» нашлось бы и в «golang».
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	for name, want := range map[string]string{
		"ID": "1", "Заголовок": "Hello", "Категория": "golang", "Вердикт": "keep",
		"Теги": "go", "Добавлено": "2026-07-11", "Ссылка": "https://h/x",
	} {
		if got := rows[1][col[name]]; got != want {
			t.Errorf("%s = %q, want %q (%v)", name, got, want, rows[1])
		}
	}
}
