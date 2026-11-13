package markets_test

import (
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// «Магазин» за выбранные даты (отзыв владельца 13.11): отправления и начисления
// из загруженных 90 дней оставляются по дню, оба края включительно.
func TestShopPeriod(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	from, to := time.Date(2026, 11, 1, 0, 0, 0, 0, msk), time.Date(2026, 11, 7, 0, 0, 0, 0, msk)
	posts := []markets.OzonPosting{
		{Number: "до", At: time.Date(2026, 10, 31, 23, 59, 0, 0, msk)},
		{Number: "первый день", At: from},
		{Number: "последний день", At: time.Date(2026, 11, 7, 23, 59, 0, 0, msk)},
		{Number: "после", At: time.Date(2026, 11, 8, 0, 0, 0, 0, msk)},
		{Number: "без даты"},
	}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) } // начисление — день, полночь UTC
	acc := []markets.OzonAccrual{{Unit: "a", Day: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)}, {Unit: "b", Day: day(1)}, {Unit: "c", Day: day(7)}, {Unit: "d", Day: day(8)}}

	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"отправления", numbers(markets.PostingsIn(posts, from, to)), []string{"первый день", "последний день"}},
		{"начисления", units(markets.AccrualsIn(acc, from, to)), []string{"b", "c"}},
	}
	for _, tc := range tests {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func numbers(ps []markets.OzonPosting) (out []string) {
	for _, p := range ps {
		out = append(out, p.Number)
	}
	return out
}

func units(as []markets.OzonAccrual) (out []string) {
	for _, a := range as {
		out = append(out, a.Unit)
	}
	return out
}
