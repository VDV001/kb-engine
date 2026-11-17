package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // фото карточек Ozon — jpg и png
	_ "image/png"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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
	Photo int    `json:"photo"` // номер фото карточки, с 1
}

// richVariantView — вариант рич-контента (#671) и его «Rich-контент JSON» для Ozon.
type richVariantView struct {
	Name   string          `json:"name"`
	Blocks []richBlockView `json:"blocks"`
	JSON   string          `json:"json"`
	// PhotosFixed — модель назвала фото, которого нет: окно взяло незанятое.
	PhotosFixed bool `json:"photosFixed"`
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
	return a.settings.richVariants(ctx, nil, a.chat, ozon.DefaultURL, cab, sku, praise, complaints, consent, fetchImage(ozonImageHosts, maxStudioImage))
}

// MarketsRichSave saves a variant's JSON for «Rich-контент JSON» in the cabinet.
func (a *App) MarketsRichSave(sku, name, doc string) (string, error) {
	if err := checkRichJSON(doc); err != nil {
		return "", err
	}
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Сохранить рич-контент",
		DefaultFilename: richFileName(sku, name)})
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
func (f *folderSettings) richVariants(ctx context.Context, hc *http.Client, cd *chatDesk, base, cab, sku string, praise, complaints []string, consent bool, fetch func(context.Context, string) ([]byte, error)) (richView, error) {
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
	photos, err := photoSizes(ctx, fetch, card.Photos, vs)
	if err != nil {
		return richView{}, err
	}
	v := richView{Model: reply.Model}
	for _, rv := range vs {
		doc, err := markets.RichJSON(rv, photos)
		if err != nil {
			return richView{}, err
		}
		out := richVariantView{Name: rv.Name, JSON: string(doc), PhotosFixed: rv.PhotosFixed}
		for _, b := range rv.Blocks {
			out.Blocks = append(out.Blocks, richBlockView{Title: b.Title, Text: b.Text, Photo: b.Photo})
		}
		v.Variants = append(v.Variants, out)
	}
	return v, nil
}

// photoSizes reads the size of every photo the variants use (только их, разом):
// скачивает fetch — в окне только https с ozone.ru (ревью #676, как #629).
func photoSizes(ctx context.Context, fetch func(context.Context, string) ([]byte, error), urls []string, vs []markets.RichVariant) ([]markets.RichPhoto, error) {
	out := make([]markets.RichPhoto, len(urls))
	errs := make([]error, len(urls))
	need := map[int]bool{}
	for _, v := range vs {
		for _, b := range v.Blocks {
			need[b.Photo-1] = true
		}
	}
	var wg sync.WaitGroup
	for i, u := range urls {
		out[i].URL = u
		if !need[i] {
			continue
		}
		wg.Go(func() {
			b, err := fetch(ctx, u)
			if err == nil {
				var cfg image.Config
				if cfg, _, err = image.DecodeConfig(bytes.NewReader(b)); err != nil {
					err = errors.New("формат не jpg и не png")
				}
				out[i].Width, out[i].Height = cfg.Width, cfg.Height
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("размер фото %d не прочитан — %w", i+1, err)
		}
	}
	return out, nil
}

// richFileName is the default file name; имя варианта придумала модель —
// без знаков, которые ломают путь (ревью #676).
func richFileName(sku, name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return ' '
		}
		return r
	}, name)
	return strings.Join(strings.Fields("Рич-контент "+sku+" "+name), " ") + ".json"
}
