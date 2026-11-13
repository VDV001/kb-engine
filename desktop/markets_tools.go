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

// shopExtras gives the chat the shop's tools of the shared cabinets; nil — нет.
// Кабинет читается один раз за ход: экземпляр живет один ход.
func shopExtras(s *folderSettings, base string, hc *http.Client, now func() time.Time) []chat.Extra {
	if len(sharedOzon(s.snapshot().cfg)) == 0 {
		return nil
	}
	var mu sync.Mutex
	turn := map[string]markets.ShopData{}
	load := func(ctx context.Context, cabinet string) (markets.ShopData, error) {
		c, err := pickCabinet(sharedOzon(s.snapshot().cfg), cabinet)
		if err != nil {
			return markets.ShopData{}, err
		}
		mu.Lock()
		d, ok := turn[c.ID]
		mu.Unlock()
		if ok {
			return d, nil
		}
		if d, err = readShop(ctx, s, base, hc, c, now()); err != nil {
			return markets.ShopData{}, err
		}
		mu.Lock()
		turn[c.ID] = d
		mu.Unlock()
		return d, nil
	}
	full := map[string]markets.ShopData{} // с начислениями и возвратами: только по просьбе модели, раз за ход
	money := func(ctx context.Context, cabinet string) (markets.ShopData, error) {
		d, err := load(ctx, cabinet)
		if err != nil {
			return markets.ShopData{}, err
		}
		c, _ := pickCabinet(sharedOzon(s.snapshot().cfg), cabinet) // load уже нашел его
		mu.Lock()
		f, ok := full[c.ID]
		mu.Unlock()
		if ok {
			return f, nil
		}
		if d, err = readMoney(ctx, s, base, hc, c, d, now()); err != nil {
			return markets.ShopData{}, err
		}
		mu.Lock()
		full[c.ID] = d
		mu.Unlock()
		return d, nil
	}
	return []chat.Extra{shopExtra{markets.ShopTools{Load: load, Money: money}}}
}

// readMoney adds the cabinet's accruals and returns to d — те же 90 дней.
func readMoney(ctx context.Context, s *folderSettings, base string, hc *http.Client, c desktopconfig.Connection, d markets.ShopData, now time.Time) (markets.ShopData, error) {
	cl, _, err := s.ozonCabinet(c.ID, base, hc)
	if err != nil {
		return markets.ShopData{}, err
	}
	if d.Accruals, err = cl.Accruals(ctx, d.From, d.To); err != nil {
		return markets.ShopData{}, errors.New(economyErr(c.Name, err))
	}
	if d.Returns, err = cl.Returns(ctx, d.From, d.To); err != nil {
		return markets.ShopData{}, errors.New(returnsErr(c.Name, err))
	}
	d.Now = now
	return d, nil
}

// readShop reads cabinet c: отправления из 10-минутной памяти, остатки свежие.
func readShop(ctx context.Context, s *folderSettings, base string, hc *http.Client, c desktopconfig.Connection, t time.Time) (markets.ShopData, error) {
	cl, _, err := s.ozonCabinet(c.ID, base, hc)
	if err != nil {
		return markets.ShopData{}, err
	}
	from, to := ozonWindow(t)
	posts, err := s.ozonPosts.get(ctx, cl, c.ID, from, to, false)
	if err != nil {
		return markets.ShopData{}, chatOzonErr(c.Name, err)
	}
	stock, err := cl.Stocks(ctx)
	if err != nil {
		return markets.ShopData{}, chatOzonErr(c.Name, err)
	}
	return markets.ShopData{Name: c.Name, Posts: posts, Stock: stock, From: from, To: to, Loc: t.Location()}, nil
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
