package markets

import (
	"slices"
	"time"
)

// ShopHistory — данные кабинета за дни From..To: Ozon отдает 90 дней, окно
// копит все с первой загрузки (#614). AccFailed, RetFailed — у свежей
// загрузки начисления или возвраты не прочитались: остаются сохраненные.
type ShopHistory struct {
	From, To             time.Time
	Posts                []OzonPosting
	Acc                  []OzonAccrual
	Rets                 []OzonReturn
	AccFailed, RetFailed bool
	Gaps                 []DayRange // дни без данных: окно не загружалось дольше 90 дней
}

// DayRange — дни From..To включительно.
type DayRange struct{ From, To time.Time }

// MergeShop adds a fresh load to the saved history: дни свежего окна — из
// новой загрузки (статусы и начисления там новее), раньше его — из
// сохраненной; разрыв между загрузками — пропуск. Возвраты Ozon отдает по дате
// логистики, а не по дню отправления, поэтому они сливаются по ключу.
func MergeShop(saved, fresh ShopHistory) ShopHistory {
	out := fresh
	out.Posts, out.Acc, out.Rets = slices.Clone(fresh.Posts), slices.Clone(fresh.Acc), slices.Clone(fresh.Rets)
	out.Gaps = nil
	if saved.From.IsZero() || !saved.From.Before(fresh.From) {
		if fresh.AccFailed {
			out.Acc = slices.Clone(saved.Acc)
		}
		if fresh.RetFailed {
			out.Rets = slices.Clone(saved.Rets)
		}
		return out
	}
	keepTo := fresh.From.AddDate(0, 0, -1)
	keep := keepTo
	if saved.To.Before(keepTo) {
		keep = saved.To
	}
	out.From = saved.From
	out.Posts = append(PostingsIn(saved.Posts, saved.From, keep), fresh.Posts...)
	if fresh.AccFailed {
		out.Acc = slices.Clone(saved.Acc)
	} else {
		out.Acc = append(AccrualsIn(saved.Acc, saved.From, keep), fresh.Acc...)
	}
	if fresh.RetFailed {
		out.Rets = slices.Clone(saved.Rets)
	} else {
		out.Rets = mergeReturns(saved.Rets, fresh.Rets)
	}
	out.Gaps = slices.Clone(saved.Gaps)
	if saved.To.Before(keepTo) {
		out.Gaps = append(out.Gaps, DayRange{From: saved.To.AddDate(0, 0, 1), To: keepTo})
	}
	return out
}

// mergeReturns keeps saved returns with the fresh status of the same return.
func mergeReturns(saved, fresh []OzonReturn) []OzonReturn {
	key := func(r OzonReturn) string { return r.Posting + "|" + r.SKU + "|" + r.Type }
	at := make(map[string]int, len(fresh))
	for i, r := range fresh {
		at[key(r)] = i
	}
	out := make([]OzonReturn, 0, len(saved)+len(fresh))
	used := make([]bool, len(fresh))
	for _, r := range saved {
		if i, ok := at[key(r)]; ok {
			r, used[i] = fresh[i], true
		}
		out = append(out, r)
	}
	for i, r := range fresh {
		if !used[i] {
			out = append(out, r)
		}
	}
	return out
}
