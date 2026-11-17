package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// reviewsView — отзывы о товаре без модели (#612): сколько, сколько с
// текстом, период, оценки по звездам (Ratings[0] — одна звезда).
type reviewsView struct {
	SKU      string  `json:"sku"`
	Total    int     `json:"total"`
	WithText int     `json:"withText"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Ratings  []int   `json:"ratings"`
	Avg      float64 `json:"avg"`
	Calls    int     `json:"calls"` // запросов тарифа MPStats на эту проверку
	Source   string  `json:"source"`
}

// reviewPointRow — вывод и сколько отзывов его подтверждают.
type reviewPointRow struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// reviewFindingsView — «хвалят» и «жалуются» моделью окна; Used — по скольким
// отзывам с текстом.
type reviewFindingsView struct {
	Praise     []reviewPointRow `json:"praise"`
	Complaints []reviewPointRow `json:"complaints"`
	Used       int              `json:"used"`
	Model      string           `json:"model"`
}

var (
	errNoReviewsConsent = errors.New("выводы делает модель окна: тексты отзывов уйдут ее провайдеру — нужно согласие")
	errNoReviewTexts    = errors.New("у отзывов нет текстов — делать выводы не по чему")
)

// MarketsReviews shows the reviews of card sku; fresh — мимо памяти.
func (a *App) MarketsReviews(sku string, fresh bool) (reviewsView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return a.settings.reviewsCheck(ctx, nil, sku, fresh, time.Now())
}

// MarketsReviewFindings asks the chat model what buyers praise and complain
// about; consent — человек согласился отдать тексты отзывов провайдеру модели.
func (a *App) MarketsReviewFindings(sku string, consent bool) (reviewFindingsView, error) {
	if !consent {
		return reviewFindingsView{}, errNoReviewsConsent
	}
	if err := a.chat.modelReady(); err != nil { // без ключа модели в MPStats не ходим
		return reviewFindingsView{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	rs, _, err := a.settings.readReviews(ctx, nil, sku, false, time.Now())
	if err != nil {
		return reviewFindingsView{}, err
	}
	return a.chat.modelReviews(rs, true)
}

// readReviews reads the card's reviews: память на 10 минут или один отчет
// comments; calls — запросов тарифа. Отзывов нет — пустой список, не сбой.
func (f *folderSettings) readReviews(ctx context.Context, hc *http.Client, sku string, fresh bool, now time.Time) ([]markets.Review, int, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return nil, 0, errors.New("отзывы читаются у товара Ozon с числовым SKU")
	}
	cl, connID, err := f.mpstatsFirst(hc)
	if err != nil {
		return nil, 0, err
	}
	key := connID + "|" + strconv.FormatInt(id, 10)
	if rs, ok := f.reviewParts.get(key, now); ok && !fresh {
		return rs, 0, nil
	}
	rs, err := mpstats.MCP{URL: f.mcpBase(), Token: cl.Token, HTTP: hc}.Comments(ctx, id)
	if err != nil && !errors.Is(err, mpstats.ErrNotFound) {
		return nil, 1, errors.New("отзывы не прочитаны — " + mpstatsMessage(err))
	}
	f.reviewParts.put(key, rs, now)
	return rs, 1, nil
}

// reviewsCheck summarizes the card's reviews without the model.
func (f *folderSettings) reviewsCheck(ctx context.Context, hc *http.Client, sku string, fresh bool, now time.Time) (reviewsView, error) {
	rs, calls, err := f.readReviews(ctx, hc, sku, fresh, now)
	if err != nil {
		return reviewsView{}, err
	}
	s := markets.SummarizeReviews(rs)
	v := reviewsView{SKU: strings.TrimSpace(sku), Total: s.Total, WithText: s.WithText, Ratings: s.Ratings[1:], Avg: s.Avg, Calls: calls,
		Source: "MPStats, отчет ozon_sku comments"}
	if s.Total > 0 {
		v.From, v.To = s.From.Format(time.DateOnly), s.To.Format(time.DateOnly)
	}
	return v, nil
}

// modelReviews asks the chat model for findings; без согласия и без текстов —
// отказ до вызова.
func (d *chatDesk) modelReviews(rs []markets.Review, consent bool) (reviewFindingsView, error) {
	if !consent {
		return reviewFindingsView{}, errNoReviewsConsent
	}
	prompt, n := markets.ReviewsPrompt(rs)
	if n == 0 {
		return reviewFindingsView{}, errNoReviewTexts
	}
	reply, err := d.modelTextFor(domain.CallReviews, markets.ReviewsSystem, prompt)
	if err != nil {
		return reviewFindingsView{}, err
	}
	f, err := markets.ParseReviewFindings(reply.Text, n)
	if err != nil {
		return reviewFindingsView{}, errors.New("модель ответила не выводами — попробуйте еще раз")
	}
	rows := func(ps []markets.ReviewPoint) []reviewPointRow {
		out := make([]reviewPointRow, 0, len(ps))
		for _, p := range ps {
			out = append(out, reviewPointRow{Text: p.Text, Count: p.Count})
		}
		return out
	}
	return reviewFindingsView{Praise: rows(f.Praise), Complaints: rows(f.Complaints), Used: n, Model: reply.Model}, nil
}
