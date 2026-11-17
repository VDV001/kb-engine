package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // фото карточек Ozon — jpg и png
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// richBlockView — блок варианта: заголовок, текст и адрес фото карточки.
type richBlockView struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Photo string `json:"photo"`
}

// richVariantView — вариант рич-контента (#671) и его «Rich-контент JSON» для Ozon.
type richVariantView struct {
	Name   string          `json:"name"`
	Blocks []richBlockView `json:"blocks"`
	JSON   string          `json:"json"`
}

// richView — варианты от модели окна.
type richView struct {
	Variants []richVariantView `json:"variants"`
	Model    string            `json:"model"`
}

var errNoRichConsent = errors.New("варианты пишет модель окна: название, описание карточки и выводы из отзывов уйдут ее провайдеру — нужно согласие")

// MarketsRich asks the chat model for rich-content variants of card sku;
// praise и complaints — выводы из отзывов, если их уже сделали (#612).
func (a *App) MarketsRich(cab, sku string, praise, complaints []string, consent bool) (richView, error) {
	if err := a.chat.modelReady(); err != nil {
		return richView{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	return a.settings.richVariants(ctx, nil, a.chat, ozon.DefaultURL, cab, sku, praise, complaints, consent)
}

// MarketsRichSave saves a variant's JSON for «Rich-контент JSON» in the cabinet.
func (a *App) MarketsRichSave(sku, name, doc string) (string, error) {
	if err := checkRichJSON(doc); err != nil {
		return "", err
	}
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Сохранить рич-контент",
		DefaultFilename: strings.TrimSpace("Рич-контент "+sku+" "+name) + ".json"})
	if err != nil || p == "" {
		return "", err
	}
	return p, os.WriteFile(p, []byte(doc), 0o600)
}

// checkRichJSON refuses what Ozon would not take as rich content.
func checkRichJSON(doc string) error {
	var d struct {
		Content *[]json.RawMessage `json:"content"`
		Version float64            `json:"version"`
	}
	if json.Unmarshal([]byte(doc), &d) != nil || d.Content == nil || d.Version == 0 {
		return errors.New("это не рич-контент Ozon: нужен объект с content и version")
	}
	return nil
}

// richVariants reads the card from the cabinet, asks the model and builds
// Ozon's JSON with the real sizes of the card's photos.
func (f *folderSettings) richVariants(ctx context.Context, hc *http.Client, cd *chatDesk, base, cab, sku string, praise, complaints []string, consent bool) (richView, error) {
	if !consent {
		return richView{}, errNoRichConsent
	}
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return richView{}, errors.New("рич-контент предлагается у товара Ozon с числовым SKU")
	}
	card, err := f.ozonCard(ctx, hc, base, cab, id)
	if err != nil {
		return richView{}, err
	}
	if len(card.Photos) == 0 {
		return richView{}, errors.New("у карточки нет фото — блокам рич-контента нечего показать")
	}
	in := markets.RichInput{Title: card.Name, Description: card.Annotation, Praise: praise, Complaints: complaints, Photos: len(card.Photos)}
	reply, err := cd.modelTextFor(domain.CallCardText, markets.RichSystem, markets.RichPrompt(in))
	if err != nil {
		return richView{}, err
	}
	vs, err := markets.ParseRichVariants(reply.Text, len(card.Photos))
	if err != nil {
		return richView{}, errors.New("модель ответила не вариантами — попробуйте еще раз")
	}
	photos, err := photoSizes(ctx, hc, card.Photos, vs)
	if err != nil {
		return richView{}, err
	}
	v := richView{Model: reply.Model}
	for _, rv := range vs {
		doc, err := markets.RichJSON(rv, photos)
		if err != nil {
			return richView{}, err
		}
		out := richVariantView{Name: rv.Name, JSON: string(doc)}
		for _, b := range rv.Blocks {
			out.Blocks = append(out.Blocks, richBlockView{Title: b.Title, Text: b.Text, Photo: card.Photos[b.Photo-1]})
		}
		v.Variants = append(v.Variants, out)
	}
	return v, nil
}

// photoSizes reads the size of every photo the variants use (только их).
func photoSizes(ctx context.Context, hc *http.Client, urls []string, vs []markets.RichVariant) ([]markets.RichPhoto, error) {
	out := make([]markets.RichPhoto, len(urls))
	for i, u := range urls {
		out[i].URL = u
	}
	for _, v := range vs {
		for _, b := range v.Blocks {
			p := &out[b.Photo-1]
			if p.Width > 0 {
				continue
			}
			w, h, err := imageSize(ctx, hc, p.URL)
			if err != nil {
				return nil, fmt.Errorf("размер фото %d не прочитан — %w", b.Photo, err)
			}
			p.Width, p.Height = w, h
		}
	}
	return out, nil
}

// maxPhotoHead — сколько байт фото читать ради размера: заголовок jpg и png в начале.
const maxPhotoHead = 1 << 20

func imageSize(ctx context.Context, hc *http.Client, u string) (int, int, error) {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return 0, 0, errors.New("адрес не http")
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, errors.New(resp.Status)
	}
	cfg, _, err := image.DecodeConfig(io.LimitReader(resp.Body, maxPhotoHead))
	if err != nil {
		return 0, 0, errors.New("формат не jpg и не png")
	}
	return cfg.Width, cfg.Height, nil
}
