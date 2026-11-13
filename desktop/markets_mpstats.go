package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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

// mpstatsTTL — сколько ряд ниши живет в памяти окна: «Посчитать», «Посчитать
// моделью» и пересчет со своим файлом не тратят тариф заново (ревью #605).
// ponytail: память процесса; потолок — после перезапуска окна ряд качается
// снова; путь — кэш на диске, если тариф станет тесным.
const mpstatsTTL = 10 * time.Minute

type nicheEntry struct {
	at     time.Time
	series [2]domain.DailySales
	files  []forecastFile
}

// nicheCache keeps MPStats niche series by «id|путь|сегодня».
type nicheCache struct {
	mu sync.Mutex
	m  map[string]nicheEntry
}

func (c *nicheCache) get(k string, now time.Time) (nicheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	return e, ok && now.Sub(e.at) < mpstatsTTL
}

func (c *nicheCache) put(k string, e nicheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]nicheEntry{}
	}
	c.m[k] = e
}

// nicheSeries reads the two niche series: из MPStats или файлами.
func (f *folderSettings) nicheSeries(ctx context.Context, hc *http.Client, last, now string, today time.Time) ([2]domain.DailySales, []forecastFile, error) {
	var series [2]domain.DailySales
	lastMP, nowMP := strings.HasPrefix(last, "mpstats:"), strings.HasPrefix(now, "mpstats:")
	switch {
	case !lastMP && !nowMP:
		return readNiche(last, now)
	case last != now: // обе — из MPStats одним источником или обе файлами (ревью #605)
		return series, nil, errors.New("обе ниши — из MPStats или обе файлами: нажмите «Из MPStats» еще раз или выберите оба файла")
	}
	src := strings.TrimPrefix(last, "mpstats:")
	key := src + "|" + today.Format(time.DateOnly)
	if e, ok := f.mpstatsNiche.get(key, time.Now()); ok {
		return e.series, e.files, nil
	}
	cl, path, err := f.mpstatsSource(src, hc)
	if err != nil {
		return series, nil, err
	}
	from, to := ozonWindow(today)
	spans := [2][2]time.Time{{from.AddDate(-1, 0, 0), to.AddDate(-1, 0, mpstatsHorizon)}, {from, to}}
	var files [2]forecastFile
	var errs [2]error
	var wg sync.WaitGroup
	for i, sp := range spans { // ряды независимы: качаются сразу, а не друг за другом (ревью #605)
		wg.Go(func() { series[i], files[i], errs[i] = mpstatsSpan(ctx, cl, path, sp[0], sp[1]) })
	}
	wg.Wait()
	if err := cmp.Or(errs[0], errs[1]); err != nil {
		return series, nil, err
	}
	f.mpstatsNiche.put(key, nicheEntry{at: time.Now(), series: series, files: files[:]})
	return series, files[:], nil
}

// mpstatsSpan reads one niche series for days from..to.
func mpstatsSpan(ctx context.Context, cl mpstats.Client, path string, from, to time.Time) (domain.DailySales, forecastFile, error) {
	days, err := cl.CategoryByDay(ctx, path, from, to)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, errors.New(mpstatsMessage(err))
	}
	ds := make([]domain.DaySales, 0, len(days))
	for _, d := range days {
		ds = append(ds, domain.DaySales{Day: d.Day, Value: d.Sales})
	}
	s, err := domain.NewDailySales(ds)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, err
	}
	got := s.Days()
	if len(got) == 0 {
		return domain.DailySales{}, forecastFile{}, fmt.Errorf("в MPStats нет продаж ниши «%s» за %s – %s: проверьте путь категории", path, from.Format("02.01.2006"), to.Format("02.01.2006"))
	}
	return s, forecastFile{File: "MPStats · " + path, Format: "mpstats", Days: len(got),
		From: got[0].Day.Format("02.01.2006"), To: got[len(got)-1].Day.Format("02.01.2006")}, nil
}

// mpstatsSource resolves «<id>|<путь>» into a client with the saved key.
func (f *folderSettings) mpstatsSource(src string, hc *http.Client) (mpstats.Client, string, error) {
	id, path, _ := strings.Cut(src, "|")
	if strings.TrimSpace(path) == "" {
		return mpstats.Client{}, "", errors.New("не выбрана ниша: путь категории Ozon, как в MPStats")
	}
	c, ok := f.snapshot().cfg.Connection(id)
	if !ok || c.Kind != desktopconfig.ConnMPStats {
		return mpstats.Client{}, "", errors.New("подключения MPStats нет — добавьте ключ в Настройках → Подключения")
	}
	if !c.Enabled {
		return mpstats.Client{}, "", fmt.Errorf("подключение «%s» выключено", c.Name)
	}
	key, err := f.tokens.Get(connKeyAccount(id))
	if err != nil {
		return mpstats.Client{}, "", errors.New("ключа MPStats нет в Связке ключей — добавьте его заново")
	}
	return mpstats.Client{BaseURL: f.mpstatsBase(), Token: key, HTTP: hc}, path, nil
}
