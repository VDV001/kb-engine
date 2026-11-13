package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Кабинет Ozon — подключение окна (#580): Client-Id в config.json, ключ в
// Связке ключей. «Магазин» читает кабинет по API только на чтение и дает
// советы; ключ уходит только на api-seller.ozon.ru.

// ozonDays — за сколько дней «Магазин» берет отправления: столько же отдает
// аналитика Ozon.
const ozonDays = 90

// ozonTimeout — на чтение кабинета: 3 700 отправлений — около 40 страниц.
const ozonTimeout = 5 * time.Minute

// AddOzon saves an Ozon cabinet as a connection.
func (f *folderSettings) AddOzon(name, clientID, key string) (connRow, error) {
	name, clientID, key = strings.TrimSpace(name), strings.TrimSpace(clientID), strings.TrimSpace(key)
	if clientID == "" || strings.IndexFunc(clientID, func(r rune) bool { return !unicode.IsDigit(r) }) >= 0 {
		return connRow{}, errors.New("Client-Id — номер кабинета из цифр: Ozon Seller → Настройки → Seller API")
	}
	if key == "" {
		return connRow{}, errors.New("нужен API-ключ: Ozon Seller → Настройки → Seller API")
	}
	if name == "" {
		name = "Ozon " + clientID
	}
	conn := desktopconfig.Connection{ID: "ozon-" + randomString()[:8], Kind: desktopconfig.ConnOzon, Name: name, Account: clientID, Enabled: true}
	if err := desktopconfig.CheckConnections([]desktopconfig.Connection{conn}); err != nil {
		return connRow{}, err
	}
	if err := f.tokens.Set(connKeyAccount(conn.ID), key); err != nil {
		return connRow{}, fmt.Errorf("ключ не сохранен в Связке ключей: %w", err)
	}
	if err := f.updateConfig(func(cfg *desktopconfig.Config) { cfg.Connections = append(cfg.Connections, conn) }); err != nil {
		_ = f.tokens.Delete(connKeyAccount(conn.ID))
		return connRow{}, err
	}
	return f.ozonConn(conn), nil
}

func (f *folderSettings) ozonConn(c desktopconfig.Connection) connRow {
	r := connRow{ID: c.ID, Kind: "ozon", Name: c.Name, Detail: "Ozon Seller API, только чтение · Client-Id " + c.Account,
		UsedBy: []string{"Маркеты → Магазин"}, Section: "conn", Own: true, Enabled: c.Enabled}
	_, err := f.tokens.Get(connKeyAccount(c.ID))
	switch {
	case !c.Enabled:
		r.Status = "выключено"
	case err == nil:
		r.Status, r.OK = "ключ в Связке ключей", true
	case !errors.Is(err, keychain.ErrNotFound):
		r.Status = keychainNote
	default:
		r.Status = "нет ключа — добавьте кабинет заново"
	}
	return r
}

// MarketsForecastOzon forecasts with own sales from an Ozon cabinet.
func (a *App) MarketsForecastOzon(lastYear, nicheNow, id string) (forecastView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return a.settings.forecastOzon(ctx, ozon.DefaultURL, nil, lastYear, nicheNow, id, time.Now())
}

// MarketsOzon reads the shop from an Ozon cabinet for the last 90 days.
func (a *App) MarketsOzon(id string) (shopView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return a.settings.ozonShop(ctx, ozon.DefaultURL, nil, id, time.Now())
}

// ozonCabinet returns the client of cabinet id with its key from the keychain.
func (f *folderSettings) ozonCabinet(id, base string, hc *http.Client) (ozon.Client, desktopconfig.Connection, error) {
	c, ok := f.snapshot().cfg.Connection(id)
	if !ok || c.Kind != desktopconfig.ConnOzon {
		return ozon.Client{}, c, errors.New("кабинета Ozon нет в «Подключениях» — добавьте его")
	}
	if !c.Enabled {
		return ozon.Client{}, c, fmt.Errorf("подключение «%s» выключено", c.Name)
	}
	key, err := f.tokens.Get(connKeyAccount(id))
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return ozon.Client{}, c, fmt.Errorf("ключа кабинета «%s» нет в Связке ключей — добавьте кабинет заново", c.Name)
		}
		return ozon.Client{}, c, keychainNote2(err)
	}
	return ozon.Client{BaseURL: base, ClientID: c.Account, Key: key, HTTP: hc}, c, nil
}

// ozonWindow — 90 дней до вчерашнего включительно: сегодняшние отправления еще идут.
func ozonWindow(now time.Time) (from, to time.Time) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	to = day.AddDate(0, 0, -1)
	return to.AddDate(0, 0, -(ozonDays - 1)), to
}

// ozonShop reads cabinet id through base: отправления за 90 дней и остатки сейчас.
func (f *folderSettings) ozonShop(ctx context.Context, base string, hc *http.Client, id string, now time.Time) (shopView, error) {
	cl, c, err := f.ozonCabinet(id, base, hc)
	if err != nil {
		return shopView{}, err
	}
	from, to := ozonWindow(now)
	posts, err := cl.Postings(ctx, from, to)
	if err != nil {
		return shopView{}, ozonErr(c.Name, err)
	}
	stock, err := cl.Stocks(ctx)
	if err != nil {
		return shopView{}, ozonErr(c.Name, err)
	}
	rep, err := markets.OzonShop(posts, stock, from, to)
	if err != nil {
		return shopView{}, err
	}
	v := shopViewOf(rep)
	v.File, v.Path, v.Source = "Ozon · "+c.Name, id, "ozon"
	return v, nil
}

// ozonErr names an Ozon refusal in words.
func ozonErr(name string, err error) error {
	switch {
	case errors.Is(err, ozon.ErrUnauthorized):
		return fmt.Errorf("кабинет «%s»: Ozon не принял Client-Id или ключ — проверьте их или выпустите ключ заново", name)
	case errors.Is(err, ozon.ErrTooMany):
		return errors.New("запросы слишком частые, Ozon просит подождать — загрузите снова через минуту")
	case errors.Is(err, ozon.ErrTooBig):
		return fmt.Errorf("кабинет «%s» слишком большой для одного чтения (больше 50 000 отправлений одной схемы за 90 дней или 500 000 карточек) — такой окно пока не читает", name)
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("сервер Ozon не ответил за пять минут — загрузите снова")
	}
	return fmt.Errorf("кабинет «%s»: %w", name, err)
}

// adviceOf turns the domain's advice into the window's rows.
func adviceOf(ps []*domain.ShopProduct) []shopAdvice {
	out := []shopAdvice{}
	for _, a := range domain.AdviseShop(ps) {
		out = append(out, shopAdvice{Kind: string(a.Kind), SKUs: a.SKUs, Units: a.Units})
	}
	return out
}

// forecastOzon forecasts with own sales from cabinet id instead of the third
// file: штуки без отмен по дням за 90 дней до вчера.
func (f *folderSettings) forecastOzon(ctx context.Context, base string, hc *http.Client, last, now, id string, today time.Time) (forecastView, error) {
	series, files, err := f.ozonForecastSeries(ctx, base, hc, last, now, id, today)
	if err != nil {
		return forecastView{}, err
	}
	return forecastOf(series, files)
}

// ozonForecastSeries reads the two niche files and own sales from cabinet id.
func (f *folderSettings) ozonForecastSeries(ctx context.Context, base string, hc *http.Client, last, now, id string, today time.Time) ([3]domain.DailySales, []forecastFile, error) {
	var series [3]domain.DailySales
	var files []forecastFile
	for i, x := range []struct{ path, what string }{{last, "ниша, прошлый год"}, {now, "ниша, сейчас"}} {
		s, info, err := readSales(x.path, x.what)
		if err != nil {
			return series, nil, err
		}
		series[i], files = s, append(files, info)
	}
	cl, c, err := f.ozonCabinet(id, base, hc)
	if err != nil {
		return series, nil, err
	}
	from, to := ozonWindow(today)
	posts, err := cl.Postings(ctx, from, to)
	if err != nil {
		return series, nil, ozonErr(c.Name, err)
	}
	if series[2], _, err = markets.OzonDaily(posts, from, to, today.Location()); err != nil {
		return series, nil, err
	}
	files = append(files, forecastFile{File: "Ozon · " + c.Name, Format: "ozon", Days: len(series[2].Days()),
		From: from.Format("02.01.2006"), To: to.Format("02.01.2006")})
	return series, files, nil
}

// forecast is not written yet.
func (f *folderSettings) forecast(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time) (forecastView, error) {
	if id, ok := strings.CutPrefix(own, "ozon:"); ok {
		return f.forecastOzon(ctx, base, hc, last, now, id, today)
	}
	return forecastFiles(last, now, own)
}
