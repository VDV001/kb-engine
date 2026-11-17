package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/imagegen"
)

// maxImagePrompt — предел задания: платный вызов по ключу человека не должен
// уносить мегабайты текста.
const maxImagePrompt = 8000

// hostOf is the host of a gateway address, for «тот же шлюз»; кривой адрес — он сам.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.ToLower(u.Host)
	}
	return raw
}

// imageKeyAccount — учетка ключа модели изображений в Связке ключей окна (#678).
const imageKeyAccount = "images"

type imageSettingsView struct {
	URL    string `json:"url"`
	Model  string `json:"model"`
	HasKey bool   `json:"hasKey"`
}

// ownImage is a picture of the person's own image model: сразу data URL, без
// скачивания по адресу из ответа.
type ownImage struct {
	Data  string `json:"data"`
	Cost  string `json:"cost"`
	Model string `json:"model"`
}

// imageSettings is the image model as the page shows it; ключ — только «есть».
func (f *folderSettings) imageSettings() imageSettingsView {
	im := f.snapshot().cfg.Images
	_, err := f.tokens.Get(imageKeyAccount)
	return imageSettingsView{URL: im.URL, Model: im.Model, HasKey: err == nil}
}

// setImageSettings connects (или, при пустых адресе и имени, отключает и стирает
// ключ) the image model; пустой key при подключении оставляет прежний.
func (f *folderSettings) setImageSettings(url, model, key string) error {
	im := desktopconfig.Images{URL: strings.TrimSpace(url), Model: strings.TrimSpace(model)}
	if err := im.Check(); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	f.mu.Lock()
	defer f.mu.Unlock()
	// Ключ не переезжает на другой хост сам: сменил адрес — введи ключ заново (ревью #683).
	if im.On() && key == "" && f.cfg.Images.On() && hostOf(f.cfg.Images.URL) != hostOf(im.URL) {
		return errors.New("адрес шлюза другой: введите ключ этого шлюза, прежний на него не уйдет")
	}
	next := f.cfg
	next.Images = im
	if err := filelock.With(f.path, func() error {
		if err := desktopconfig.Write(f.path, next); err != nil {
			return err
		}
		f.cfg = next
		f.publishLocked(false)
		return nil
	}); err != nil {
		return err
	}
	if key != "" && im.On() { // после конфига: сбой записи не оставит новый ключ при старом адресе
		if err := f.tokens.Set(imageKeyAccount, key); err != nil {
			return fmt.Errorf("ключ не сохранен в Связке ключей: %w", err)
		}
	}
	if !im.On() {
		_ = f.tokens.Delete(imageKeyAccount) // отключили — ключ не лежит без дела
	}
	return nil
}

// imageClient returns the client of the connected image model, with its key.
func (f *folderSettings) imageClient() (imagegen.Client, error) {
	im := f.snapshot().cfg.Images
	if !im.On() {
		return imagegen.Client{}, errors.New("модель изображений не подключена: адрес, имя и ключ — в «Чат и модель»")
	}
	key, err := f.tokens.Get(imageKeyAccount)
	if err != nil || key == "" {
		return imagegen.Client{}, errors.New("нет ключа модели изображений — введите его в «Чат и модель»")
	}
	return imagegen.Client{URL: im.URL, Model: im.Model, Key: key, HTTP: &http.Client{Timeout: 3 * time.Minute}}, nil
}

// ownInfographic draws the card's infographic with the person's image model:
// главное фото карточки — референс (#678). Платный вызов — только по кнопке
// после показа цены; здесь вызов один, и он идет в журнал расходов.
func (f *folderSettings) ownInfographic(ctx context.Context, hc *http.Client, base, cab, sku, prompt string, draw imageDesk, fetch func(context.Context, string) ([]byte, error)) (ownImage, error) {
	if prompt = strings.TrimSpace(prompt); prompt == "" {
		return ownImage{}, errors.New("пустое задание — напишите его или попросите модель окна")
	}
	if utf8.RuneCountInString(prompt) > maxImagePrompt {
		return ownImage{}, fmt.Errorf("задание длиннее %d знаков: сократите его", maxImagePrompt)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return ownImage{}, errors.New("инфографика рисуется для товара Ozon с числовым SKU")
	}
	card, err := f.ozonCard(ctx, hc, base, cab, id)
	if err != nil {
		return ownImage{}, err
	}
	if len(card.Photos) == 0 {
		return ownImage{}, errors.New("у карточки нет фото — не с чего рисовать товар")
	}
	photo, err := fetch(ctx, card.Photos[0])
	if err != nil {
		return ownImage{}, err
	}
	img, err := draw.Generate(ctx, prompt, [][]byte{photo})
	if err != nil {
		return ownImage{}, err
	}
	return ownImage{Data: "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data), Cost: img.Cost, Model: draw.model}, nil
}
