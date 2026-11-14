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
// сохраненной; номер отправления из свежей — только свежий; разрыв между
// загрузками — пропуск. Возвраты не копятся: своего id у них нет, старые
// статусы зависали бы, а нужны они пропажам за последние 90 дней (ревью #618).
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
	fresher := make(map[string]bool, len(fresh.Posts))
	for _, p := range fresh.Posts {
		fresher[p.Number] = p.Number != ""
	}
	old := slices.DeleteFunc(PostingsIn(saved.Posts, saved.From, keep), func(p OzonPosting) bool { return fresher[p.Number] })
	out.Posts = append(old, fresh.Posts...)
	if fresh.AccFailed {
		out.Acc = slices.Clone(saved.Acc)
	} else {
		out.Acc = append(AccrualsIn(saved.Acc, saved.From, keep), fresh.Acc...)
	}
	if fresh.RetFailed {
		out.Rets = slices.Clone(saved.Rets)
	}
	out.Gaps = slices.Clone(saved.Gaps)
	if saved.To.Before(keepTo) {
		out.Gaps = append(out.Gaps, DayRange{From: saved.To.AddDate(0, 0, 1), To: keepTo})
	}
	return out
}
