package tui_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
)

// saverSpy records what the screen asked to write and can refuse, the way a
// domain invariant refuses.
type saverSpy struct {
	got  []tui.Edit
	fail error
}

func (s *saverSpy) Save(e tui.Edit) error {
	if s.fail != nil {
		return s.fail
	}
	s.got = append(s.got, e)
	return nil
}

// reloadSpy stands in for re-reading the catalog after a write: the screen must
// show what the file now says, not what it hoped it would say.
type reloadSpy struct {
	entries []domain.Entry
	calls   int
}

func (r *reloadSpy) Entries() ([]domain.Entry, error) {
	r.calls++
	return r.entries, nil
}

func editable(t *testing.T, saver tui.EntrySaver, loader tui.EntryLoader) tui.Model {
	t.Helper()
	return tui.NewEditableModel(fixture(t), saver, loader)
}

func TestModel_changingLifecycleFromTheCard(t *testing.T) {
	saver := &saverSpy{}
	loader := &reloadSpy{entries: fixture(t)}
	m := send(editable(t, saver, loader), "enter", "l")

	if !strings.Contains(m.View(), "dead-end") {
		t.Fatalf("список состояний не показан:\n%s", m.View())
	}

	// active → outdated → canonical → superseded → dead-end: четыре шага вниз.
	m = send(m, "down", "down", "down", "down", "enter")

	if len(saver.got) != 1 {
		t.Fatalf("записей = %d, want 1", len(saver.got))
	}
	if got := saver.got[0]; got.ID != 1 || got.Lifecycle != "dead-end" {
		t.Errorf("записано %+v, want id=1 lifecycle=dead-end", got)
	}
	if loader.calls == 0 {
		t.Error("каталог не перечитан после записи — экран показывает надежду, а не файл")
	}
	if view := m.View(); !strings.Contains(view, "dead-end") {
		t.Errorf("после записи экран не назвал новое значение:\n%s", view)
	}
}

func TestModel_changingVerdictFromTheCard(t *testing.T) {
	saver := &saverSpy{}
	m := send(editable(t, saver, &reloadSpy{entries: fixture(t)}), "enter", "v", "down", "enter")
	_ = m

	if len(saver.got) != 1 {
		t.Fatalf("записей = %d, want 1", len(saver.got))
	}
	if got := saver.got[0]; got.Verdict == "" || got.Lifecycle != "" {
		t.Errorf("записано %+v — ожидался только вердикт", got)
	}
}

// Отказ домена обязан доехать до экрана: молча проглоченная ошибка означает,
// что человек считает правку применённой, а в файле её нет.
func TestModel_showsWhyTheWriteWasRefused(t *testing.T) {
	saver := &saverSpy{fail: errors.New("status is a publish stage, not a verdict")}
	m := send(editable(t, saver, &reloadSpy{entries: fixture(t)}), "enter", "v", "down", "enter")

	view := m.View()
	if !strings.Contains(view, "publish stage") {
		t.Errorf("причина отказа не показана:\n%s", view)
	}
}

func TestModel_escapeCancelsTheEdit(t *testing.T) {
	saver := &saverSpy{}
	m := send(editable(t, saver, &reloadSpy{entries: fixture(t)}), "enter", "l", "down", "esc")

	if len(saver.got) != 0 {
		t.Errorf("Esc записал %+v — отмена обязана ничего не менять", saver.got)
	}
	if !m.OnCard() {
		t.Error("Esc из выбора должен возвращать на карточку, а не в список")
	}
}

// Модель без права записи не должна предлагать правку: кнопка, которая ничего
// не делает, хуже отсутствующей.
func TestModel_readOnlyModelIgnoresEditKeys(t *testing.T) {
	m := send(tui.NewModel(fixture(t)), "enter", "l")

	if strings.Contains(m.View(), "dead-end") {
		t.Error("экран только для чтения предложил правку")
	}
}

// Успех обязан быть виден так же, как отказ: иначе единственный признак того,
// что правка прошла, — отсутствие ошибки, а это не признак.
func TestModel_saysWhatWasWritten(t *testing.T) {
	m := send(editable(t, &saverSpy{}, &reloadSpy{entries: fixture(t)}), "enter", "v", "down", "enter")

	view := m.View()
	if !strings.Contains(view, "записано") {
		t.Errorf("успешная правка молчит:\n%s", view)
	}
	if !strings.Contains(view, "вердикт") {
		t.Errorf("не названо, какое поле изменилось:\n%s", view)
	}
}

// Выбор открывается на текущем значении: промахнувшийся Enter не должен
// поменять состояние записи на первое из списка.
//
// Запись берётся НЕ в состоянии active: оно первое в списке, и на нём тест
// прошёл бы даже с курсором, прибитым к нулю. Проверено подсадкой дефекта.
func TestModel_pickerStartsOnTheCurrentValue(t *testing.T) {
	saver := &saverSpy{}
	entries := []domain.Entry{withLifecycle(t, mustEntry(t, 7, "канонический разбор", "ai-agents", nil), "canonical")}
	send(tui.NewEditableModel(entries, saver, &reloadSpy{entries: entries}), "enter", "l", "enter")

	if len(saver.got) != 1 {
		t.Fatalf("записей = %d, want 1", len(saver.got))
	}
	if got := saver.got[0].Lifecycle; got != "canonical" {
		t.Errorf("записано %q, want canonical — выбор открылся не на текущем значении", got)
	}
}
