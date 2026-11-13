package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Инструменты магазина в чате окна (#580): модель в режиме «модель ищет» сама
// спрашивает сводку, топ, «что довезти», карточку товара, деньги и пропажи — только у
// кабинетов, которым человек разрешил давать цифры модели (Share, ревью #593):
// названия товаров и продажи уходят провайдеру модели.

// shopExtra adapts markets.ShopTools to the chat.
type shopExtra struct{ tools markets.ShopTools }

func (shopExtra) Prompt() string { return markets.ShopToolsPrompt }

func (x shopExtra) Tools() []chat.Tool {
	var out []chat.Tool
	for _, s := range x.tools.Specs() {
		out = append(out, chat.Tool{Name: s.Name, Description: s.Description, Parameters: []byte(s.Params)})
	}
	return out
}

// Exec runs a shop tool; ошибка кабинета уходит модели словами, ход продолжается.
func (x shopExtra) Exec(ctx context.Context, name string, args []byte) (string, bool) {
	if !strings.HasPrefix(name, "shop_") {
		return "", false
	}
	out, err := x.tools.Run(ctx, name, args)
	if err != nil {
		return "ошибка: " + err.Error(), true
	}
	return out, true
}

// errNoCabinet — подходящего кабинета нет.
var errNoCabinet = errors.New("нет кабинета")

// shopTurn — кабинет в одном ходе чата: клиент, данные и деньги, если их просили.
type shopTurn struct {
	c        desktopconfig.Connection
	cl       ozon.Client
	d        markets.ShopData
	money    bool  // начисления и возвраты уже читались в этом ходе
	moneyErr error // их неудача — тоже до конца хода (ревью #597)
}

// shopExtras gives the chat the shop's tools of the shared cabinets; nil — нет.
// Кабинет читается один раз за ход: экземпляр живет один ход.
func shopExtras(s *folderSettings, base string, hc *http.Client, now func() time.Time) []chat.Extra {
	if len(sharedOzon(s.snapshot().cfg)) == 0 {
		return nil
	}
	var mu sync.Mutex
	turn := map[string]*shopTurn{}
	get := func(ctx context.Context, cabinet string) (*shopTurn, error) {
		c, err := pickCabinet(sharedOzon(s.snapshot().cfg), cabinet)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		t, ok := turn[c.ID]
		mu.Unlock()
		if ok {
			return t, nil
		}
		t = &shopTurn{c: c}
		if t.cl, t.d, err = readShop(ctx, s, base, hc, c, now()); err != nil {
			return nil, err
		}
		mu.Lock()
		turn[c.ID] = t
		mu.Unlock()
		return t, nil
	}
	load := func(ctx context.Context, cabinet string) (markets.ShopData, error) {
		t, err := get(ctx, cabinet)
		if err != nil {
			return markets.ShopData{}, err
		}
		return t.d, nil
	}
	money := func(ctx context.Context, cabinet string) (markets.ShopData, error) {
		t, err := get(ctx, cabinet) // тот же кабинет и клиент, что у сводки (ревью #597)
		if err != nil {
			return markets.ShopData{}, err
		}
		mu.Lock()
		done := t.money
		mu.Unlock()
		if !done {
			d, err := withMoney(ctx, t, now())
			mu.Lock()
			t.d, t.moneyErr, t.money = d, err, true
			mu.Unlock()
		}
		mu.Lock()
		defer mu.Unlock()
		return t.d, t.moneyErr
	}
	return []chat.Extra{shopExtra{markets.ShopTools{Load: load, Money: money}}}
}

// withMoney adds accruals and returns to the turn's data — те же 90 дней,
// параллельно, как в окне. Без возвратов деньги отвечают, пропажи называют причину.
func withMoney(ctx context.Context, t *shopTurn, now time.Time) (markets.ShopData, error) {
	d := t.d
	fn := startFinance(ctx, t.cl, d.From, d.To)()
	if fn.accErr != nil {
		return t.d, chatMoneyErr(t.c.Name, fn.accErr, economyErr)
	}
	d.Accruals, d.Now = fn.acc, now
	if fn.retErr != nil {
		d.ReturnsErr = chatMoneyErr(t.c.Name, fn.retErr, returnsErr)
	} else {
		d.Returns = fn.rets
	}
	return d, nil
}

// chatMoneyErr — отказ в начислениях или возвратах словами для модели: таймаут —
// словами чата, без кнопки «Загрузить» (ревью #597).
func chatMoneyErr(name string, err error, words func(string, error) string) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return chatOzonErr(name, err)
	}
	return errors.New(words(name, err))
}

// readShop reads cabinet c: отправления из 10-минутной памяти, остатки свежие.
func readShop(ctx context.Context, s *folderSettings, base string, hc *http.Client, c desktopconfig.Connection, t time.Time) (ozon.Client, markets.ShopData, error) {
	cl, _, err := s.ozonCabinet(c.ID, base, hc)
	if err != nil {
		return ozon.Client{}, markets.ShopData{}, err
	}
	from, to := ozonWindow(t)
	posts, err := s.ozonPosts.get(ctx, cl, c.ID, from, to, false)
	if err != nil {
		return ozon.Client{}, markets.ShopData{}, chatOzonErr(c.Name, err)
	}
	stock, err := cl.Stocks(ctx)
	if err != nil {
		return ozon.Client{}, markets.ShopData{}, chatOzonErr(c.Name, err)
	}
	return cl, markets.ShopData{Name: c.Name, Posts: posts, Stock: stock, From: from, To: to, Loc: t.Location()}, nil
}

// chatOzonErr — отказ Ozon словами для модели: в чате нет кнопки «Загрузить».
func chatOzonErr(name string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("кабинет «%s»: Ozon не ответил вовремя — скажи человеку, что данные кабинета сейчас недоступны и их можно открыть в «Маркеты → Магазин»", name)
	}
	return ozonErr(name, err)
}

func sharedOzon(cfg desktopconfig.Config) []desktopconfig.Connection {
	var out []desktopconfig.Connection
	for _, c := range cfg.Connections {
		if c.Kind == desktopconfig.ConnOzon && c.Enabled && c.Share {
			out = append(out, c)
		}
	}
	return out
}

// pickCabinet finds the cabinet by the person's words: точное имя, потом
// Client-Id, потом единственное совпадение части имени; "" — первый.
// Несколько совпадений — вопрос, а не догадка (ревью #593).
func pickCabinet(cs []desktopconfig.Connection, name string) (desktopconfig.Connection, error) {
	if len(cs) == 0 {
		return desktopconfig.Connection{}, fmt.Errorf("%w: подключенных кабинетов, разрешенных для чата, не осталось", errNoCabinet)
	}
	q, raw := domain.FoldName(name), strings.TrimSpace(name)
	if q == "" {
		return cs[0], nil
	}
	var all, part []string
	var hit []desktopconfig.Connection
	for _, c := range cs {
		all = append(all, "«"+c.Name+"»")
		if domain.FoldName(c.Name) == q {
			return c, nil
		}
	}
	for _, c := range cs {
		if c.Account == raw {
			return c, nil
		}
	}
	for _, c := range cs {
		if strings.Contains(domain.FoldName(c.Name), q) {
			hit, part = append(hit, c), append(part, "«"+c.Name+"»")
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], nil
	case 0:
		return desktopconfig.Connection{}, fmt.Errorf("%w «%s»; есть %s", errNoCabinet, raw, strings.Join(all, ", "))
	}
	return desktopconfig.Connection{}, fmt.Errorf("подходят несколько кабинетов: %s — спроси человека, какой", strings.Join(part, ", "))
}
