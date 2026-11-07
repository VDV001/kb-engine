package usage

import (
	"cmp"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Totals sums calls: токены — только по вызовам со счетом; без счета —
// отдельным числом, чтобы «неизвестно» не выглядело нулем.
type Totals struct {
	Calls, Failed, Uncounted int
	In, Out                  int
	Answers, Searches        int
}

func (t *Totals) add(c domain.ModelCall) {
	t.Calls++
	t.In += c.In()
	t.Out += c.Out()
	if c.Failed() {
		t.Failed++
	}
	if !c.Counted() {
		t.Uncounted++
	}
	switch c.Purpose() {
	case domain.CallAnswer:
		t.Answers++
	case domain.CallSearch:
		t.Searches++
	}
}

// DayStat is one day of the period.
type DayStat struct {
	Date string // ГГГГ-ММ-ДД в часовом поясе человека
	Totals
}

// PurposeStat sums calls of one purpose.
type PurposeStat struct {
	Purpose domain.CallPurpose
	Totals
}

// ModelStat sums calls of one model.
type ModelStat struct {
	Model string
	Totals
}

// Report is the statistics of the last days: по дням (пустые тоже),
// назначениям и моделям, итоги периода и такого же прошлого, вызовы периода
// свежими первыми.
type Report struct {
	Days     []DayStat
	Purposes []PurposeStat
	Models   []ModelStat
	Total    Totals
	Prev     Totals
	Calls    []domain.ModelCall
}

// NewReport builds the report for days days ending today in now's time zone.
func NewReport(calls []domain.ModelCall, now time.Time, days int) Report {
	loc := now.Location()
	y, m, d := now.Date()
	from := time.Date(y, m, d-days+1, 0, 0, 0, 0, loc)
	prevFrom := time.Date(y, m, d-2*days+1, 0, 0, 0, 0, loc)
	r := Report{Days: make([]DayStat, days)}
	idx := make(map[string]int, days)
	for i := range days {
		r.Days[i].Date = from.AddDate(0, 0, i).Format(time.DateOnly)
		idx[r.Days[i].Date] = i
	}
	byPurpose := map[domain.CallPurpose]*Totals{}
	byModel := map[string]*Totals{}
	for _, c := range calls {
		at := c.At().In(loc)
		switch {
		case !at.Before(from):
			i, ok := idx[at.Format(time.DateOnly)]
			if !ok {
				continue // из будущего: часы Мака сбились
			}
			r.Days[i].add(c)
			r.Total.add(c)
			if byPurpose[c.Purpose()] == nil {
				byPurpose[c.Purpose()] = &Totals{}
			}
			byPurpose[c.Purpose()].add(c)
			if byModel[c.Model()] == nil {
				byModel[c.Model()] = &Totals{}
			}
			byModel[c.Model()].add(c)
			r.Calls = append(r.Calls, c)
		case !at.Before(prevFrom):
			r.Prev.add(c)
		}
	}
	for p, t := range byPurpose {
		r.Purposes = append(r.Purposes, PurposeStat{p, *t})
	}
	slices.SortFunc(r.Purposes, func(a, b PurposeStat) int { return cmp.Compare(b.In+b.Out, a.In+a.Out) })
	for m, t := range byModel {
		r.Models = append(r.Models, ModelStat{m, *t})
	}
	slices.SortFunc(r.Models, func(a, b ModelStat) int { return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Model, b.Model)) })
	slices.SortStableFunc(r.Calls, func(a, b domain.ModelCall) int { return b.At().Compare(a.At()) })
	return r
}
