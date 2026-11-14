package markets_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// История кабинета (#614): Ozon отдает 90 дней, окно копит все с первой
// загрузки. Дни свежего окна — из новой загрузки (статусы и начисления там
// новее), раньше его — из сохраненной; разрыв между загрузками — пропуск.
func TestMergeShop(t *testing.T) {
	loc := time.FixedZone("UZT", 5*3600)
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, loc) }
	at := func(m time.Month, d int) time.Time { return day(m, d).Add(10 * time.Hour) }
	acc := func(m time.Month, d int, kop int64) markets.OzonAccrual {
		return markets.OzonAccrual{Day: time.Date(2026, m, d, 0, 0, 0, 0, time.UTC), SKU: "1", Amount: domain.NewMoney(kop)}
	}
	saved := markets.ShopHistory{
		From: day(8, 15), To: day(11, 12),
		Posts: []markets.OzonPosting{
			{Number: "old-15.08", At: at(8, 15), Status: "delivered"},
			{Number: "both-20.08", At: at(8, 20), Status: "delivering"},
			{Number: "undated"},
		},
		Acc:  []markets.OzonAccrual{acc(8, 15, 100), acc(8, 20, 200)},
		Rets: []markets.OzonReturn{{Posting: "r1", SKU: "1", Type: "Cancellation", Status: "MovingToOzon"}, {Posting: "r2", SKU: "1", Type: "Cancellation", Status: "WriteOff"}},
	}
	fresh := markets.ShopHistory{
		From: day(8, 16), To: day(11, 13),
		Posts: []markets.OzonPosting{{Number: "both-20.08", At: at(8, 20), Status: "delivered"}, {Number: "new-13.11", At: at(11, 13)}},
		Acc:   []markets.OzonAccrual{acc(8, 20, 250), acc(11, 13, 300)},
		Rets:  []markets.OzonReturn{{Posting: "r1", SKU: "1", Type: "Cancellation", Status: "ReturnedToOzon"}},
	}
	posts := func(h markets.ShopHistory) string {
		var s []string
		for _, p := range h.Posts {
			s = append(s, p.Number+":"+p.Status)
		}
		return strings.Join(s, " ")
	}
	accs := func(h markets.ShopHistory) (n int64) {
		for _, a := range h.Acc {
			n += a.Amount.Kopecks()
		}
		return n
	}
	rets := func(h markets.ShopHistory) string {
		var s []string
		for _, r := range h.Rets {
			s = append(s, r.Posting+":"+r.Status)
		}
		return strings.Join(s, " ")
	}

	got := markets.MergeShop(saved, fresh)
	if !got.From.Equal(day(8, 15)) || !got.To.Equal(day(11, 13)) || len(got.Gaps) != 0 {
		t.Errorf("days %v – %v, gaps %v; want 15.08 – 13.11 without gaps", got.From, got.To, got.Gaps)
	}
	if p := posts(got); p != "old-15.08:delivered both-20.08:delivered new-13.11:" {
		t.Errorf("posts %q: the fresh window wins, earlier days stay, undated old ones go", p)
	}
	if n := accs(got); n != 100+250+300 {
		t.Errorf("accruals %d kopecks, want 650: 15.08 kept, 20.08 from the fresh load", n)
	}
	// Возвраты не копятся (ревью #618): у них нет своего id, а старые статусы
	// зависали бы навсегда; они нужны пропажам, а те смотрят последние 90 дней.
	if r := rets(got); r != "r1:ReturnedToOzon" {
		t.Errorf("returns %q: the fresh list only", r)
	}

	t.Run("первая загрузка", func(t *testing.T) {
		if got := markets.MergeShop(markets.ShopHistory{}, fresh); posts(got) != posts(fresh) || !got.From.Equal(fresh.From) {
			t.Errorf("got %q from %v", posts(got), got.From)
		}
	})
	t.Run("разрыв между загрузками", func(t *testing.T) {
		old := markets.ShopHistory{From: day(5, 1), To: day(6, 30), Posts: []markets.OzonPosting{{Number: "may", At: at(5, 10)}}}
		got := markets.MergeShop(old, fresh)
		if !got.From.Equal(day(5, 1)) || len(got.Gaps) != 1 || !got.Gaps[0].From.Equal(day(7, 1)) || !got.Gaps[0].To.Equal(day(8, 15)) {
			t.Errorf("from %v, gaps %+v; want from 01.05 and the gap 01.07 – 15.08", got.From, got.Gaps)
		}
		if !strings.HasPrefix(posts(got), "may:") {
			t.Errorf("posts %q", posts(got))
		}
		// Пропуск переживает следующую загрузку.
		if again := markets.MergeShop(got, markets.ShopHistory{From: day(8, 17), To: day(11, 14)}); len(again.Gaps) != 1 {
			t.Errorf("gaps after the next load %+v", again.Gaps)
		}
	})
	t.Run("начисления и возвраты не загрузились", func(t *testing.T) {
		f := fresh
		f.Acc, f.Rets, f.AccFailed, f.RetFailed = nil, nil, true, true
		got := markets.MergeShop(saved, f)
		if accs(got) != 300 || rets(got) != "r1:MovingToOzon r2:WriteOff" {
			t.Errorf("accruals %d, returns %q: a failed read keeps what was saved", accs(got), rets(got))
		}
	})
	t.Run("отправление на стыке окон — одно", func(t *testing.T) {
		s := saved
		s.Posts = append(slices.Clone(saved.Posts), markets.OzonPosting{Number: "new-13.11", At: at(8, 15), Status: "awaiting"})
		got := markets.MergeShop(s, fresh)
		if p := posts(got); p != "old-15.08:delivered both-20.08:delivered new-13.11:" {
			t.Errorf("posts %q: a number from the fresh load is taken once, fresh", p)
		}
	})
	t.Run("сохраненное не раньше свежего", func(t *testing.T) {
		if got := markets.MergeShop(fresh, fresh); posts(got) != posts(fresh) || accs(got) != accs(fresh) {
			t.Errorf("a reload of the same window doubled: %q, %d", posts(got), accs(got))
		}
	})
	if len(saved.Posts) != 3 || len(fresh.Posts) != 2 {
		t.Error("MergeShop changed its input")
	}
}
