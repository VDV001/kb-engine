package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// hashtagWhy — коды markets.HashtagWhy для окна; слова причин — в app.js.
var hashtagWhy = map[markets.HashtagWhy]string{markets.TagOK: "ok", markets.TagOwn: "own", markets.TagFormat: "format", markets.TagLong: "long",
	markets.TagDuplicate: "dup", markets.TagBrand: "brand", markets.TagOffTopic: "gender", markets.TagNoDemand: "nodemand", markets.TagNew: "new"}

// hashtagRow — хештег и его спрос: запросов в месяц со всеми его словами.
type hashtagRow struct {
	Tag      string `json:"tag"`
	Demand   int    `json:"demand"`
	Why      string `json:"why"`
	Query    string `json:"query"`
	Position int    `json:"position"`
}

// hashtagsView — хештеги карточки (#610): каждый со спросом, что убрать и что добавить.
type hashtagsView struct {
	SKU     string       `json:"sku"`
	Niche   string       `json:"niche"`
	Main    string       `json:"main"`
	Total   int          `json:"total"`
	Max     int          `json:"max"`
	TooMany bool         `json:"tooMany"`
	Tags    []hashtagRow `json:"tags"`
	Remove  []hashtagRow `json:"remove"`
	Add     []hashtagRow `json:"add"`
	Note    string       `json:"note"`
}

// MarketsHashtags checks the hashtags of card sku in Ozon cabinet cab.
// fresh — «Проверить еще раз»: перечитать нишу из MPStats.
func (a *App) MarketsHashtags(cab, sku string, fresh bool) (hashtagsView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	return a.settings.hashtagsCheck(ctx, nil, ozon.DefaultURL, cab, sku, fresh)
}

// hashtagsCheck reads the card's hashtags from the cabinet and its niche from
// MPStats (из памяти, если «Название» уже читало) and judges them.
func (f *folderSettings) hashtagsCheck(ctx context.Context, hc *http.Client, base, cab, sku string, fresh bool) (hashtagsView, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return hashtagsView{}, errors.New("хештеги проверяются у товара Ozon с числовым SKU")
	}
	oc, _, err := f.ozonCabinet(cab, base, hc)
	if err != nil {
		return hashtagsView{}, err
	}
	card, err := oc.Card(ctx, strconv.FormatInt(id, 10))
	if errors.Is(err, ozon.ErrNoCard) {
		return hashtagsView{}, errors.New("товара с этим SKU нет в кабинете — загрузите «Магазин» заново")
	}
	if err != nil {
		return hashtagsView{}, ozonErr("Ozon", err)
	}
	cl, connID, err := f.mpstatsFirst(hc)
	if err != nil {
		return hashtagsView{}, err
	}
	n, err := f.readCardNiche(ctx, hc, connID, cl.Token, id, fresh)
	if err != nil {
		return hashtagsView{}, err
	}
	r := markets.CheckHashtags(markets.HashtagInput{Title: card.Name, Hashtags: card.Hashtags, Brands: []string{n.card.Brand, n.card.Seller},
		Queries: n.qs, Forms: n.ws, Own: n.own, NicheBrands: n.brands})
	if r.Main.Text == "" {
		return hashtagsView{}, errors.New("у ниши «" + n.card.Niche + "» в MPStats нет запросов за 30 дней — проверить хештеги не по чему")
	}
	return hashtagsView{SKU: strconv.FormatInt(id, 10), Niche: n.card.Niche, Main: r.Main.Text, Total: r.Total, Max: r.Ozon30, TooMany: r.TooMany,
		Tags: hashtagRows(r.Tags), Remove: hashtagRows(r.Remove), Add: hashtagRows(r.Add), Note: titleNote(n.ownErr, n.brandErr)}, nil
}

func hashtagRows(rs []markets.HashtagRow) []hashtagRow {
	out := make([]hashtagRow, 0, len(rs)) // не nil: в JSON — [], а не null
	for _, r := range rs {
		out = append(out, hashtagRow{Tag: r.Tag, Demand: r.Demand, Why: hashtagWhy[r.Why], Query: r.Query, Position: r.Position})
	}
	return out
}
