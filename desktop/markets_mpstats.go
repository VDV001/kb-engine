package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/domain"
)

// «Прогноз» берет нишу из MPStats (отзыв владельца 13.11): обе ниши — одним
// источником «mpstats:<id подключения>|<путь категории Ozon>». Сейчас — те же
// 90 дней до вчера, что свои продажи из кабинета; прошлый год — эти дни годом
// раньше и mpstatsHorizon дней после них: они и прогнозируются.
const mpstatsHorizon = 120

// mpstatsBase — адрес API: в окне настоящий, в тестах — поддельный.
func (f *folderSettings) mpstatsBase() string {
	if f.mpstatsURL != "" {
		return f.mpstatsURL
	}
	return mpstats.DefaultURL
}

// nicheSeries reads the two niche series: из MPStats или файлами.
func (f *folderSettings) nicheSeries(ctx context.Context, hc *http.Client, last, now string, today time.Time) ([2]domain.DailySales, []forecastFile, error) {
	var series [2]domain.DailySales
	src, ok := strings.CutPrefix(last, "mpstats:")
	if !ok {
		return readNiche(last, now)
	}
	if now != last {
		return series, nil, errors.New("обе ниши — из MPStats: нажмите «Из MPStats» еще раз")
	}
	id, path, _ := strings.Cut(src, "|")
	if strings.TrimSpace(path) == "" {
		return series, nil, errors.New("не выбрана ниша: путь категории Ozon, как в MPStats")
	}
	c, ok := f.snapshot().cfg.Connection(id)
	if !ok || c.Kind != desktopconfig.ConnMPStats {
		return series, nil, errors.New("подключения MPStats нет — добавьте ключ в Настройках → Подключения")
	}
	if !c.Enabled {
		return series, nil, fmt.Errorf("подключение «%s» выключено", c.Name)
	}
	key, err := f.tokens.Get(connKeyAccount(id))
	if err != nil {
		return series, nil, errors.New("ключа MPStats нет в Связке ключей — добавьте его заново")
	}
	cl := mpstats.Client{BaseURL: f.mpstatsBase(), Token: key, HTTP: hc}
	from, to := ozonWindow(today)
	spans := [2][2]time.Time{{from.AddDate(-1, 0, 0), to.AddDate(-1, 0, mpstatsHorizon)}, {from, to}}
	var files []forecastFile
	for i, sp := range spans {
		days, err := cl.CategoryByDay(ctx, path, sp[0], sp[1])
		if err != nil {
			return series, nil, errors.New(mpstatsMessage(err))
		}
		ds := make([]domain.DaySales, 0, len(days))
		for _, d := range days {
			ds = append(ds, domain.DaySales{Day: d.Day, Value: d.Sales})
		}
		if series[i], err = domain.NewDailySales(ds); err != nil {
			return series, nil, err
		}
		got := series[i].Days()
		if len(got) == 0 {
			return series, nil, fmt.Errorf("в MPStats нет продаж ниши «%s» за %s – %s: проверьте путь категории", path, sp[0].Format("02.01.2006"), sp[1].Format("02.01.2006"))
		}
		files = append(files, forecastFile{File: "MPStats · " + path, Format: "mpstats", Days: len(got),
			From: got[0].Day.Format("02.01.2006"), To: got[len(got)-1].Day.Format("02.01.2006")})
	}
	return series, files, nil
}
