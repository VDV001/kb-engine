package tui_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

type stubHealth struct {
	h     audit.Health
	err   error
	calls int
}

func (s *stubHealth) Health() (audit.Health, error) {
	s.calls++
	return s.h, s.err
}

func sampleHealth() audit.Health {
	return audit.Health{
		Outdated:     []audit.Finding{{EntryID: 12, Title: "Материал снят", Reasons: []string{"keyword:снят"}}},
		Supersession: []audit.Finding{{EntryID: 34, Title: "Замена", Reasons: []string{"supersedes:999"}}},
		Duplicates:   []audit.DuplicateGroup{{Kind: "exact-url", Key: "https://example.com/a", EntryIDs: []int{1, 2}}},
		Links:        audit.LinkHealth{Alive: 1004, Moved: 179, Gone: 5, Undecidable: 122, Unchecked: 0, WithURL: 1310},
	}
}

func healthModel(h *stubHealth) tui.Model {
	return tui.NewModel(nil).WithHealth(h)
}

// Здоровье — то, ради чего в дашборд заходят чаще всего: оно отвечает, что
// чинить. В терминале его не было вовсе, поэтому за ответом приходилось
// открывать браузер.
func TestHealth_showsWhatToFix(t *testing.T) {
	m := healthModel(&stubHealth{h: sampleHealth()})

	m = press(m, tab())

	view := m.View()
	for _, want := range []string{"здоровье", "Материал снят", "Замена", "1004", "179"} {
		if !strings.Contains(view, want) {
			t.Errorf("на экране нет %q\n--- view ---\n%s", want, view)
		}
	}
}

// Непроверенные ссылки называются всегда, включая ноль. Правило 11 стандарта:
// инструмент обязан сказать, чего он НЕ проверял, иначе пробел неотличим от
// проверенной чистоты.
func TestHealth_namesTheUncheckedEvenWhenZero(t *testing.T) {
	m := healthModel(&stubHealth{h: sampleHealth()})

	m = press(m, tab())

	if view := m.View(); !strings.Contains(view, "не спрашивали") {
		t.Errorf("экран молчит о непроверенных ссылках\n--- view ---\n%s", view)
	}
}

// Чистая база говорит, что она чиста. Пустой экран читается как поломка ровно
// так же, как как отсутствие находок.
func TestHealth_saysSoWhenNothingIsWrong(t *testing.T) {
	m := healthModel(&stubHealth{h: audit.Health{Links: audit.LinkHealth{Alive: 10, WithURL: 10}}})

	m = press(m, tab())

	if view := m.View(); !strings.Contains(view, "находок нет") {
		t.Errorf("экран не сказал, что находок нет\n--- view ---\n%s", view)
	}
}

// Отказ назван, а не показан нулями: экран с нулями утверждает, что база
// в порядке, ровно тогда, когда о ней ничего не известно.
func TestHealth_namesAFailure(t *testing.T) {
	m := healthModel(&stubHealth{err: errors.New("каталог не прочитан")})

	m = press(m, tab())

	if view := m.View(); !strings.Contains(view, "каталог не прочитан") {
		t.Errorf("экран не назвал отказ\n--- view ---\n%s", view)
	}
}

// Tab перебирает экраны по кругу, а не открывает один заранее назначенный.
// Без этого второй экран в терминале некуда повесить: на списке любая буква
// уходит в поиск, так что свободных клавиш там нет.
func TestTabCyclesThroughScreens(t *testing.T) {
	fin := &stubFinances{sum: sampleSummary()}
	m := tui.NewModel(nil).WithFinances(fin).WithHealth(&stubHealth{h: sampleHealth()})

	m = press(m, tab())
	if !m.OnFinances() {
		t.Fatal("первый Tab не открыл финансы")
	}
	m = press(m, tab())
	if !m.OnHealth() {
		t.Fatal("второй Tab не открыл здоровье")
	}
	m = press(m, tab())
	if m.OnFinances() || m.OnHealth() {
		t.Fatal("третий Tab не вернул в поиск")
	}
}

// Экран, для которого нет источника, в круге отсутствует — правило, которому
// уже следуют пишущие клавиши финансов.
func TestTabSkipsScreensWithoutASource(t *testing.T) {
	m := tui.NewModel(nil).WithHealth(&stubHealth{h: sampleHealth()})

	m = press(m, tab())

	if m.OnFinances() {
		t.Fatal("финансы открылись без ledger")
	}
	if !m.OnHealth() {
		t.Fatal("Tab не открыл здоровье, хотя это единственный доступный экран")
	}
}

// Сводка перечитывается при каждом открытии: аудит отвечает на вопрос о
// каталоге, который правят из этого же терминала, и ответ, снятый один раз при
// запуске, устареет к моменту, когда на него посмотрят.
func TestHealth_rereadsOnEveryOpen(t *testing.T) {
	stub := &stubHealth{h: sampleHealth()}
	m := healthModel(stub)

	m = press(press(m, tab()), tab())
	press(m, tab())

	if stub.calls < 2 {
		t.Errorf("сводка запрошена %d раз, ожидалось не меньше 2", stub.calls)
	}
}
