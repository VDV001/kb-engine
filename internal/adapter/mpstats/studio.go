package mpstats

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// ИИ Студия MPStats (#609): картинки по заданию и фото-референсу. Генерация платная
// (токены Студии, замер 15.11: Nano Banana Pro — 10, Seedream 5 Lite — 3), остальное бесплатно.

// StudioModel — модель Студии для инфографики карточки Ozon (3:4) и ее параметры.
type StudioModel struct {
	Slug, Name string
	Params     map[string]any
}

// StudioModels — модели на выбор в окне (решение владельца 15.11): первая — по умолчанию.
// Параметры — формат карточки Ozon 3:4, из ai_studio_models (замер 15.11).
var StudioModels = []StudioModel{
	{Slug: "nano-banana-pro", Name: "Nano Banana Pro", Params: map[string]any{"aspectRatio": "3:4", "resolution": "2K"}},
	{Slug: "nano-banana-2", Name: "Nano Banana 2", Params: map[string]any{"aspectRatio": "3:4", "resolution": "1K"}},
	{Slug: "seedream-5-lite", Name: "Seedream 5 Lite", Params: map[string]any{"resolution": "1728x2304"}},
}

// StudioJob — запущенная генерация: статус, картинки или причина отказа.
type StudioJob struct {
	MessageID, ChatID, Status string
	Cost                      int
	Images                    []string
	Fail                      string
}

// Done — генерация закончилась: картинками или отказом.
func (j StudioJob) Done() bool { return j.Status == "COMPLETED" || j.Fail != "" }

// ErrStudioUnsure — запуск генерации оборвался: она могла состояться и списать токены.
var ErrStudioUnsure = errors.New("ответ ИИ Студии не пришел, генерация могла состояться — не повторяйте, посмотрите историю в ИИ Студии MPStats")

// StudioBalance is the available balance of AI Studio tokens (без зарезервированных).
func (m MCP) StudioBalance(ctx context.Context) (int, error) {
	var raw struct {
		Balance int `json:"balance"`
	}
	err := m.call(ctx, "ai_studio_balance", map[string]any{}, &raw)
	return raw.Balance, studioErr(err)
}

// StudioEstimate is the cost of a generation in tokens, бесплатно.
func (m MCP) StudioEstimate(ctx context.Context, model string, params map[string]any, refs []string) (int, error) {
	var raw struct {
		Cost int `json:"cost"`
	}
	err := m.call(ctx, "ai_studio_estimate", studioArgs(model, params, refs), &raw)
	return raw.Cost, studioErr(err)
}

// StudioUpload uploads a reference image (data:image/…;base64,…) for model and returns its id.
// Ссылку на фото Ozon Студия не скачивает (замер 15.11: «download failed»), поэтому — файлом.
func (m MCP) StudioUpload(ctx context.Context, model, dataURI, name string) (string, error) {
	var raw struct {
		ID string `json:"id"`
	}
	if err := m.call(ctx, "ai_studio_upload", map[string]any{"model": model, "image": dataURI, "filename": name}, &raw); err != nil {
		return "", studioErr(err)
	}
	if raw.ID == "" {
		return "", errors.New("ИИ Студия не приняла фото — нет номера загрузки")
	}
	return raw.ID, nil
}

// StudioGenerate starts a paid generation not dearer than maxCost tokens. Один вызов
// без повторов: обрыв — ErrStudioUnsure, а не новая генерация.
func (m MCP) StudioGenerate(ctx context.Context, model, prompt string, maxCost int, params map[string]any, refs []string) (StudioJob, error) {
	args := studioArgs(model, params, refs)
	args["prompt"], args["max_cost_tokens"] = prompt, maxCost
	var raw struct {
		ChatID    string `json:"chat_id"`
		MessageID string `json:"message_id"`
		Status    string `json:"status"`
		Cost      int    `json:"cost"`
	}
	if err := m.callOnce(ctx, "ai_studio_generate", args, &raw); err != nil {
		if errors.Is(err, ErrDropped) {
			return StudioJob{}, ErrStudioUnsure
		}
		return StudioJob{}, studioErr(err)
	}
	return StudioJob{MessageID: raw.MessageID, ChatID: raw.ChatID, Status: raw.Status, Cost: raw.Cost}, nil
}

// StudioResult reads the status and images of a started generation.
func (m MCP) StudioResult(ctx context.Context, messageID string) (StudioJob, error) {
	var raw struct {
		MessageID   string `json:"message_id"`
		ChatID      string `json:"chat_id"`
		Status      string `json:"status"`
		Attachments []struct {
			Path     string `json:"path"`
			Category string `json:"category"`
		} `json:"attachments"`
		FailReason *string `json:"fail_reason"`
		FailCode   *string `json:"fail_code"`
	}
	if err := m.call(ctx, "ai_studio_result", map[string]any{"message_id": messageID}, &raw); err != nil {
		return StudioJob{}, studioErr(err)
	}
	j := StudioJob{MessageID: raw.MessageID, ChatID: raw.ChatID, Status: raw.Status}
	for _, a := range raw.Attachments {
		if a.Path != "" && (a.Category == "" || a.Category == "image") {
			j.Images = append(j.Images, a.Path)
		}
	}
	switch { // отказ живьем не видели: узнаем по fail_reason при любом статусе
	case raw.FailReason != nil && *raw.FailReason != "":
		j.Fail = *raw.FailReason
	case raw.FailCode != nil && *raw.FailCode != "":
		j.Fail = *raw.FailCode
	}
	return j, nil
}

func studioArgs(model string, params map[string]any, refs []string) map[string]any {
	args := map[string]any{"model": model}
	if len(params) > 0 {
		args["parameters"] = params
	}
	if len(refs) > 0 {
		args["attachment_ids"] = refs
	}
	return args
}

// toolPrefix — служебное начало ошибки инструмента MCP.
var toolPrefix = regexp.MustCompile(`Error calling tool '[^']*':\s*`)

// studioErr keeps the meaning of a tool error without its service prefix.
func studioErr(err error) error {
	if err == nil {
		return nil
	}
	if msg := toolPrefix.ReplaceAllString(err.Error(), ""); msg != err.Error() {
		return fmt.Errorf("%s", msg)
	}
	return err
}
