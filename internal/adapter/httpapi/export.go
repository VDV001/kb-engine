package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/finance"

	"github.com/daniil/kb-engine/internal/adapter/xlsxdim"
	"github.com/xuri/excelize/v2"
)

// exportRow is one journal line as the view has it on screen. The view sends
// the rows it already filtered and sorted rather than a set of query
// parameters: repeating the filtering here would be a second implementation of
// it, and two implementations of one rule drift apart.
type exportRow struct {
	Date string `json:"date"`
	// Kind — «expense» или «income». Без него доход и расход в файле выглядят
	// одинаково: сумма у обоих положительная, и колонка «Сумма», сложенная
	// целиком, смешивает траты с поступлениями.
	Kind        string `json:"kind"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	Place       string `json:"place"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
	Account     string `json:"account"`
	// Currency — код валюты суммы, пусто для рубля. Без неё доллары в числовой
	// колонке «Сумма» складываются с рублями при первом же СУММ.
	Currency string `json:"currency"`
}

type exportRequest struct {
	Rows []exportRow `json:"rows"`
}

var exportHeader = []string{
	"Дата", "Вид", "Категория", "Подкатегория", "Место", "Описание", "Сумма", "Счёт", "Валюта",
}

// Ширины на глаз по самому длинному ожидаемому значению: файл открывают, чтобы
// читать, а колонка в восемь символов с «Яндекс Такси» внутри читается как
// решётка.
var exportWidths = []float64{12, 8, 16, 18, 22, 30, 12, 14, 8}

// amountColumn is the one column written as a number rather than text: without
// it the total cannot be summed, and the total is what the file is opened for.
const amountColumn = 6

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
	values := make([][]string, 0, len(rows))
	for _, row := range rows {
		values = append(values, []string{
			row.Date, kindLabel(row.Kind), row.Category, row.Subcategory,
			row.Place, row.Description, row.Amount, row.Account, currencyLabel(row.Currency),
		})
	}
	return writeBook(dst, []sheet{{"Sheet1", exportHeader, exportWidths, values, []int{amountColumn}}})
}

// writeRow writes one row, treating the values at numeric as numbers when they
// parse as ones. Без numeric — весь ряд текстом.
func writeRow(f *excelize.File, sheet string, rowNum int, values []string, numeric ...int) error {
	for i, v := range values {
		cell, err := excelize.CoordinatesToCellName(i+1, rowNum)
		if err != nil {
			return err
		}
		if slices.Contains(numeric, i) {
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

// kindLabel переводит вид записи на язык человека, открывающего файл.
//
// Пустой вид оставляется пустым, а не подставляется расходом: выгрузка со
// старой витрины вида не несёт, и назвать такую строку расходом значило бы
// соврать про доход.
func kindLabel(kind string) string {
	switch kind {
	case "expense":
		return "расход"
	case "income":
		return "доход"
	default:
		return ""
	}
}

// currencyLabel пишет валюту в файл явно, RUB для рубля: пустая клетка — плохой
// фильтр в книге, которую открывают, чтобы считать по ней.
func currencyLabel(code string) string {
	if code == "" {
		return "RUB"
	}
	return code
}

var accountsHeader = []string{"Счёт", "Подтверждено", "Обновлено", "Сейчас", "Валюта", "Курс"}

var accountsWidths = []float64{24, 14, 12, 14, 8, 10}

var entriesHeader = []string{
	"ID", "Заголовок", "Категория", "Тип", "Статус", "Вердикт", "Теги", "Добавлено", "Автор", "Ссылка", "Описание",
}

var entriesWidths = []float64{6, 50, 16, 10, 10, 10, 24, 12, 18, 40, 60}

// handleFinanceWorkbook is GET /api/finances/export.xlsx: вся книга человека с
// сервера, без страницы — лист «Операции» (колонки той же выгрузки, что
// POST /api/finances/export) и лист «Счета» с расчетным остатком тем же
// finance.CurrentBalances, что у /api/finances. Человек скачивает свое и
// смотрит без движка.
func handleFinanceWorkbook(fin Financier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fin == nil {
			http.Error(w, "no finances on this server", http.StatusNotFound)
			return
		}
		f, err := fin.Finances()
		if err != nil {
			// Как у /api/finances: в причине путь к личному файлу или ключ.
			log.Printf("finances export: %v", err)
			http.Error(w, "finances unavailable", http.StatusInternalServerError)
			return
		}
		ops := make([][]string, 0, len(f.Transactions))
		for _, t := range f.Transactions {
			ops = append(ops, []string{
				t.Date().Format(time.DateOnly), kindLabel(t.Kind()), t.Category(), t.Subcategory(),
				t.Place(), t.Description(), t.Amount().String(), t.Account(), currencyLabel(foreignCode(t.Currency())),
			})
		}
		var accs [][]string
		for _, b := range finance.CurrentBalances(f.Accounts, f.Transactions, f.Confirmations) {
			rate := ""
			if !b.Unvalued && !b.Rate.IsZero() {
				rate = b.Rate.String()
			}
			accs = append(accs, []string{
				b.Bank, b.Confirmed.String(), b.ConfirmedOn, b.Current.String(), currencyLabel(currencyCode(b.Currency)), rate,
			})
		}
		sendBook(w, "finances.xlsx", []sheet{
			{"Операции", exportHeader, exportWidths, ops, []int{amountColumn}},
			{"Счета", accountsHeader, accountsWidths, accs, []int{1, 3, 5}},
		})
	}
}

// handleEntriesWorkbook is GET /api/entries/export.xlsx: каталог базы, запись
// строкой. Поля берутся тем же toDTO, что у /api/entries: две сборки одной
// записи однажды разошлись бы.
func handleEntriesWorkbook(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := q.Entries()
		if err != nil {
			log.Printf("entries export: %v", err)
			http.Error(w, "entries unavailable", http.StatusInternalServerError)
			return
		}
		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			d := toDTO(e)
			verdict := ""
			if d.Verdict != nil {
				verdict = *d.Verdict
			}
			rows = append(rows, []string{
				strconv.Itoa(d.ID), d.Title, d.Category, d.Kind, d.Lifecycle, verdict,
				strings.Join(d.Tags, ", "), d.DateAdded, d.Author, d.URL, d.Description,
			})
		}
		sendBook(w, "entries.xlsx", []sheet{{"Записи", entriesHeader, entriesWidths, rows, []int{0}}})
	}
}

// sheet is one sheet of a workbook: шапка, ширины, строки и номера колонок,
// которые пишутся числами.
type sheet struct {
	name    string
	header  []string
	widths  []float64
	rows    [][]string
	numeric []int
}

// sendBook builds the workbook in memory and only then answers: сбой сборки —
// честный 500, а не оборванный на середине файл с кодом 200.
func sendBook(w http.ResponseWriter, filename string, sheets []sheet) {
	var buf bytes.Buffer
	if err := writeBook(&buf, sheets); err != nil {
		log.Printf("export %s: %v", filename, err)
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write(buf.Bytes())
}

// writeBook renders sheets into dst, the first one in place of the default.
func writeBook(dst io.Writer, sheets []sheet) error {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for i, sh := range sheets {
		if i == 0 {
			if err := f.SetSheetName(f.GetSheetName(0), sh.name); err != nil {
				return err
			}
		} else if _, err := f.NewSheet(sh.name); err != nil {
			return err
		}
		if err := writeSheet(f, sh); err != nil {
			return err
		}
	}
	// Книгу открывает кто-то другой — в этом весь смысл выгрузки. Читатель,
	// который верит объявленному диапазону, без этого не увидит ни одной
	// ячейки: excelize пишет туда "A1" при любом содержимом.
	if err := xlsxdim.Sync(f); err != nil {
		return err
	}
	return f.Write(dst)
}

func writeSheet(f *excelize.File, sh sheet) error {
	if err := writeRow(f, sh.name, 1, sh.header); err != nil {
		return err
	}
	for n, row := range sh.rows {
		if err := writeRow(f, sh.name, n+2, row, sh.numeric...); err != nil {
			return err
		}
	}
	for i, width := range sh.widths {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return err
		}
		if err := f.SetColWidth(sh.name, col, col, width); err != nil {
			return err
		}
	}
	return nil
}
