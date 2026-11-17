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
