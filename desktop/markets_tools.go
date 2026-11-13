package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Инструменты магазина в чате окна (#580): модель в режиме «модель ищет» сама
// спрашивает сводку, топ, «что довезти» и карточку товара по кабинетам Ozon.
// Цифры уходят провайдеру модели вместе с ответом инструмента — как и
// найденные документы; справка говорит это прямо.

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

// errNoCabinet — кабинета с таким именем нет.
var errNoCabinet = errors.New("нет кабинета")

// shopExtras gives the chat the shop's tools when an Ozon cabinet is on; nil — нет.
func shopExtras(s *folderSettings, base string, hc *http.Client, now func() time.Time) []chat.Extra {
	if len(enabledOzon(s.snapshot().cfg)) == 0 {
		return nil
	}
	load := func(ctx context.Context, cabinet string) (markets.ShopData, error) {
		c, err := pickCabinet(enabledOzon(s.snapshot().cfg), cabinet)
		if err != nil {
			return markets.ShopData{}, err
		}
		cl, _, err := s.ozonCabinet(c.ID, base, hc)
		if err != nil {
			return markets.ShopData{}, err
		}
		t := now()
		from, to := ozonWindow(t)
		posts, err := s.ozonPosts.get(ctx, cl, c.ID, from, to, false)
		if err != nil {
			return markets.ShopData{}, ozonErr(c.Name, err)
		}
		stock, err := cl.Stocks(ctx)
		if err != nil {
			return markets.ShopData{}, ozonErr(c.Name, err)
		}
		return markets.ShopData{Name: c.Name, Posts: posts, Stock: stock, From: from, To: to, Loc: t.Location()}, nil
	}
	return []chat.Extra{shopExtra{markets.ShopTools{Load: load}}}
}

func enabledOzon(cfg desktopconfig.Config) []desktopconfig.Connection {
	var out []desktopconfig.Connection
	for _, c := range cfg.Connections {
		if c.Kind == desktopconfig.ConnOzon && c.Enabled {
			out = append(out, c)
		}
	}
	return out
}

// pickCabinet finds the cabinet by the person's words; "" — первый.
func pickCabinet(cs []desktopconfig.Connection, name string) (desktopconfig.Connection, error) {
	q := domain.FoldName(name)
	if q == "" {
		return cs[0], nil
	}
	var names []string
	for _, c := range cs {
		if strings.Contains(domain.FoldName(c.Name), q) || c.Account == strings.TrimSpace(name) {
			return c, nil
		}
		names = append(names, "«"+c.Name+"»")
	}
	return desktopconfig.Connection{}, errors.Join(errNoCabinet, errors.New("«"+strings.TrimSpace(name)+"»; есть "+strings.Join(names, ", ")))
}
