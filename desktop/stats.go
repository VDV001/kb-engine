package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

// callLog keeps model calls and reads them back for the statistics screen.
type callLog interface {
	usage.Recorder
	Load() ([]domain.ModelCall, int, error)
}

// purposeTitles are the call purposes as the person reads them.
var purposeTitles = map[domain.CallPurpose]string{
	domain.CallAnswer: "Ответ в чате", domain.CallSearch: "Поиск моделью",
	domain.CallExtract: "Разбор портрета", domain.CallCompress: "Сжатие портрета",
	domain.CallListen: "Распознавание речи", domain.CallSpeak: "Облачный голос",
	domain.CallDescribe: "Описание версий",
}

// maxStatsDays bounds the period: экран предлагает 7, 30 и 90 дней.
const maxStatsDays = 366

// statsTotals is usage.Totals for the page.
type statsTotals struct {
	Calls     int `json:"calls"`
	Failed    int `json:"failed"`
	Uncounted int `json:"uncounted"` // без счета от провайдера: токены неизвестны
	In        int `json:"in"`
	Out       int `json:"out"`
}

type statsDay struct {
	Date string `json:"date"`
	statsTotals
}

type statsGroup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	statsTotals
}

// statsCall is one call for the journal screen; текста вопроса нет и в журнале.
type statsCall struct {
	At      string `json:"at"` // RFC 3339 в поясе Мака
	Purpose string `json:"purpose"`
	Title   string `json:"title"`
	Model   string `json:"model"`
	In      int    `json:"in"`
	Out     int    `json:"out"`
	Counted bool   `json:"counted"`
	FirstMS int64  `json:"firstMs"`
	TookMS  int64  `json:"tookMs"`
	Failed  bool   `json:"failed"`
}

// statsView is the statistics of the last days for the page.
type statsView struct {
	Days     []statsDay   `json:"days"`
	Purposes []statsGroup `json:"purposes"`
	Models   []statsGroup `json:"models"`
	Total    statsTotals  `json:"total"`
	Prev     statsTotals  `json:"prev"`
	Calls    []statsCall  `json:"calls"`
	Bad      int          `json:"bad"` // нечитаемых строк журнала
}

func totalsView(t usage.Totals) statsTotals {
	return statsTotals{t.Calls, t.Failed, t.Uncounted, t.In, t.Out}
}

// Stats returns the statistics of the last days days.
func (d *chatDesk) Stats(days int) (statsView, error) {
	if days < 1 || days > maxStatsDays {
		return statsView{}, fmt.Errorf("период %d дней: можно от 1 до %d", days, maxStatsDays)
	}
	if d.calls == nil {
		return statsView{}, errors.New("журнал вызовов модели не ведется")
	}
	calls, bad, err := d.calls.Load()
	if err != nil {
		return statsView{}, err
	}
	r := usage.NewReport(calls, d.now(), days)
	v := statsView{Total: totalsView(r.Total), Prev: totalsView(r.Prev), Bad: bad,
		Days: make([]statsDay, 0, len(r.Days)), Purposes: []statsGroup{}, Models: []statsGroup{}, Calls: make([]statsCall, 0, len(r.Calls))}
	for _, x := range r.Days {
		v.Days = append(v.Days, statsDay{x.Date, totalsView(x.Totals)})
	}
	for _, x := range r.Purposes {
		v.Purposes = append(v.Purposes, statsGroup{string(x.Purpose), purposeTitles[x.Purpose], totalsView(x.Totals)})
	}
	for _, x := range r.Models {
		v.Models = append(v.Models, statsGroup{x.Model, x.Model, totalsView(x.Totals)})
	}
	for _, c := range r.Calls {
		v.Calls = append(v.Calls, statsCall{c.At().In(d.now().Location()).Format(time.RFC3339), string(c.Purpose()), purposeTitles[c.Purpose()],
			c.Model(), c.In(), c.Out(), c.Counted(), c.FirstToken().Milliseconds(), c.Took().Milliseconds(), c.Failed()})
	}
	return v, nil
}
