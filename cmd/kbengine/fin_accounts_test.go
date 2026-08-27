package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// Гейт push.sh собирает имена счетов из ledger и сам объявляет, чего этот
// источник не видит: «счёт, по которому нет ни одной транзакции, в ledger не
// попадает вовсе — а именно так устроен долговой счёт». Там же сказано, чем это
// закрывается: «командой „перечисли счета“ у движка; её сегодня нет».
//
// Цена отсутствия замерена 26.08 на публичном репозитории: имя личного счёта
// стояло в фикстурах и комментариях 17 раз, гейт молчал, нашлось руками.
func TestRun_finAccounts(t *testing.T) {
	book := accountsWorkbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "accounts", "--from", book}, &out, &errb); code != 0 {
		t.Fatalf("fin accounts exit = %d, stderr = %s", code, errb.String())
	}

	// Счёт БЕЗ единой транзакции обязан быть в выводе — ради него команда и
	// заводится. Со списком из журнала он невидим по конструкции.
	for _, want := range []string{"Сбербанк", "Долг → Кузнецов"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("вывод не содержит счёт %q:\n%s", want, out.String())
		}
	}
}

// ⚠️ Остатки НЕ печатаются, и это не забывчивость. Потребитель команды — гейт
// публичного репозитория: он зовёт её при каждом push и пишет вывод в лог.
// Команда, печатающая рядом с именем сумму, сделала бы утечкой сам инструмент
// против утечек. Кому нужен остаток — у того есть `fin balance` и `fin report`.
func TestRun_finAccounts_printsNoBalances(t *testing.T) {
	book := accountsWorkbook(t)

	var out, errb bytes.Buffer
	if code := run([]string{"fin", "accounts", "--from", book}, &out, &errb); code != 0 {
		t.Fatalf("fin accounts exit = %d, stderr = %s", code, errb.String())
	}

	// Числа фикстуры выбраны непохожими ни на что другое в выводе: совпадение
	// подстроки здесь означало бы настоящую утечку, а не случайность.
	for _, leak := range []string{"1000.50", "1000,50", "7654.32", "7654,32"} {
		if strings.Contains(out.String(), leak) {
			t.Errorf("в выводе есть остаток %q — команда обязана печатать только имена:\n%s",
				leak, out.String())
		}
	}
}

// Книги может не быть — тогда отказ обязан НАЗВАТЬ причину и вернуть ненулевой
// код. Молчаливый пустой список читался бы как «счетов нет», а это другой ответ.
func TestRun_finAccounts_missingBookIsAnError(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"fin", "accounts", "--from", "/такого/пути/нет.xlsx"}, &out, &errb)
	if code == 0 {
		t.Fatalf("отсутствующая книга дала код 0, вывод: %s", out.String())
	}
	if strings.TrimSpace(errb.String()) == "" {
		t.Error("отказ не назвал причину — «не смог прочитать» и «счетов нет» обязаны различаться")
	}
	// ⚠️ Без этой проверки тест зеленел на НЕСУЩЕСТВУЮЩЕЙ команде: «unknown fin
	// subcommand» тоже даёт код 2 и тоже пишет в stderr. Поймано на RED-прогоне
	// — два соседних случая упали, а этот прошёл, хотя проверять было нечего.
	if strings.Contains(errb.String(), "unknown fin subcommand") {
		t.Fatalf("сработал диспетчер, а не команда: %s", errb.String())
	}
}

// accountsWorkbook — книга с двумя счетами, из которых ВТОРОЙ не участвует ни в
// одной транзакции. Это и есть форма долгового счёта из живых данных владельца.
func accountsWorkbook(t *testing.T) string {
	t.Helper()
	path := workbook(t)

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetCellValue("Счета", "A4", "Долг → Кузнецов"))
	must(f.SetCellValue("Счета", "B4", 7654.32))
	must(f.SetCellValue("Счета", "C4", time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)))
	must(f.Save())
	return path
}
