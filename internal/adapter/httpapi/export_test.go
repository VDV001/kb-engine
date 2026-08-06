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
	if rows[0][0] != "Дата" || rows[0][7] != "Счёт" {
		t.Errorf("шапка = %v", rows[0])
	}
	// Кириллица доезжает без бубна с кодировками — это половина смысла замены.
	if rows[1][4] != "Монетка" {
		t.Errorf("место = %q, want Монетка", rows[1][4])
	}
	// Сумма должна быть числом, иначе по колонке не посчитать итог.
	if rows[1][6] != "349.97" {
		t.Errorf("сумма = %q, want 349.97", rows[1][6])
	}
}

// В выгрузке доход и расход выглядели одинаково: колонки «Вид» не было, а сумма
// у обоих положительная. В журнале доходов 77 из 625 строк — то есть человек,
// открывший файл и сложивший колонку «Сумма», получал расходы, сложенные с
// поступлениями, и число выглядело правдоподобно.
func TestExportCarriesTheKindOfEachRow(t *testing.T) {
	srv := newTestServer()

	body := strings.NewReader(`{"rows":[
	  {"kind":"expense","date":"2026-07-31","category":"Еда","subcategory":"Продукты","place":"Монетка","description":"","amount":"349.97","account":"Сбербанк"},
	  {"kind":"income","date":"2026-07-30","category":"","subcategory":"","place":"","description":"","amount":"9000","account":""}
	]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/finances/export", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("ответ не открывается как книга: %v", err)
	}
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if rows[0][1] != "Вид" {
		t.Fatalf("шапка = %v, ожидалась колонка «Вид» второй", rows[0])
	}
	if rows[1][1] != "расход" {
		t.Errorf("первая строка: вид = %q, ожидался «расход»", rows[1][1])
	}
	if rows[2][1] != "доход" {
		t.Errorf("вторая строка: вид = %q, ожидался «доход»", rows[2][1])
	}
}
