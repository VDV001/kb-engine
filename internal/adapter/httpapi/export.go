package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/xuri/excelize/v2"
)

// exportRow is one journal line as the view has it on screen. The view sends
// the rows it already filtered and sorted rather than a set of query
// parameters: repeating the filtering here would be a second implementation of
// it, and two implementations of one rule drift apart.
type exportRow struct {
	Date        string `json:"date"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	Place       string `json:"place"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
	Account     string `json:"account"`
}

type exportRequest struct {
	Rows []exportRow `json:"rows"`
}

var exportHeader = []string{
	"Дата", "Категория", "Подкатегория", "Место", "Описание", "Сумма", "Счёт",
}

// Ширины на глаз по самому длинному ожидаемому значению: файл открывают, чтобы
// читать, а колонка в восемь символов с «Яндекс Такси» внутри читается как
// решётка.
var exportWidths = []float64{12, 16, 18, 22, 30, 12, 14}

// amountColumn is the one column written as a number rather than text: without
// it the total cannot be summed, and the total is what the file is opened for.
const amountColumn = 5

// handleFinanceExport writes the rows it is given as an xlsx workbook.
//
// It replaces a CSV export: comma-separated text lands in a single cell in a
// Russian-locale Excel, so the file looked broken. excelize is already a
// dependency — the engine reads the owner's workbook with it — so writing one
// costs nothing new.
func handleFinanceExport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req exportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="finances.xlsx"`)
		if err := writeWorkbook(w, req.Rows); err != nil {
			// Заголовки могли уже уйти — статус тут писать поздно, остаётся
			// оборвать ответ и не делать вид, что файл целый.
			return
		}
	}
}

// writeWorkbook renders rows into dst as an xlsx workbook.
func writeWorkbook(dst io.Writer, rows []exportRow) error {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)

	if err := writeRow(f, sheet, 1, exportHeader, -1); err != nil {
		return err
	}
	for n, row := range rows {
		values := []string{
			row.Date, row.Category, row.Subcategory,
			row.Place, row.Description, row.Amount, row.Account,
		}
		if err := writeRow(f, sheet, n+2, values, amountColumn); err != nil {
			return err
		}
	}
	for i, width := range exportWidths {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return err
		}
		if err := f.SetColWidth(sheet, col, col, width); err != nil {
			return err
		}
	}
	return f.Write(dst)
}

// writeRow writes one row, treating the value at numeric as a number when it
// parses as one. numeric of -1 means every cell is text.
func writeRow(f *excelize.File, sheet string, rowNum int, values []string, numeric int) error {
	for i, v := range values {
		cell, err := excelize.CoordinatesToCellName(i+1, rowNum)
		if err != nil {
			return err
		}
		if i == numeric {
			if err := f.SetCellValue(sheet, cell, asNumber(v)); err != nil {
				return err
			}
			continue
		}
		if err := f.SetCellStr(sheet, cell, v); err != nil {
			return err
		}
	}
	return nil
}

// asNumber returns v as a float when it is one, and as the original string
// otherwise. A malformed amount is shown verbatim rather than silently turned
// into zero: a wrong number in a ledger is worse than a visible oddity.
func asNumber(v string) any {
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return n
	}
	return v
}
