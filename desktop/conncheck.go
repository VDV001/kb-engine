package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/adapter/ozon"
)

// «Проверить» у каждого подключения (отзыв владельца 13.11): живой запрос
// туда, куда подключение ходит, и итог одной строкой для человека. Ключ
// MPStats — подключение, как кабинет Ozon: проверяется до сохранения.

// checkTimeout — на одну проверку: остатки Ozon — пара страниц.
const checkTimeout = 30 * time.Second

// checkEndpoints — адреса API; в окне — настоящие, в тестах — поддельные.
type checkEndpoints struct {
	Ozon, MPStats string
	HTTP          *http.Client
}

func liveEndpoints() checkEndpoints {
	return checkEndpoints{Ozon: ozon.DefaultURL, MPStats: mpstats.DefaultURL}
}

// connCheck — итог «Проверить».
type connCheck struct {
	OK   bool   `json:"ok"`
	Note string `json:"note"`
}

func checked(note string) connCheck { return connCheck{OK: true, Note: note} }
func failed(note string) connCheck  { return connCheck{Note: note} }

// AddMPStatsConnection saves an MPStats key after a live check.
func (a *App) AddMPStatsConnection(name, key string) (connRow, error) {
	ctx, cancel := context.WithTimeout(a.ctx, checkTimeout)
	defer cancel()
	return a.settings.addMPStats(ctx, liveEndpoints(), name, key)
}

// CheckConnection is the «Проверить» button of a connection row.
func (a *App) CheckConnection(id string) connCheck {
	ctx, cancel := context.WithTimeout(a.ctx, checkTimeout)
	defer cancel()
	if c, ok := a.settings.snapshot().cfg.Connection(id); ok && c.Kind == desktopconfig.ConnGoogle {
		if !c.Enabled {
			return failed("подключение выключено — включите его, чтобы проверить")
		}
		return a.google.check(ctx, id)
	}
	return a.settings.checkConn(ctx, id, liveEndpoints())
}

func (f *folderSettings) addMPStats(ctx context.Context, ep checkEndpoints, name, key string) (connRow, error) {
	name, key = strings.TrimSpace(name), strings.TrimSpace(key)
	if key == "" {
		return connRow{}, errors.New("нужен ключ API: MPStats → Профиль → API")
	}
	if name == "" {
		name = "MPStats"
	}
	if _, err := (mpstats.Client{BaseURL: ep.MPStats, Token: key, HTTP: ep.HTTP}).Limits(ctx); err != nil {
		return connRow{}, errors.New(mpstatsMessage(err))
	}
	conn := desktopconfig.Connection{ID: "mpstats-" + randomString()[:8], Kind: desktopconfig.ConnMPStats, Name: name, Enabled: true}
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
	return f.mpstatsConn(conn), nil
}

func (f *folderSettings) mpstatsConn(c desktopconfig.Connection) connRow {
	r := connRow{ID: c.ID, Kind: "mpstats", Name: c.Name, Detail: "аналитика ниш Ozon по ключу API, только чтение",
		UsedBy: []string{"Маркеты → Прогноз: ниша по дням"}, Section: "conn", Own: true, Enabled: c.Enabled}
	_, err := f.tokens.Get(connKeyAccount(c.ID))
	switch {
	case !c.Enabled:
		r.Status = "выключено"
	case err == nil:
		r.Status, r.OK = "ключ в Связке ключей", true
	case !errors.Is(err, keychain.ErrNotFound):
		r.Status = keychainNote
	default:
		r.Status = "нет ключа — добавьте заново"
	}
	return r
}

// mpstatsMessage says what went wrong in words a seller understands.
func mpstatsMessage(err error) string {
	switch {
	case errors.Is(err, mpstats.ErrUnauthorized):
		return "ключ MPStats не принят: скопируйте его заново в MPStats → Профиль → API"
	case errors.Is(err, mpstats.ErrQuota):
		return "лимит запросов тарифа MPStats исчерпан — дождитесь нового периода тарифа"
	case errors.Is(err, mpstats.ErrTimeout):
		return "MPStats не ответил вовремя — он бывает занят, попробуйте через минуту"
	case errors.Is(err, mpstats.ErrDropped):
		return "MPStats оборвал соединение и после повторов — он перегружен, попробуйте через минуту"
	}
	return "MPStats не ответил: " + err.Error()
}

// checkConn checks connection id (all kinds but Google, у него свой вход).
func (f *folderSettings) checkConn(ctx context.Context, id string, ep checkEndpoints) connCheck {
	cfg := f.snapshot().cfg
	if id == chatKeyAccount {
		return f.checkModel(ctx, cfg.Chat.WithDefaults(), ep.HTTP)
	}
	if url := f.savedURL(id); url != "" {
		if _, err := f.CheckServer(id, url, ""); err != nil {
			return failed(err.Error())
		}
		return checked("сервер отвечает, токен принят")
	}
	c, ok := cfg.Connection(id)
	if !ok {
		return failed("подключение не найдено — обновите список")
	}
	if !c.Enabled {
		return failed("подключение выключено — включите его, чтобы проверить")
	}
	key, bad := f.connKey(c)
	if bad != "" {
		return failed(bad)
	}
	switch c.Kind {
	case desktopconfig.ConnOzon:
		return checkOzon(ctx, ozon.Client{BaseURL: ep.Ozon, ClientID: c.Account, Key: key, HTTP: ep.HTTP}, c.Name)
	case desktopconfig.ConnMPStats:
		l, err := (mpstats.Client{BaseURL: ep.MPStats, Token: key, HTTP: ep.HTTP}).Limits(ctx)
		if err != nil {
			return failed(mpstatsMessage(err))
		}
		return checked(fmt.Sprintf("ключ принят · запросов тарифа потрачено %d из %d", l.Used, l.Available))
	case desktopconfig.ConnSpeech:
		return checkOpenAI(ctx, c.URL, key, ep.HTTP, "сервер распознавания")
	}
	return failed("этот вид подключения проверить нельзя")
}

func checkOzon(ctx context.Context, cl ozon.Client, name string) connCheck {
	st, err := cl.Stocks(ctx)
	if err != nil {
		if errors.Is(err, ozon.ErrUnauthorized) {
			return failed("ключ кабинета «" + name + "» не принят: проверьте Client-Id и ключ в Ozon Seller → Настройки → Seller API")
		}
		return failed(ozonErr(name, err).Error())
	}
	return checked(fmt.Sprintf("ключ принят · товаров в кабинете %d", len(st)))
}

// checkOpenAI asks an OpenAI-compatible server for /models: зеленое — только
// 2xx; 401 и 403 — ключ, 429 — занят, 404 и перенаправление — не тот путь (ревью #605).
func checkOpenAI(ctx context.Context, base, key string, hc *http.Client, what string) connCheck {
	status, body, err := getWithKey(ctx, strings.TrimRight(base, "/")+"/models", key, hc)
	switch {
	case err != nil:
		return failed(what + " не отвечает: " + err.Error())
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return failed("ключ не принят: " + what + " ответил " + fmt.Sprint(status))
	case status == http.StatusTooManyRequests:
		return failed(what + " занят (429) — попробуйте через минуту")
	case status >= 500:
		return failed(fmt.Sprintf("%s отвечает ошибкой %d — попробуйте позже", what, status))
	case status < 200 || status > 299:
		return failed(fmt.Sprintf("%s ответил %d — проверьте путь: адрес обычно кончается на /v1", what, status))
	}
	var list struct{ Data []json.RawMessage }
	if json.Unmarshal(body, &list) == nil && len(list.Data) > 0 {
		return checked(fmt.Sprintf("%s отвечает · моделей %d", what, len(list.Data)))
	}
	return checked(what + " отвечает")
}

// checkModel checks the chat model: у Vercel AI Gateway — баланс по ключу
// (/credits без ключа не отвечает), у остальных — /models.
func (f *folderSettings) checkModel(ctx context.Context, c desktopconfig.Chat, hc *http.Client) connCheck {
	if c.Local() {
		return checkOpenAI(ctx, c.URL, "", hc, "модель на этом Маке")
	}
	key, err := f.tokens.Get(chatKeyAccount)
	if err != nil {
		return failed("ключа модели нет в Связке ключей — впишите его в «Чат и модель»")
	}
	if modelProvider(c) == "Vercel AI Gateway" {
		status, body, err := getWithKey(ctx, strings.TrimRight(c.URL, "/")+"/credits", key, hc)
		var cr struct{ Balance string }
		if err == nil && status == http.StatusOK && json.Unmarshal(body, &cr) == nil && cr.Balance != "" {
			return checked("ключ принят · баланс шлюза $" + cr.Balance)
		}
		if err == nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
			return failed("ключ шлюза не принят: впишите его заново в «Чат и модель»")
		}
	}
	return checkOpenAI(ctx, c.URL, key, hc, "сервер модели")
}

func getWithKey(ctx context.Context, u, key string, hc *http.Client) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: checkTimeout}
	if hc != nil {
		cp := *hc
		client = &cp
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse } // ключ — только на этот адрес
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, err
}

// check checks a Google connection: обновить вход и спросить, чей он.
func (g *googleDesk) check(ctx context.Context, id string) connCheck {
	c, at, err := g.token(ctx, id)
	if err != nil {
		return failed(err.Error())
	}
	who, err := c.Account(ctx, at)
	if err != nil {
		return failed(googleErr(err).Error())
	}
	return checked("вход действует · " + who)
}

// connKey reads the connection's key; bad — почему проверить нельзя.
// Распознаванию речи ключ не обязателен.
func (f *folderSettings) connKey(c desktopconfig.Connection) (key, bad string) {
	key, err := f.tokens.Get(connKeyAccount(c.ID))
	switch {
	case errors.Is(err, keychain.ErrNotFound) && c.Kind != desktopconfig.ConnSpeech:
		return "", "ключа нет в Связке ключей — удалите подключение и добавьте заново"
	case err != nil && !errors.Is(err, keychain.ErrNotFound):
		return "", keychainNote
	}
	return key, ""
}
