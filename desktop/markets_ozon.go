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

// MarketsOzon reads the shop from an Ozon cabinet for the last 90 days.
func (a *App) MarketsOzon(id string) (shopView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	return a.settings.ozonShop(ctx, ozon.DefaultURL, nil, id, time.Now())
}

// ozonShop reads cabinet id through base: отправления за 90 дней до
// вчерашнего дня включительно (сегодняшние еще идут) и остатки сейчас.
func (f *folderSettings) ozonShop(ctx context.Context, base string, hc *http.Client, id string, now time.Time) (shopView, error) {
	c, ok := f.snapshot().cfg.Connection(id)
	if !ok || c.Kind != desktopconfig.ConnOzon {
		return shopView{}, errors.New("кабинета Ozon нет в «Подключениях» — добавьте его")
	}
	if !c.Enabled {
		return shopView{}, fmt.Errorf("подключение «%s» выключено", c.Name)
	}
	key, err := f.tokens.Get(connKeyAccount(id))
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return shopView{}, fmt.Errorf("ключа кабинета «%s» нет в Связке ключей — добавьте кабинет заново", c.Name)
		}
		return shopView{}, keychainNote2(err)
	}
	cl := ozon.Client{BaseURL: base, ClientID: c.Account, Key: key, HTTP: hc}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	to := day.AddDate(0, 0, -1)
	from := to.AddDate(0, 0, -(ozonDays - 1))
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

// forecastOzon is not written yet.
func (f *folderSettings) forecastOzon(ctx context.Context, base string, hc *http.Client, last, now, id string, today time.Time) (forecastView, error) {
	return forecastView{}, nil
}
