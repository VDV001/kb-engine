package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Кабинет Ozon — подключение окна (#580): Client-Id в config.json, ключ в
// Связке ключей. «Магазин» читает кабинет по API только на чтение и дает
// советы; ключ уходит только на api-seller.ozon.ru.

// ozonDays — за сколько дней «Магазин» берет отправления: столько же отдает
// аналитика Ozon.
const ozonDays = 90

// ozonTimeout — на чтение кабинета: 3 700 отправлений — около 40 страниц,
// начисления — запрос на день, 90 дней — около 11 секунд (замер 13.11).
const ozonTimeout = 5 * time.Minute

// AddOzon saves an Ozon cabinet as a connection.
func (f *folderSettings) AddOzon(name, clientID, key string, share bool) (connRow, error) {
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
	conn := desktopconfig.Connection{ID: "ozon-" + randomString()[:8], Kind: desktopconfig.ConnOzon, Name: name, Account: clientID, Enabled: true, Share: share}
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
		UsedBy: []string{"Маркеты → Магазин", "Маркеты → Прогноз"}, Section: "conn", Own: true, Enabled: c.Enabled, Share: c.Share}
	if c.Share {
		r.UsedBy = append(r.UsedBy, "чат: цифры магазина уходят провайдеру модели")
	}
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

// shopStage — пройденный этап загрузки кабинета для полосы окна; load —
// номер загрузки от окна: событие запоздавшей прежней загрузки окно отбросит.
type shopStage struct {
	Load  int    `json:"load"`
	Stage string `json:"stage"`
}

// MarketsOzon reads the shop from an Ozon cabinet for the last 90 days; этапы
// уходят окну событием «markets:stage» с номером load.
func (a *App) MarketsOzon(id string, load int) (shopView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, ozonTimeout)
	defer cancel()
	stage := func(s string) { runtime.EventsEmit(a.ctx, "markets:stage", shopStage{load, s}) }
	return a.settings.ozonShop(ctx, ozon.DefaultURL, nil, id, time.Now(), stage)
}

// ozonCabinet returns the client of cabinet id with its key from the keychain.
func (f *folderSettings) ozonCabinet(id, base string, hc *http.Client) (ozon.Client, desktopconfig.Connection, error) {
	c, err := f.ozonCabinetConn(id)
	if err != nil {
		return ozon.Client{}, c, err
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
// stage узнает о каждом прочитанном списке; nil — не сообщать.
func (f *folderSettings) ozonShop(ctx context.Context, base string, hc *http.Client, id string, now time.Time, stage func(string)) (shopView, error) {
	cl, c, err := f.ozonCabinet(id, base, hc)
	if err != nil {
		return shopView{}, err
	}
	from, to := ozonWindow(now)
	if stage == nil {
		stage = func(string) {}
	}
	fin := startFinance(ctx, cl, from, to, stage)
	posts, err := f.ozonPosts.get(ctx, cl, id, from, to, true) // «Загрузить» — всегда свежие
	if err != nil {
		return shopView{}, ozonErr(c.Name, err)
	}
	stage("posts")
	stock, err := cl.Stocks(ctx)
	if err != nil {
		return shopView{}, ozonErr(c.Name, err)
	}
	stage("stock")
	l := shopLoad{name: c.Name, from: from, to: to, now: now, posts: posts, stock: stock, fin: fin()}
	f.shopLoads.put(id, l)
	return f.shopFor(id, l, from, to)
}

// shopLoad — кабинет, прочитанный за 90 дней: «Магазин» за выбранные дни
// режется из него без новых запросов к Ozon.
type shopLoad struct {
	name          string
	from, to, now time.Time
	posts         []markets.OzonPosting
	stock         []markets.OzonStock
	fin           finance
}

// shopLoadCache keeps the last load of each cabinet.
// ponytail: память процесса без срока; потолок — после перезапуска окна период
// недоступен до «Загрузить из Ozon»; путь — история кабинета на Маке (#580, 3b).
type shopLoadCache struct {
	mu sync.Mutex
	m  map[string]shopLoad
}

func (c *shopLoadCache) put(id string, l shopLoad) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]shopLoad{}
	}
	c.m[id] = l
}

func (c *shopLoadCache) get(id string) (shopLoad, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.m[id]
	return l, ok
}

// MarketsOzonPeriod shows the loaded cabinet for days from..to (ГГГГ-ММ-ДД).
func (a *App) MarketsOzonPeriod(id, from, to string) (shopView, error) {
	return a.settings.ozonShopPeriod(id, from, to)
}

// ozonShopPeriod cuts the last load of cabinet id to days from..to.
func (f *folderSettings) ozonShopPeriod(id, from, to string) (shopView, error) {
	l, ok := f.shopLoads.get(id)
	if !ok {
		return shopView{}, errors.New("сначала загрузите кабинет: «Загрузить из Ozon»")
	}
	day := func(s string) (time.Time, error) { return time.ParseInLocation("2006-01-02", s, l.from.Location()) }
	d1, err1 := day(from)
	d2, err2 := day(to)
	switch {
	case err1 != nil || err2 != nil:
		return shopView{}, fmt.Errorf("даты периода не прочитаны: %q – %q", from, to)
	case d2.Before(d1):
		return shopView{}, errors.New("конец периода раньше начала")
	case d1.Before(l.from) || d2.After(l.to):
		return shopView{}, fmt.Errorf("загружены дни %s – %s: выберите период внутри них", l.from.Format("02.01.2006"), l.to.Format("02.01.2006"))
	}
	return f.shopFor(id, l, d1, d2)
}

// shopFor builds the shop of load l for days from..to; все дни окна — как загружено.
func (f *folderSettings) shopFor(id string, l shopLoad, from, to time.Time) (shopView, error) {
	whole := from.Equal(l.from) && to.Equal(l.to)
	posts, acc, rets := l.posts, l.fin.acc, l.fin.rets
	if !whole { // отправления без даты входят только во все дни
		posts, acc, rets = markets.PostingsIn(posts, from, to), markets.AccrualsIn(acc, from, to), markets.ReturnsIn(rets, from, to)
	}
	rep, err := markets.OzonShop(posts, l.stock, from, to)
	if err != nil {
		return shopView{}, err
	}
	v := shopViewOf(rep)
	v.File, v.Path, v.Source = "Ozon · "+l.name, id, "ozon"
	v.From, v.To, v.WindowFrom, v.WindowTo = from.Format(time.DateOnly), to.Format(time.DateOnly), l.from.Format(time.DateOnly), l.to.Format(time.DateOnly)
	fn := l.fin
	switch {
	case fn.accErr != nil:
		why := economyErr(l.name, fn.accErr) // товары остаются: отказ — только в финансах
		v.Economy = &economyView{Err: why}
		v.Losses = &lossesView{Err: "без начислений не проверить, начислена ли продажа: " + why}
	case fn.retErr != nil:
		v.Economy = economyOf(markets.OzonEconomy(acc))
		v.Losses = &lossesView{Err: returnsErr(l.name, fn.retErr)}
	default:
		v.Economy = economyOf(markets.OzonEconomy(acc))
		// Продажу ищем во всех начислениях: она приходит позже доставки.
		v.Losses = lossesOf(markets.OzonLosses(posts, fn.acc, rets, l.now))
	}
	// Себестоимость видна и без начислений; испорченный costs.json — причина
	// словами, а не пустой «Магазин» (ревью #598).
	costs, err := f.costsOf(id)
	if err != nil {
		v.CostsErr = err.Error()
	}
	rowsWithMoney(&v, acc, costs)
	return v, nil
}

// finance — начисления и возвраты кабинета, у каждого своя ошибка: отказ в
// одном не прячет другое.
type finance struct {
	acc            []markets.OzonAccrual
	rets           []markets.OzonReturn
	accErr, retErr error
}

// startFinance reads accruals and returns in parallel, независимо от
// отправлений и друг от друга (ревью #594, #595); wait — дождаться обоих.
// Одна копия для окна и чата (ревью #597). done узнает о каждом прочитанном
// без ошибки списке («accruals», «returns»): отказу окно ✓ не рисует; nil — не сообщать.
func startFinance(ctx context.Context, cl ozon.Client, from, to time.Time, done func(string)) (wait func() finance) {
	if done == nil {
		done = func(string) {}
	}
	type accrued struct {
		acc []markets.OzonAccrual
		err error
	}
	type returned struct {
		rets []markets.OzonReturn
		err  error
	}
	accCh, retCh := make(chan accrued, 1), make(chan returned, 1)
	go func() {
		acc, err := cl.Accruals(ctx, from, to)
		if err == nil {
			done("accruals")
		}
		accCh <- accrued{acc, err}
	}()
	go func() {
		rets, err := cl.Returns(ctx, from, to)
		if err == nil {
			done("returns")
		}
		retCh <- returned{rets, err}
	}()
	return func() finance {
		a, r := <-accCh, <-retCh
		return finance{acc: a.acc, accErr: a.err, rets: r.rets, retErr: r.err}
	}
}

// returnsErr names a refusal of the returns list (ревью #595: не словами начислений).
func returnsErr(name string, err error) string {
	switch {
	case errors.Is(err, ozon.ErrUnauthorized):
		return fmt.Sprintf("кабинет «%s»: Ozon не дает этому ключу список возвратов — нужен ключ с доступом к возвратам (Ozon Seller → Настройки → Seller API)", name)
	case errors.Is(err, ozon.ErrTooBig):
		return fmt.Sprintf("кабинет «%s»: возвратов за 90 дней слишком много страниц — такой кабинет окно пока не читает", name)
	}
	return "возвраты не загрузились: " + ozonErr(name, err).Error()
}

// lossesOf turns the losses into the window's rows.
func lossesOf(ls []markets.ShopLoss) *lossesView {
	v := &lossesView{Rows: []lossRow{}}
	for _, l := range ls {
		r := lossRow{Kind: string(l.Kind), Posting: l.Posting, SKU: l.SKU, Name: l.Name, Price: l.Price}
		if !l.Since.IsZero() {
			r.Since = l.Since.Format("02.01.2006") // без даты статуса — без дня, а не «01.01.0001» (ревью #595)
		}
		v.Rows = append(v.Rows, r)
	}
	return v
}

// economyErr names a refusal of the accruals: тот же ключ только что прочитал
// товары, поэтому 401/403 здесь — нет роли, а не неверный ключ (ревью #594).
func economyErr(name string, err error) string {
	switch {
	case errors.Is(err, ozon.ErrUnauthorized):
		return fmt.Sprintf("кабинет «%s»: Ozon не дает этому ключу начисления — нужен ключ с ролью «Финансы» (Ozon Seller → Настройки → Seller API)", name)
	case errors.Is(err, ozon.ErrTooBig):
		return fmt.Sprintf("кабинет «%s»: начислений за один день слишком много страниц — такой кабинет окно пока не читает", name)
	}
	return ozonErr(name, err).Error()
}

// economyOf turns the economy into rubles for the window.
func economyOf(e markets.ShopEconomy) *economyView {
	rub := func(m domain.Money) float64 { return float64(m.Kopecks()) / 100 }
	return &economyView{Sales: rub(e.Sales), Commission: rub(e.Commission), Delivery: rub(e.Delivery), Promotion: rub(e.Promotion),
		Partners: rub(e.Partners), Other: rub(e.Other), Total: rub(e.Total)}
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

// ozonPostTTL — сколько отправления кабинета живут в памяти окна: формула,
// модель и чат за один разговор не читают кабинет заново (ревью #592).
// ponytail: память процесса, без сброса по событию; потолок — заказ за последние
// 10 минут не виден в прогнозе; путь — история кабинета на Маке (#580, 3b).
const ozonPostTTL = 10 * time.Minute

// ozonPostCache keeps the last postings of each cabinet.
type ozonPostCache struct {
	mu sync.Mutex
	m  map[string]ozonPostEntry
}

type ozonPostEntry struct {
	from, to time.Time
	at       time.Time
	posts    []markets.OzonPosting
}

// get returns the postings of cabinet id for [from, to]; fresh — мимо памяти.
func (c *ozonPostCache) get(ctx context.Context, cl ozon.Client, id string, from, to time.Time, fresh bool) ([]markets.OzonPosting, error) {
	c.mu.Lock()
	e, ok := c.m[id]
	c.mu.Unlock()
	if ok && !fresh && e.from.Equal(from) && e.to.Equal(to) && time.Since(e.at) < ozonPostTTL {
		return e.posts, nil
	}
	posts, err := cl.Postings(ctx, from, to)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]ozonPostEntry{}
	}
	c.m[id] = ozonPostEntry{from: from, to: to, at: time.Now(), posts: posts}
	c.mu.Unlock()
	return posts, nil
}

// ownSeries reads own orders: файл или «ozon:<id>» — заказы кабинета по дням
// за 90 дней до вчера, включая отмены.
func (f *folderSettings) ownSeries(ctx context.Context, base string, hc *http.Client, own string, today time.Time) (domain.DailySales, forecastFile, error) {
	id, ok := strings.CutPrefix(own, "ozon:")
	if !ok {
		return readSales(own, "свои продажи")
	}
	cl, c, err := f.ozonCabinet(id, base, hc)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, err
	}
	from, to := ozonWindow(today)
	posts, err := f.ozonPosts.get(ctx, cl, id, from, to, false)
	if err != nil {
		return domain.DailySales{}, forecastFile{}, ozonErr(c.Name, err)
	}
	s, undated, err := markets.OzonDaily(posts, from, to, today.Location())
	if err != nil {
		return domain.DailySales{}, forecastFile{}, err
	}
	return s, forecastFile{File: "Ozon · " + c.Name, Format: "ozon", Days: len(s.Days()), Undated: undated,
		From: from.Format("02.01.2006"), To: to.Format("02.01.2006")}, nil
}

// forecastInputs reads the three series: две ниши файлами и свои — файлом или из кабинета.
func (f *folderSettings) forecastInputs(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time) ([3]domain.DailySales, []forecastFile, error) {
	var series [3]domain.DailySales
	niche, files, err := readNiche(last, now)
	if err != nil {
		return series, nil, err
	}
	series[0], series[1] = niche[0], niche[1]
	s, info, err := f.ownSeries(ctx, base, hc, own, today)
	if err != nil {
		return series, nil, err
	}
	series[2] = s
	return series, append(files, info), nil
}

// forecast is the formula's forecast from any source of own orders.
func (f *folderSettings) forecast(ctx context.Context, base string, hc *http.Client, last, now, own string, today time.Time) (forecastView, error) {
	series, files, err := f.forecastInputs(ctx, base, hc, last, now, own, today)
	if err != nil {
		return forecastView{}, err
	}
	return forecastOf(series, files)
}

// SetOzonShare allows or forbids giving cabinet id's numbers to the chat model.
func (f *folderSettings) SetOzonShare(id string, on bool) error {
	found := false
	err := f.updateConfig(func(cfg *desktopconfig.Config) {
		for i, c := range cfg.Connections {
			if c.ID == id && c.Kind == desktopconfig.ConnOzon {
				cfg.Connections[i].Share, found = on, true
			}
		}
	})
	if err == nil && !found {
		return errors.New("кабинета Ozon нет в «Подключениях»")
	}
	return err
}
