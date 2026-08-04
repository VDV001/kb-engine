package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Записанную трату можно было исправить только командой, зная её id. Владелец
// пропустил счёт в форме и остался с записью, до которой из терминала не
// дотянуться: экран показывал суммы, но не сами записи.
//
// Экран правки закрывает это: последние записи списком, выбор стрелками, та же
// форма с заполненными полями. Пишет он не вторым способом — тем же usecase,
// что и команда.

// stubEditor стоит вместо леджера: отдаёт последние записи и запоминает правку.
type stubEditor struct {
	recent []finance.Record
	gotID  string
	gotP   finance.EditParams
	err    error
}

func (s *stubEditor) Recent(int) ([]finance.Record, error) { return s.recent, s.err }

func (s *stubEditor) EditEntry(id string, p finance.EditParams) error {
	s.gotID, s.gotP = id, p
	return s.err
}

func recordFor(t *testing.T, id, account string) finance.Record {
	t.Helper()
	amount, err := domain.ParseMoney("322.00")
	if err != nil {
		t.Fatalf("ParseMoney: %v", err)
	}
	at := func() time.Time { return time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC) }
	rec, err := finance.Add(finance.AddParams{
		Kind: domain.KindExpense, Date: at(), Amount: amount,
		Category: "Транспорт", Subcategory: "Такси", Description: "такси до центра",
		Account: account,
	}, func() string { return id }, at)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return rec
}

func editorModel(t *testing.T) (tui.Model, *stubEditor) {
	t.Helper()
	ed := &stubEditor{recent: []finance.Record{recordFor(t, "01AAA", "")}}
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(&stubWriter{}).WithEntryEditor(ed)
	return m, ed
}

func TestFinances_eOpensRecentEntries(t *testing.T) {
	m, _ := editorModel(t)

	view := press(press(m, tab()), runes("e")).View()

	if !strings.Contains(view, "такси до центра") {
		t.Errorf("список последних записей не показан:\n%s", view)
	}
	// Пропущенный счёт должен быть виден: ради него экран и заводится.
	if !strings.Contains(view, "без счёта") {
		t.Errorf("запись без счёта ничем не помечена:\n%s", view)
	}
}

// Без редактора клавиши нет вовсе: клавиша, открывающая экран, с которого
// нельзя ничего сохранить, хуже отсутствующей.
func TestFinances_eIsAbsentWithoutEditor(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin).WithFinanceWriter(&stubWriter{})

	view := press(press(m, tab()), runes("e")).View()

	if strings.Contains(view, "правка записи") {
		t.Errorf("экран правки открылся без редактора:\n%s", view)
	}
}

func TestFinances_editFormWritesTheChange(t *testing.T) {
	m, ed := editorModel(t)

	m = press(press(m, tab()), runes("e")) // список
	m = press(m, enter())                  // выбрать запись под курсором
	// Форма открыта на выбранной записи: доходим до поля счёта и вписываем банк.
	view := m.View()
	if !strings.Contains(view, "Транспорт") {
		t.Fatalf("форма не заполнена значениями записи:\n%s", view)
	}
	for range 4 {
		m = press(m, tab())
	}
	m = press(m, runes("Сбербанк"))
	press(m, enter())

	if ed.gotID != "01AAA" {
		t.Errorf("правка ушла не к той записи: %q", ed.gotID)
	}
	if ed.gotP.Account != "Сбербанк" {
		t.Errorf("счёт не передан в правку: %+v", ed.gotP)
	}
}

// Esc возвращает к списку, а не роняет экран целиком: правка — это два шага,
// и отказ на втором не должен отменять первый.
func TestFinances_escFromEditFormReturnsToList(t *testing.T) {
	m, _ := editorModel(t)

	m = press(press(m, tab()), runes("e"))
	m = press(m, enter())
	view := press(m, esc()).View()

	if !strings.Contains(view, "такси до центра") {
		t.Errorf("Esc из формы не вернул к списку:\n%s", view)
	}
}
