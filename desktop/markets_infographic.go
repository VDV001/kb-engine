package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/adapter/ozon"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/markets"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

// Инфографика карточки (#609): ИИ Студия MPStats рисует по заданию модели окна
// и главному фото товара. Генерация платная — только по кнопке после показа цены
// (решение владельца 15.11); цена, баланс и загрузка фото бесплатны.

const maxStudioImage = 7 << 20 // фото-референс: предел Nano Banana — 7168 КБ (ai_studio_models, 15.11)

var (
	ozonImageHosts   = []string{"ozone.ru"}         // фото товара: ir.ozone.ru, cdnN.ozone.ru
	studioImageHosts = []string{"images.24ai.tech"} // готовые картинки ИИ Студии (замер 15.11)
)

// studioModelView — модель на выбор: цена в токенах или почему ее не узнать.
type studioModelView struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Cost int    `json:"cost"`
	Err  string `json:"err"`
}

// studioOffer — что показать до генерации: баланс и модели с ценой.
type studioOffer struct {
	Balance int               `json:"balance"`
	Models  []studioModelView `json:"models"`
}

// studioJobView — генерация в окне.
type studioJobView struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Done   bool     `json:"done"`
	Cost   int      `json:"cost"`
	Images []string `json:"images"`
	Fail   string   `json:"fail"`
	// Repriced — с фото цена выше согласованной: генерации нет, новая цена — на кнопку (ревью #629).
	Repriced int `json:"repriced"`
}

// MarketsInfographicOffer shows the AI Studio balance and the models with their price.
func (a *App) MarketsInfographicOffer() (studioOffer, error) {
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	return a.settings.infographicOffer(ctx, nil)
}

// MarketsInfographicDraft asks the window model for the picture task; consent —
// человек согласился отдать карточку провайдеру модели.
func (a *App) MarketsInfographicDraft(cab, sku string, consent bool) (descriptionDraft, error) {
	if !consent {
		return descriptionDraft{}, errNoInfographicConsent
	}
	if err := a.chat.modelReady(); err != nil {
		return descriptionDraft{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	in, _, _, err := a.settings.descriptionInput(ctx, nil, ozon.DefaultURL, cab, sku, false)
	if err != nil {
		return descriptionDraft{}, err
	}
	return a.chat.modelText(markets.InfographicSystem, markets.InfographicPrompt(in))
}

var errNoInfographicConsent = errors.New("задание на картинку пишет модель окна: название, пол, цвет и описание карточки и главный запрос ниши уйдут ее провайдеру — нужно согласие")

// MarketsInfographicGenerate starts a paid generation not dearer than maxCost tokens.
func (a *App) MarketsInfographicGenerate(cab, sku, model, prompt string, maxCost int) (studioJobView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
	defer cancel()
	return a.settings.infographicGenerate(ctx, nil, ozon.DefaultURL, cab, sku, model, prompt, maxCost, fetchImage(ozonImageHosts, maxStudioImage))
}

// MarketsInfographicResult reads a started generation.
func (a *App) MarketsInfographicResult(id string) (studioJobView, error) {
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	return a.settings.infographicResult(ctx, nil, id)
}

// MarketsInfographicSave asks where to save image u of sku; "" — отменил.
func (a *App) MarketsInfographicSave(u, sku string) (string, error) {
	if !imageHostOK(u, studioImageHosts) {
		return "", errors.New("сохраняются только картинки ИИ Студии")
	}
	ext := path.Ext(strings.SplitN(u, "?", 2)[0])
	if ext == "" {
		ext = ".jpg"
	}
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Сохранить инфографику", DefaultFilename: "Инфографика " + sku + " " + time.Now().Format("2006-01-02 15.04") + ext})
	if err != nil || p == "" {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	b, err := fetchImage(studioImageHosts, 30<<20)(ctx, u)
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, b, 0o600)
}

// studio — MCP MPStats первого подключения.
func (f *folderSettings) studio(hc *http.Client) (mpstats.MCP, error) {
	cl, _, err := f.mpstatsFirst(hc)
	if err != nil {
		return mpstats.MCP{}, err
	}
	return mpstats.MCP{URL: f.mcpBase(), Token: cl.Token, HTTP: hc}, nil
}

func (f *folderSettings) infographicOffer(ctx context.Context, hc *http.Client) (studioOffer, error) {
	m, err := f.studio(hc)
	if err != nil {
		return studioOffer{}, err
	}
	o := studioOffer{Models: make([]studioModelView, len(mpstats.StudioModels))}
	var balErr error
	var wg sync.WaitGroup // баланс и цены — сразу, 4 запроса: MPStats держит до 4 (замер 13.11); подряд — 9,5 с (живая проверка 15.11)
	wg.Go(func() { o.Balance, balErr = m.StudioBalance(ctx) })
	for i, sm := range mpstats.StudioModels {
		wg.Go(func() {
			v := studioModelView{Slug: sm.Slug, Name: sm.Name}
			var err error
			if v.Cost, err = m.StudioEstimate(ctx, sm.Slug, sm.Params, nil); err != nil {
				v.Err = mpstatsMessage(err)
			}
			o.Models[i] = v
		})
	}
	wg.Wait()
	if balErr != nil {
		return studioOffer{}, errors.New("баланс ИИ Студии не прочитан — " + mpstatsMessage(balErr))
	}
	return o, nil
}

func (f *folderSettings) infographicGenerate(ctx context.Context, hc *http.Client, base, cab, sku, model, prompt string, maxCost int, fetch func(context.Context, string) ([]byte, error)) (studioJobView, error) {
	i := slices.IndexFunc(mpstats.StudioModels, func(sm mpstats.StudioModel) bool { return sm.Slug == model })
	if i < 0 {
		return studioJobView{}, fmt.Errorf("модель «%s» не из списка окна", model)
	}
	sm := mpstats.StudioModels[i]
	if prompt = strings.TrimSpace(prompt); prompt == "" {
		return studioJobView{}, errors.New("пустое задание — напишите его или попросите модель окна")
	}
	m, err := f.studio(hc)
	if err != nil {
		return studioJobView{}, err
	}
	ref, err := f.cardReference(ctx, hc, base, cab, sku, m, sm.Slug, fetch)
	if err != nil {
		return studioJobView{}, err
	}
	cost, err := m.StudioEstimate(ctx, sm.Slug, sm.Params, []string{ref})
	if err != nil {
		return studioJobView{}, errors.New("цена не узнана — " + mpstatsMessage(err))
	}
	if cost > maxCost { // согласие было на другую цену: без трат, новая — на кнопку
		return studioJobView{Repriced: cost, Images: []string{}}, nil
	}
	bal, err := m.StudioBalance(ctx)
	if err != nil {
		return studioJobView{}, errors.New("баланс не прочитан — " + mpstatsMessage(err))
	}
	if bal < cost {
		return studioJobView{}, fmt.Errorf("на балансе ИИ Студии %d токенов, нужно %d — пополните в MPStats", bal, cost)
	}
	// Свой срок платному вызову: подготовка могла съесть общий, и задание ушло бы с обрывом (ревью #629).
	gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	j, err := m.StudioGenerate(gctx, sm.Slug, prompt, cost, sm.Params, []string{ref})
	if err != nil {
		return studioJobView{}, err
	}
	return jobView(j), nil
}

// cardReference uploads the main photo of card sku to AI Studio for model and returns its id;
// файлом, а не ссылкой: ссылки Ozon Студия не скачивает (замер 15.11).
func (f *folderSettings) cardReference(ctx context.Context, hc *http.Client, base, cab, sku string, m mpstats.MCP, model string, fetch func(context.Context, string) ([]byte, error)) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(sku), 10, 64)
	if err != nil || id <= 0 {
		return "", errors.New("инфографика рисуется для товара Ozon с числовым SKU")
	}
	card, err := f.ozonCard(ctx, hc, base, cab, id)
	if err != nil {
		return "", err
	}
	if len(card.Photos) == 0 {
		return "", errors.New("у карточки нет фото — ИИ Студии не с чего рисовать товар")
	}
	img, err := fetch(ctx, card.Photos[0])
	if err != nil {
		return "", err
	}
	mime := http.DetectContentType(img)
	ext, ok := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[mime]
	if !ok {
		return "", fmt.Errorf("главное фото карточки — %s, ИИ Студия берет JPEG, PNG и WebP", mime)
	}
	ref, err := m.StudioUpload(ctx, model, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(img), "card"+ext)
	if err != nil {
		return "", errors.New("фото не загружено в ИИ Студию — " + mpstatsMessage(err))
	}
	return ref, nil
}

func (f *folderSettings) infographicResult(ctx context.Context, hc *http.Client, id string) (studioJobView, error) {
	m, err := f.studio(hc)
	if err != nil {
		return studioJobView{}, err
	}
	j, err := m.StudioResult(ctx, id)
	if err != nil {
		return studioJobView{}, errors.New("результат не прочитан — " + mpstatsMessage(err))
	}
	return jobView(j), nil
}

func jobView(j mpstats.StudioJob) studioJobView {
	v := studioJobView{ID: j.MessageID, Status: j.Status, Done: j.Done(), Cost: j.Cost, Images: j.Images, Fail: j.Fail}
	if v.Images == nil {
		v.Images = []string{}
	}
	return v
}

// imageHostOK — https на одном из hosts или его поддомене, без логина в адресе.
func imageHostOK(raw string, hosts []string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	h := u.Hostname()
	return slices.ContainsFunc(hosts, func(d string) bool { return h == d || strings.HasSuffix(h, "."+d) })
}

// fetchImage downloads an image from hosts, не больше limit байт; редирект — тоже только на hosts.
func fetchImage(hosts []string, limit int64) func(context.Context, string) ([]byte, error) {
	return fetchImageVia(&http.Client{Timeout: time.Minute}, hosts, limit, imageHostOK)
}

// fetchImageVia — fetchImage с клиентом и проверкой адреса (тест подставляет свои).
func fetchImageVia(hc *http.Client, hosts []string, limit int64, ok func(string, []string) bool) func(context.Context, string) ([]byte, error) {
	c := *hc
	c.CheckRedirect = func(r *http.Request, _ []*http.Request) error { // каждый адрес цепочки — с разрешенного хоста (ревью #629)
		if !ok(r.URL.String(), hosts) {
			return fmt.Errorf("картинка уводит на %s", r.URL.Hostname())
		}
		return nil
	}
	return func(ctx context.Context, u string) ([]byte, error) {
		if !ok(u, hosts) {
			return nil, fmt.Errorf("картинка не с %s", strings.Join(hosts, ", "))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, errors.New("картинка не скачана — " + err.Error())
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("картинка не скачана — ответ %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, errors.New("картинка не скачана — " + err.Error())
		}
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("картинка больше %d МБ", limit>>20)
		}
		return b, nil
	}
}

// modelText asks the chat model with system and user text (описание, задание на картинку).
func (d *chatDesk) modelText(system, user string) (descriptionDraft, error) {
	return d.modelTextFor(domain.CallCardText, system, user)
}

// modelTextFor is modelText under its own purpose in the usage log.
func (d *chatDesk) modelTextFor(purpose domain.CallPurpose, system, user string) (descriptionDraft, error) {
	cfg := d.config()
	key, err := d.modelKey(cfg)
	if err != nil {
		return descriptionDraft{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	model := d.metered(d.model(cfg, key), cfg, usage.Fixed(purpose), "")
	reply, err := model.Complete(ctx, []chat.ModelMessage{{Role: chat.RoleSystem, Content: system}, {Role: chat.RoleUser, Content: user}}, nil)
	if err != nil {
		return descriptionDraft{}, errors.New(chatError(err))
	}
	text := strings.TrimSpace(reply.Content)
	if text == "" {
		return descriptionDraft{}, errors.New("модель вернула пустой текст — попробуйте еще раз")
	}
	return descriptionDraft{Text: text, Model: cfg.Model}, nil
}
