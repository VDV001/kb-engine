package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
	"github.com/xuri/excelize/v2"
)

// workbookWithDollarAccount is the CLI fixture plus one account held in USD —
// the shape of the dollar accounts in the live book.
func workbookWithDollarAccount(t *testing.T) string {
	t.Helper()
	path := workbook(t)
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for cell, v := range map[string]any{
		"A4": "Банк USD", "B4": 25.5, "C4": time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC), "D4": "USD",
	} {
		if err := f.SetCellValue("Счета", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_finAdd_currency(t *testing.T) {
	cases := []struct {
		name     string
		extra    func(book string) []string
		wantCode int
		wantLine string // substring of the written line; "" = must carry no currency
		wantErr  string
	}{
		{
			name:     "валюта берётся у счёта из книги",
			extra:    func(book string) []string { return []string{"--from", book, "--account", "Банк USD"} },
			wantLine: `"currency":"USD"`,
		},
		{
			name:     "названная валюта без книги",
			extra:    func(string) []string { return []string{"--currency", "usd"} },
			wantLine: `"currency":"USD"`,
		},
		{
			name: "рубли с долларового счёта — отказ",
			extra: func(book string) []string {
				return []string{"--from", book, "--account", "Банк USD", "--currency", "RUB"}
			},
			wantCode: 1,
			wantErr:  "currency",
		},
		{
			name:     "негодный код — отказ",
			extra:    func(string) []string { return []string{"--currency", "ДОЛ"} },
			wantCode: 1,
			wantErr:  "currency",
		},
		{
			name:  "рублёвая трата поля не несёт",
			extra: func(book string) []string { return []string{"--from", book, "--account", "Сбербанк"} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			book := workbookWithDollarAccount(t)
			ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
			finImport(t, book, ledger)

			args := append([]string{"fin", "add", "--ledger", ledger,
				"--amount", "12,30", "--cat", "Сервисы", "--date", "2026-04-06"}, tc.extra(book)...)
			var out, errb bytes.Buffer
			code := run(args, &out, &errb)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, tc.wantCode, errb.String())
			}
			if tc.wantCode != 0 {
				if !strings.Contains(strings.ToLower(errb.String()), tc.wantErr) {
					t.Errorf("stderr = %q, want it to mention %q", errb.String(), tc.wantErr)
				}
				return
			}
			line := lastLedgerLine(t, ledger)
			if tc.wantLine == "" {
				if strings.Contains(line, `"currency"`) {
					t.Errorf("рублёвая строка несёт валюту:\n%s", line)
				}
				return
			}
			if !strings.Contains(line, tc.wantLine) {
				t.Errorf("строка без %s:\n%s", tc.wantLine, line)
			}
		})
	}
}

// fin edit меняет валюту записи — так переводится в доллары трата, записанная
// когда-то рублями по курсу.
func TestRun_finEdit_currency(t *testing.T) {
	book := workbookWithDollarAccount(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, book, ledger)
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--amount", "12,30", "--cat", "Сервисы", "--date", "2026-04-06"}, &out, &errb); code != 0 {
		t.Fatalf("fin add: %s", errb.String())
	}
	id := strings.Fields(out.String())[2]

	out.Reset()
	errb.Reset()
	if code := run([]string{"fin", "edit", "--ledger", ledger, "--id", id, "--currency", "USD"}, &out, &errb); code != 0 {
		t.Fatalf("fin edit exit = %d, stderr = %s", code, errb.String())
	}
	if line := lastLedgerLine(t, ledger); !strings.Contains(line, `"currency":"USD"`) {
		t.Errorf("после правки строка без валюты:\n%s", line)
	}
}

// Форма терминала пишет тем же путём, что fin add, и обязана так же брать валюту
// у счёта: иначе трата с долларового счёта, введённая в TUI, ушла бы рублями и
// встала бы на сверке отказом.
func TestLedgerFinances_addTakesTheAccountCurrency(t *testing.T) {
	book := workbookWithDollarAccount(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, book, ledger)
	money, err := domain.ParseMoney("12,30")
	if err != nil {
		t.Fatal(err)
	}
	f := ledgerFinances{ledgerPath: ledger, workbookPath: book}
	if _, err := f.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
		Amount: money, Category: "Сервисы", Account: "Банк USD",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if line := lastLedgerLine(t, ledger); !strings.Contains(line, `"currency":"USD"`) {
		t.Errorf("трата из формы без валюты счёта:\n%s", line)
	}
}

// fin report называет доллары рядом с итогом, а не роняет их молча.
func TestRun_finSummary_namesOtherCurrencies(t *testing.T) {
	book := workbookWithDollarAccount(t)
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, book, ledger)
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--amount", "12,30", "--cat", "Сервисы", "--date", "2026-04-06", "--currency", "USD"}, &out, &errb); code != 0 {
		t.Fatalf("fin add: %s", errb.String())
	}
	out.Reset()
	if code := run([]string{"fin", "report", "--ledger", ledger}, &out, &errb); code != 0 {
		t.Fatalf("fin report: %s", errb.String())
	}
	if !strings.Contains(out.String(), "USD") || !strings.Contains(out.String(), "12.30") {
		t.Errorf("fin report не назвал долларовую трату:\n%s", out.String())
	}
}

// Сумма печатается вместе с валютой везде, где её читает человек. Без кода
// дифф сверки показал бы «27196.00 → 312.40», и правильный перевод в доллары
// выглядел бы как сумма, упавшая в десятки раз.
func TestAmountText_namesTheCurrency(t *testing.T) {
	usd, _ := domain.NewCurrency("USD")
	mk := func(c domain.Currency) domain.Transaction {
		tx, err := domain.NewTransaction(domain.TransactionParams{
			ID: "01X", Kind: domain.KindExpense, Date: time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
			Amount: domain.NewMoney(31240), Category: "Жильё", Currency: c,
			Now: func() time.Time { return time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC) },
		})
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	if got := describeTx(mk(usd)); !strings.Contains(got, "312.40 USD") {
		t.Errorf("строка сверки без валюты: %q", got)
	}
	if got := describeTx(mk(domain.Currency{})); strings.Contains(got, "RUB") {
		t.Errorf("рублёвая строка сверки изменила вид: %q", got)
	}
}

func TestRun_finAdd_printsTheCurrency(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, workbook(t), ledger)
	var out, errb bytes.Buffer
	if code := run([]string{"fin", "add", "--ledger", ledger, "--amount", "12,30", "--cat", "Сервисы", "--date", "2026-04-06", "--currency", "USD"}, &out, &errb); code != 0 {
		t.Fatalf("fin add: %s", errb.String())
	}
	if !strings.Contains(out.String(), "12.30 USD") {
		t.Errorf("подтверждение без валюты: %s", out.String())
	}
}

// Быстрый ввод не зависит от того, разбирается ли книга целиком: испорченная
// ячейка «Валюта» на листе трат не мешает записать рублёвую трату, потому что
// для валюты счёта нужен только лист «Счета».
func TestLedgerFinances_addSurvivesABrokenCurrencyCell(t *testing.T) {
	book := workbookWithDollarAccount(t)
	f, err := excelize.OpenFile(book)
	if err != nil {
		t.Fatal(err)
	}
	for cell, v := range map[string]string{"J2": "Валюта", "J3": "ДОЛ"} {
		if err := f.SetCellValue("Расходы", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	ledger := filepath.Join(t.TempDir(), "transactions.jsonl")
	finImport(t, workbook(t), ledger)
	money, _ := domain.ParseMoney("120")
	lf := ledgerFinances{ledgerPath: ledger, workbookPath: book}
	if _, err := lf.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
		Amount: money, Category: "Еда", Account: "Банк USD",
	}); err != nil {
		t.Fatalf("Add отказал из-за чужой испорченной ячейки: %v", err)
	}
	if line := lastLedgerLine(t, ledger); !strings.Contains(line, `"currency":"USD"`) {
		t.Errorf("валюта счёта не подставлена:\n%s", line)
	}
}
