package markets_test

import (
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func TestSearchPlaces(t *testing.T) {
	from := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	days := func(v ...int) []int { // место по дням с from; 0 — не в выдаче
		d := make([]int, 31)
		copy(d, v)
		return d
	}
	own := []markets.OwnQuery{
		{Text: "футболка оверсайз", Count: 40, Position: 12, Days: days(10, 0, 14)},
		{Text: "футболка женская", Count: 900, Position: 30, Days: days(25, 31, 0, 0, 28)},
		{Text: "футболка редкая", Count: 900, Position: 5, Days: days(5)},
		{Text: "футболка пропала", Count: 70, Position: 8, Days: days()},
	}
	got := markets.SearchPlaces(own, from, 3)
	if len(got) != 3 {
		t.Fatalf("%d rows, want the limit 3: %+v", len(got), got)
	}
	// Частые запросы первыми; при равной частоте — где место выше.
	if got[0].Query != "футболка редкая" || got[1].Query != "футболка женская" || got[2].Query != "футболка пропала" {
		t.Fatalf("order %+v", got)
	}
	w := got[1]
	if w.Count != 900 || w.Avg != 30 || w.Last != 28 || !w.LastDay.Equal(from.AddDate(0, 0, 4)) || w.Seen != 3 || w.Days != 31 {
		t.Errorf("женская %+v", w)
	}
	if p := got[2]; p.Last != 0 || !p.LastDay.IsZero() || p.Seen != 0 {
		t.Errorf("пропала %+v — без дней в выдаче нет и последнего места", p)
	}
	if all := markets.SearchPlaces(own, from, 0); len(all) != 4 {
		t.Errorf("limit 0 — все: %d", len(all))
	}
	if markets.SearchPlaces(nil, from, 3) == nil {
		t.Error("nil rows — окно ждет пустой список, а не null")
	}
}

// Ревью #673: место 0 — «нет данных», а не выше всех; дни не по длине периода
// — без дат.
func TestSearchPlacesNoData(t *testing.T) {
	from := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	own := []markets.OwnQuery{
		{Text: "без места", Count: 900, Position: 0, Days: make([]int, 31)},
		{Text: "место 5", Count: 900, Position: 5, Days: append(make([]int, 30), 5)},
		{Text: "короткий", Count: 10, Position: 3, Days: []int{3, 4}},
	}
	got := markets.SearchPlaces(own, from, 0)
	if got[0].Query != "место 5" || got[1].Query != "без места" {
		t.Fatalf("order %+v", got)
	}
	if !got[0].LastDay.Equal(from.AddDate(0, 0, 30)) {
		t.Errorf("last day %v", got[0].LastDay)
	}
	if c := got[2]; c.Last != 4 || !c.LastDay.IsZero() || c.Seen != 2 {
		t.Errorf("short days %+v — длина не 31: место есть, даты нет", c)
	}
}
