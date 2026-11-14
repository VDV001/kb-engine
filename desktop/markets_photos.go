package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

const (
	photoLeaders     = 10  // лидеров в сравнении
	photoLeaderShots = 6   // у скольких лидеров читать главное фото: запрос на каждого
	photoProducts    = 200 // товаров ниши за запрос (один запрос тарифа при любом числе до 500): в топ-50 лидеров с отзывами от разных продавцов — 2 (живая проверка #608)
)

// photoLeader — лидер ниши для окна.
type photoLeader struct {
	SKU      string  `json:"sku"`
	Name     string  `json:"name"`
	Seller   string  `json:"seller"`
	Price    float64 `json:"price"`
	Rating   float64 `json:"rating"`
	Comments int     `json:"comments"`
	Photos   int     `json:"photos"`
	Video    bool    `json:"video"`
	Photo    string  `json:"photo"`
}

// photoAdvice — совет: photos, video или rich.
type photoAdvice struct {
	Kind  string  `json:"kind"`
	Have  float64 `json:"have"`
	Their float64 `json:"their"`
}

// photosView — фото карточки против лидеров ниши (#608).
type photosView struct {
	SKU        string        `json:"sku"`
	Niche      string        `json:"niche"`
	Own        []string      `json:"own"`      // фото из кабинета — показать
	OwnCount   int           `json:"ownCount"` // сколько фото для сравнения: из MPStats, как у лидеров
	OwnVideo   bool          `json:"ownVideo"`
	OwnRich    bool          `json:"ownRich"`
	Leaders    []photoLeader `json:"leaders"`
	Median     float64       `json:"median"`
	VideoShare float64       `json:"videoShare"`
	Advice     []photoAdvice `json:"advice"`
}

// MarketsPhotos compares the photos of card sku in cabinet cab with the niche leaders.
func (a *App) MarketsPhotos(cab, sku string, fresh bool) (photosView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	return a.settings.photosCheck(ctx, nil, ozon.DefaultURL, cab, sku, fresh)
}

// photosCheck reads the card's photos from the cabinet, the niche's products
// and the leaders' main photos from MPStats (1 + до 6 запросов; ниша — в памяти).
func (f *folderSettings) photosCheck(ctx context.Context, hc *http.Client, base, cab, sku string, fresh bool) (photosView, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return photosView{}, errors.New("фото сравниваются у товара Ozon с числовым SKU")
	}
	card, err := f.ozonCard(ctx, hc, base, cab, id)
	if err != nil {
		return photosView{}, err
	}
	cl, connID, err := f.mpstatsFirst(hc)
	if err != nil {
		return photosView{}, err
	}
	m := mpstats.MCP{URL: f.mcpBase(), Token: cl.Token, HTTP: hc}
	mc, err := f.mpstatsCard(ctx, m, connID, id, fresh) // фото нужен только путь ниши: без запросов и брендов (ревью #627)
	if err != nil {
		return photosView{}, err
	}
	ps, err := f.readNicheProducts(ctx, m, connID, mc.Niche, fresh)
	if err != nil {
		return photosView{}, err
	}
	own := markets.PhotoCard{SKU: id, Seller: mc.Seller, Photos: mc.Photos, Video: card.Video, Rich: strings.TrimSpace(card.Rich) != ""}
	if own.Photos == 0 { // MPStats не знает числа — считаем по кабинету
		own.Photos = len(card.Photos)
	}
	c := markets.CheckPhotos(own, ps, photoLeaders)
	leaderPhotos(ctx, m, c.Leaders)
	v := photosView{SKU: strconv.FormatInt(id, 10), Niche: mc.Niche, Own: card.Photos, OwnCount: own.Photos, OwnVideo: own.Video, OwnRich: own.Rich,
		Median: c.MedianPhotos, VideoShare: c.VideoShare, Leaders: []photoLeader{}, Advice: []photoAdvice{}}
	if v.Own == nil {
		v.Own = []string{}
	}
	for _, p := range c.Leaders {
		v.Leaders = append(v.Leaders, photoLeader{SKU: strconv.FormatInt(p.SKU, 10), Name: p.Name, Seller: p.Seller, Price: p.Price, Rating: p.Rating,
			Comments: p.Comments, Photos: p.Photos, Video: p.Video, Photo: p.Photo})
	}
	for _, a := range c.Advice {
		v.Advice = append(v.Advice, photoAdvice{Kind: a.Kind, Have: a.Have, Their: a.Their})
	}
	return v, nil
}

// mpstatsCard — карточка товара в MPStats: из памяти «Названия», иначе один запрос.
func (f *folderSettings) mpstatsCard(ctx context.Context, m mpstats.MCP, connID string, id int64, fresh bool) (mpstats.Card, error) {
	if cp, ok := f.cardParts.get(connID+"|"+strconv.FormatInt(id, 10), time.Now()); ok && !fresh {
		return cp.card, nil
	}
	return readTitleCard(ctx, m, id)
}

// readNicheProducts — товары ниши из памяти или одним запросом.
func (f *folderSettings) readNicheProducts(ctx context.Context, m mpstats.MCP, connID, niche string, fresh bool) ([]markets.NicheProduct, error) {
	key := connID + "|" + niche
	if ps, ok := f.nicheProducts.get(key, time.Now()); ok && !fresh {
		return ps, nil
	}
	ps, err := m.NicheProducts(ctx, niche, photoProducts)
	if err != nil {
		return nil, errors.New("товары ниши «" + niche + "» не прочитаны — " + mpstatsMessage(err))
	}
	f.nicheProducts.put(key, ps, time.Now())
	return ps, nil
}

// leaderPhotos fills the leaders' main photos, до четырех запросов сразу:
// не прочитанное фото — пустое, сравнение остается.
func leaderPhotos(ctx context.Context, m mpstats.MCP, ls []markets.NicheProduct) {
	sem := make(chan struct{}, 4) // MPStats рвет соединение при ~8 параллельных (замер 13.11)
	var wg sync.WaitGroup
	for i := range ls {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if c, err := m.Card(ctx, ls[i].SKU); err == nil {
				ls[i].Photo = c.Photo
			}
		})
	}
	wg.Wait()
}
