package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/financexlsx"
)

// Движок научился ЧИТАТЬ валюту счёта (#332, пункт 1), но записать её было
// нечем: колонки заполнялись руками. Правило проекта прямое — в книгу пишет
// только движок, потому что строка, дописанная мимо него, не имеет ни формата,
// ни проверенных инвариантов.
func TestFinBalance_writesCurrencyAndRate(t *testing.T) {
	book := workbook(t)
	var out, errb bytes.Buffer

	code := run([]string{
		"fin", "balance", "--from", book,
		"--bank", "Кубышка → Доллары", "--amount", "500",
		"--currency", "USD", "--rate", "84.28", "--create",
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}

	led, err := financexlsx.Read(book, time.Now)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, a := range led.Accounts {
		if a.Bank() != "Кубышка → Доллары" {
			continue
		}
		if a.Currency().Code() != "USD" {
			t.Errorf("Currency() = %q, ожидалось USD", a.Currency().Code())
		}
		per, ok := a.Rate().PerUnit()
		if !ok || per.Kopecks() != 8428 {
			t.Errorf("курс = %d, %v; ожидалось 8428 и true", per.Kopecks(), ok)
		}
		// Сумма остаётся в своей валюте: 500,00 доллара.
		if a.Balance().Kopecks() != 50000 {
			t.Errorf("Balance() = %d, ожидалось 50000", a.Balance().Kopecks())
		}
		return
	}
	t.Fatal("счёт не найден в книге после записи")
}

// Валюта без курса — законный случай: наличные, полученные подарком, курса не
// имеют вовсе. Записывается валюта, курс остаётся неизвестным, и это НЕ ошибка.
func TestFinBalance_currencyWithoutRateIsAllowed(t *testing.T) {
	book := workbook(t)
	var out, errb bytes.Buffer

	code := run([]string{
		"fin", "balance", "--from", book,
		"--bank", "Кубышка → Лиры", "--amount", "3000",
		"--currency", "TRY", "--create",
	}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}

	led, _ := financexlsx.Read(book, time.Now)
	for _, a := range led.Accounts {
		if a.Bank() != "Кубышка → Лиры" {
			continue
		}
		if a.Currency().Code() != "TRY" {
			t.Errorf("Currency() = %q, ожидалось TRY", a.Currency().Code())
		}
		if a.Rate().Known() {
			t.Error("курс объявлен известным, хотя его не называли")
		}
		return
	}
	t.Fatal("счёт не найден")
}

// Курс без валюты — противоречие: рубль оценивают в рублях только по единице.
// Отказ обязан быть ДО записи, иначе в книге останется строка, о которой никто
// не знает.
func TestFinBalance_rateWithoutCurrencyIsRefused(t *testing.T) {
	book := workbook(t)
	var out, errb bytes.Buffer

	code := run([]string{
		"fin", "balance", "--from", book,
		"--bank", "Кубышка → Ошибка", "--amount", "100",
		"--rate", "84.28", "--create",
	}, &out, &errb)
	if code == 0 {
		t.Fatal("курс без валюты принят, ожидался отказ")
	}
	if !strings.Contains(errb.String(), "валют") {
		t.Errorf("причина отказа не названа: %s", errb.String())
	}

	// Книга не должна нести следов отказавшей команды.
	led, _ := financexlsx.Read(book, time.Now)
	for _, a := range led.Accounts {
		if a.Bank() == "Кубышка → Ошибка" {
			t.Fatal("отказавшая команда всё же дописала строку в книгу")
		}
	}
}

// Негодный код валюты отвергается тем же правилом, что и при чтении: домен
// решает, что такое валюта, а не CLI.
func TestFinBalance_brokenCurrencyIsRefused(t *testing.T) {
	book := workbook(t)
	var out, errb bytes.Buffer

	code := run([]string{
		"fin", "balance", "--from", book,
		"--bank", "Кубышка → Ещё", "--amount", "100",
		"--currency", "доллары", "--create",
	}, &out, &errb)
	if code == 0 {
		t.Fatal("негодная валюта принята")
	}
}
