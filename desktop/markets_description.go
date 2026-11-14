package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/markets"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

// descriptionMismatch — слово описания, которое спорит с товаром.
type descriptionMismatch struct {
	Kind  string `json:"kind"` // gender | color
	Found string `json:"found"`
	Card  string `json:"card"`
}

// descriptionView — проверка описания карточки (#611): расхождения первыми,
// затем слова ниши, которых в описании нет.
type descriptionView struct {
	SKU         string                `json:"sku"`
	Niche       string                `json:"niche"`
	Main        string                `json:"main"`
	Description string                `json:"description"`
	Length      int                   `json:"length"`
	Empty       bool                  `json:"empty"`
	Mismatch    []descriptionMismatch `json:"mismatch"`
	Missing     []titleWordRow        `json:"missing"`
	Note        string                `json:"note"`
}

// descriptionDraft — новое описание от модели окна.
type descriptionDraft struct {
	Text  string `json:"text"`
	Model string `json:"model"`
}

// MarketsDescription checks the description of card sku in Ozon cabinet cab.
func (a *App) MarketsDescription(cab, sku string) (descriptionView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	return a.settings.descriptionCheck(ctx, nil, ozon.DefaultURL, cab, sku)
}

// MarketsDescriptionDraft asks the chat model for a new description; consent —
// человек согласился отдать текст карточки и запросы ниши провайдеру модели.
func (a *App) MarketsDescriptionDraft(cab, sku string, consent bool) (descriptionDraft, error) {
	if !consent {
		return descriptionDraft{}, errNoDraftConsent
	}
	if err := a.chat.modelReady(); err != nil { // без ключа модели в Ozon и MPStats не ходим
		return descriptionDraft{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	in, _, err := a.settings.descriptionInput(ctx, nil, ozon.DefaultURL, cab, sku)
	if err != nil {
		return descriptionDraft{}, err
	}
	return a.chat.modelDescription(in, markets.CheckDescription(in), true)
}

var errNoDraftConsent = errors.New("новое описание пишет модель окна: текст карточки и запросы ниши уйдут ее провайдеру — нужно согласие")

// descriptionInput reads the card from the cabinet and its niche from MPStats
// (общая с «Названием» и «Хештегами», 10 минут в памяти).
func (f *folderSettings) descriptionInput(ctx context.Context, hc *http.Client, base, cab, sku string) (markets.DescriptionInput, cardNiche, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return markets.DescriptionInput{}, cardNiche{}, errors.New("описание проверяется у товара Ozon с числовым SKU")
	}
	oc, _, err := f.ozonCabinet(cab, base, hc)
	if err != nil {
		return markets.DescriptionInput{}, cardNiche{}, err
	}
	card, err := oc.Card(ctx, strconv.FormatInt(id, 10))
	if errors.Is(err, ozon.ErrNoCard) {
		return markets.DescriptionInput{}, cardNiche{}, errors.New("товара с этим SKU нет в кабинете — загрузите «Магазин» заново")
	}
	if err != nil {
		return markets.DescriptionInput{}, cardNiche{}, ozonErr("Ozon", err)
	}
	cl, connID, err := f.mpstatsFirst(hc)
	if err != nil {
		return markets.DescriptionInput{}, cardNiche{}, err
	}
	n, err := f.readCardNiche(ctx, hc, connID, cl.Token, id)
	if err != nil {
		return markets.DescriptionInput{}, cardNiche{}, err
	}
	return markets.DescriptionInput{Title: card.Name, Gender: card.Gender, Color: card.Color, Description: card.Annotation,
		Brands: []string{n.card.Brand, n.card.Seller}, Queries: n.qs, Forms: n.ws, NicheBrands: n.brands}, n, nil
}

// descriptionCheck checks the description by the card and the niche, без модели.
func (f *folderSettings) descriptionCheck(ctx context.Context, hc *http.Client, base, cab, sku string) (descriptionView, error) {
	in, n, err := f.descriptionInput(ctx, hc, base, cab, sku)
	if err != nil {
		return descriptionView{}, err
	}
	c := markets.CheckDescription(in)
	v := descriptionView{SKU: strings.TrimSpace(sku), Niche: n.card.Niche, Main: c.Main.Text, Description: in.Description, Length: c.Length, Empty: c.Empty,
		Mismatch: []descriptionMismatch{}, Missing: titleRows(c.Missing), Note: titleNote(nil, n.brandErr)}
	for _, m := range c.Mismatch {
		v.Mismatch = append(v.Mismatch, descriptionMismatch{Kind: m.Kind, Found: m.Found, Card: m.Card})
	}
	return v, nil
}

// modelDescription asks the chat model for a new description; без согласия — отказ до вызова.
func (d *chatDesk) modelDescription(in markets.DescriptionInput, c markets.DescriptionCheck, consent bool) (descriptionDraft, error) {
	if !consent {
		return descriptionDraft{}, errNoDraftConsent
	}
	cfg := d.config()
	key, err := d.modelKey(cfg)
	if err != nil {
		return descriptionDraft{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	model := d.metered(d.model(cfg, key), cfg, usage.Fixed(domain.CallCardText), "")
	reply, err := model.Complete(ctx, []chat.ModelMessage{
		{Role: chat.RoleSystem, Content: markets.DescriptionSystem},
		{Role: chat.RoleUser, Content: markets.DescriptionPrompt(in, c)},
	}, nil)
	if err != nil {
		return descriptionDraft{}, errors.New(chatError(err))
	}
	text := strings.TrimSpace(reply.Content)
	if text == "" {
		return descriptionDraft{}, errors.New("модель вернула пустой текст — попробуйте еще раз")
	}
	return descriptionDraft{Text: text, Model: cfg.Model}, nil
}
