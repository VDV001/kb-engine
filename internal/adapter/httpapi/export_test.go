package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// Экспорт журнала отдавал CSV с запятыми. Excel в русской локали ждёт точку с
// запятой, поэтому строка попадала в одну ячейку целиком, и владелец получал
// файл, который выглядит как мусор. Отдаём настоящую книгу: движок и так
// умеет их читать excelize, писать той же библиотекой — не новая зависимость.
func TestExportXLSX(t *testing.T) {
	srv := newTestServer()

	body := strings.NewReader(`{"rows":[
	  {"date":"2026-07-31","category":"Еда","subcategory":"Продукты","place":"Монетка","description":"","amount":"349.97","account":"Сбербанк"},
	  {"date":"2026-07-30","category":"Транспорт","subcategory":"Такси","place":"Яндекс Такси","description":"До дома","amount":"482","account":"Сбербанк"}
	]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/finances/export", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Errorf("Content-Type = %q, want an xlsx type", ct)
	}

	// Файл обязан открываться как книга, а не просто иметь расширение.
	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("ответ не открывается как книга: %v", err)
	}
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	// Шапка плюс две строки.
	if len(rows) != 3 {
		t.Fatalf("строк = %d, want 3 (шапка + 2)", len(rows))
	}
	if rows[0][0] != "Дата" || rows[0][6] != "Счёт" {
		t.Errorf("шапка = %v", rows[0])
	}
	// Кириллица доезжает без бубна с кодировками — это половина смысла замены.
	if rows[1][3] != "Монетка" {
		t.Errorf("место = %q, want Монетка", rows[1][3])
	}
	// Сумма должна быть числом, иначе по колонке не посчитать итог.
	if rows[1][5] != "349.97" {
		t.Errorf("сумма = %q, want 349.97", rows[1][5])
	}
}
