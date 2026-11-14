package main

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// titleWhy — коды причин markets.TitleWhy для окна; слова причин — в app.js.
var titleWhy = map[markets.TitleWhy]string{markets.WhyKeep: "keep", markets.WhyMain: "main", markets.WhyFrequent: "often",
	markets.WhyRare: "rare", markets.WhyBrand: "brand", markets.WhyAttribute: "attr", markets.WhyOwn: "own", markets.WhyNicheBrand: "brand-niche"}

// titleBrandsWait — сколько ждать отчет брендов ниши: обычно 7 с (замер 14.11).
const titleBrandsWait = 30 * time.Second

// titleWordRow — слово названия или совет; Share — доля спроса как 0,25.
type titleWordRow struct {
	Word     string  `json:"word"`
	Share    float64 `json:"share"`
	Why      string  `json:"why"`
	Query    string  `json:"query"`
	Position int     `json:"position"`
}

// titleView — оценка названия карточки (#607): главный запрос ниши и откуда он,
// по скольким запросам выводы, слова названия, что добавить и что убрать.
type titleView struct {
	SKU         string         `json:"sku"`
	Title       string         `json:"title"`
	Niche       string         `json:"niche"`
	Main        string         `json:"main"`
	MainCount   int            `json:"mainCount"`
	MainItems   int            `json:"mainItems"`
	Total       int            `json:"total"`
	Related     int            `json:"related"`
	Words       []titleWordRow `json:"words"`
	Add         []titleWordRow `json:"add"`
	Remove      []titleWordRow `json:"remove"`
	Dropped     []titleWordRow `json:"dropped"`     // частые, но бренды ниши (#623)
	NicheBrands int            `json:"nicheBrands"` // сколько брендов ниши учтено
	Note        string         `json:"note"`        // свои запросы товара или бренды ниши не прочитаны
}

// MarketsTitle checks the title of Ozon card sku against its niche's queries in MPStats.
// fresh — «Проверить еще раз»: перечитать нишу из MPStats, а не из памяти.
func (a *App) MarketsTitle(sku, title string, fresh bool) (titleView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute) // карточка, потом ниша — до 90 с каждая с повтором (ревью #621)
	defer cancel()
	return a.settings.titleCheck(ctx, nil, sku, title, fresh)
}

// mcpBase — адрес MCP MPStats: в окне настоящий, в тестах — поддельный.
func (f *folderSettings) mcpBase() string {
	if f.mcpURL != "" {
		return f.mcpURL
	}
	return mpstats.MCPURL
}

// titleCheck reads sku's niche from its MPStats card, then the niche's queries
// with its brands and the card's own queries at once: четыре запроса тарифа на проверку.
func (f *folderSettings) titleCheck(ctx context.Context, hc *http.Client, sku, title string, fresh bool) (titleView, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return titleView{}, errors.New("название проверяется у товара Ozon с числовым SKU")
	}
	if strings.TrimSpace(title) == "" {
		return titleView{}, errors.New("название товара пустое — проверять нечего")
	}
	cl, connID, err := f.mpstatsFirst(hc)
	if err != nil {
		return titleView{}, err
	}
	n, err := f.readCardNiche(ctx, hc, connID, cl.Token, id, fresh)
	if err != nil {
		return titleView{}, err
	}
	card, qs, ws, own, brands, ownErr, brandErr := n.card, n.qs, n.ws, n.own, n.brands, n.ownErr, n.brandErr
	r := markets.CheckTitle(markets.TitleInput{Title: title, Brands: []string{card.Brand, card.Seller}, Queries: qs, Forms: ws, Own: own, NicheBrands: brands})
	if r.Main.Text == "" { // пустой ответ — не «добавить и убрать нечего» (ревью #621)
		return titleView{}, errors.New("у ниши «" + card.Niche + "» в MPStats нет запросов за 30 дней — проверить название не по чему")
	}
	v := titleView{SKU: strconv.FormatInt(id, 10), Title: title, Niche: card.Niche, Main: r.Main.Text, MainCount: r.Main.Count, MainItems: r.Main.Items,
		Total: r.Total, Related: r.Related, Words: titleRows(r.Words), Add: titleRows(r.Add), Remove: titleRows(r.Remove),
		Dropped: titleRows(r.Dropped), NicheBrands: r.NicheBrands, Note: titleNote(ownErr, brandErr)}
	return v, nil
}

// titleNote names what was not read: проверка остается, но без этой части.
func titleNote(ownErr, brandErr error) string {
	var notes []string
	if ownErr != nil && !errors.Is(ownErr, mpstats.ErrNotFound) { // пусто — товар не находят ни по одному запросу, это не сбой
		notes = append(notes, "запросы самого товара не прочитаны — "+mpstatsMessage(ownErr))
	}
	if brandErr != nil && !errors.Is(brandErr, mpstats.ErrNotFound) {
		notes = append(notes, "бренды ниши не прочитаны, чужие бренды в «Добавить» не отсеяны — "+mpstatsMessage(brandErr))
	}
	return strings.Join(notes, "; ")
}

// cardNiche — ниша товара в MPStats с ее запросами и брендами и свои запросы
// товара: общие для «Названия» и «Хештегов» (#610).
type cardNiche struct {
	card             mpstats.Card
	qs               []markets.NicheQuery
	ws               []markets.WordForms
	own              []markets.OwnQuery
	brands           []string
	ownErr, brandErr error
}

// ttlMap keeps values for mpstatsTTL and drops stale ones on every put, чтобы
// память не росла с каждым открытым товаром (ревью #625).
// ponytail: в markets_mpstats.go еще три таких же кэша без чистки; потолок —
// они растут за жизнь окна, апгрейд — перевести их на ttlMap.
type ttlMap[V any] struct {
	mu sync.Mutex
	m  map[string]ttlEntry[V]
}

type ttlEntry[V any] struct {
	at time.Time
	v  V
}

func (c *ttlMap[V]) get(k string, now time.Time) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	return e.v, ok && now.Sub(e.at) < mpstatsTTL
}

func (c *ttlMap[V]) put(k string, v V, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]ttlEntry[V]{}
	}
	maps.DeleteFunc(c.m, func(_ string, e ttlEntry[V]) bool { return now.Sub(e.at) >= mpstatsTTL })
	c.m[k] = ttlEntry[V]{at: now, v: v}
}

// cardPart — карточка товара в MPStats и свои запросы: по «подключение|SKU».
type cardPart struct {
	card   mpstats.Card
	own    []markets.OwnQuery
	ownErr error
}

// nichePart — запросы, словоформы и бренды ниши: по «подключение|путь», общие
// для всех товаров ниши (ревью #625: 40 футболок — один отчет).
type nichePart struct {
	qs       []markets.NicheQuery
	ws       []markets.WordForms
	brands   []string
	brandErr error
}

// readCardNiche reads sku's card and, at once, its own queries and its niche;
// fresh — перечитать («Проверить еще раз»). Хранится только прочитанное
// целиком: пустой ответ («брендов нет», «запросов товара нет») — не сбой.
func (f *folderSettings) readCardNiche(ctx context.Context, hc *http.Client, connID, token string, id int64, fresh bool) (cardNiche, error) {
	now := time.Now()
	ck := connID + "|" + strconv.FormatInt(id, 10)
	m := mpstats.MCP{URL: f.mcpBase(), Token: token, HTTP: hc}
	cp, haveCard := f.cardParts.get(ck, now)
	if !haveCard || fresh {
		card, err := readTitleCard(ctx, m, id)
		if err != nil {
			return cardNiche{}, err
		}
		cp = cardPart{card: card}
	}
	nk := connID + "|" + cp.card.Niche
	np, haveNiche := f.nicheParts.get(nk, now)
	if !haveNiche || fresh {
		np = nichePart{}
	}
	if err := readParts(ctx, m, id, &cp, &np, !haveCard || fresh, !haveNiche || fresh); err != nil {
		return cardNiche{}, err
	}
	if (!haveCard || fresh) && cp.ownErr == nil { // кладется только прочитанное: чтение из памяти срок не продлевает (ревью #627)
		f.cardParts.put(ck, cp, now)
	}
	if (!haveNiche || fresh) && np.brandErr == nil {
		f.nicheParts.put(nk, np, now)
	}
	return cardNiche{card: cp.card, qs: np.qs, ws: np.ws, own: cp.own, brands: np.brands, ownErr: cp.ownErr, brandErr: np.brandErr}, nil
}

// readParts reads at once what is missing: свои запросы товара (own) и
// запросы с брендами ниши (niche). Пустой ответ — не сбой: «брендов нет».
func readParts(ctx context.Context, m mpstats.MCP, id int64, cp *cardPart, np *nichePart, own, niche bool) error {
	var nicheErr error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	if own {
		wg.Go(func() { cp.own, cp.ownErr = m.CardQueries(ctx, id) })
	}
	if niche {
		wg.Go(func() {
			if np.qs, np.ws, nicheErr = m.NicheQueries(ctx, cp.card.Niche); stopsAll(nicheErr) {
				cancel()
			}
		})
		wg.Go(func() { // бренды — не главное: свой срок, чтобы занятый отчет не держал проверку (ревью #624)
			bctx, bcancel := context.WithTimeout(ctx, titleBrandsWait)
			defer bcancel()
			if np.brands, np.brandErr = m.NicheBrands(bctx, cp.card.Niche); stopsAll(np.brandErr) {
				cancel()
			}
		})
	}
	wg.Wait()
	if nicheErr != nil {
		return errors.New("запросы ниши «" + cp.card.Niche + "» не прочитаны — " + mpstatsMessage(nicheErr))
	}
	if errors.Is(cp.ownErr, mpstats.ErrNotFound) {
		cp.own, cp.ownErr = nil, nil // товар не находят ни по одному запросу
	}
	if errors.Is(np.brandErr, mpstats.ErrNotFound) {
		np.brands, np.brandErr = nil, nil // у ниши нет брендов
	}
	return nil
}

// readTitleCard reads sku's MPStats card; без ниши проверять не по чему.
func readTitleCard(ctx context.Context, m mpstats.MCP, id int64) (mpstats.Card, error) {
	card, err := m.Card(ctx, id)
	if errors.Is(err, mpstats.ErrNotFound) || err == nil && strings.TrimSpace(card.Niche) == "" {
		return card, errors.New("MPStats не знает ниши этого товара: товар новый или еще не продавался — проверить не по чему")
	}
	if err != nil {
		return card, errors.New(mpstatsMessage(err))
	}
	return card, nil
}

func titleRows(ws []markets.TitleWord) []titleWordRow {
	out := make([]titleWordRow, 0, len(ws)) // не nil: в JSON — [], а не null
	for _, w := range ws {
		out = append(out, titleWordRow{Word: w.Word, Share: w.Share, Why: titleWhy[w.Why], Query: w.Query, Position: w.Position})
	}
	return out
}
