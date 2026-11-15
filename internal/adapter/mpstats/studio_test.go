package mpstats_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/mpstats"
)

// Форма ответов ИИ Студии — из живого замера 15.11; номера и числа выдуманы.

func TestMCP_studioBalanceAndEstimate(t *testing.T) {
	var got map[string]any
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ai_studio_balance": func(map[string]any) (string, bool) {
			return `{"balance":7,"total_balance":9,"reserve":2}`, false
		},
		"ai_studio_estimate": func(a map[string]any) (string, bool) {
			got = a
			return `{"model":"nano-banana-pro","parameters":{"aspectRatio":"3:4","resolution":"2K"},"cost":10,"reference_count":1}`, false
		},
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	if b, err := m.StudioBalance(context.Background()); err != nil || b != 7 {
		t.Errorf("balance = %d, %v; want the available 7, not total 9", b, err)
	}
	cost, err := m.StudioEstimate(context.Background(), "nano-banana-pro", map[string]any{"aspectRatio": "3:4"}, []string{"ref-1"})
	if err != nil || cost != 10 {
		t.Fatalf("estimate = %d, %v", cost, err)
	}
	if got["model"] != "nano-banana-pro" || got["parameters"].(map[string]any)["aspectRatio"] != "3:4" || got["attachment_ids"].([]any)[0] != "ref-1" {
		t.Errorf("estimate args = %v", got)
	}
}

// Ошибка инструмента — ее смысл, без служебного «Error calling tool '…':».
func TestMCP_studioToolError(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ai_studio_estimate": func(map[string]any) (string, bool) {
			return "Error calling tool 'ai_studio_estimate': Недопустимое значение параметра resolution", true
		},
	})
	_, err := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}.StudioEstimate(context.Background(), "nano-banana-2", map[string]any{"resolution": "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "Недопустимое значение параметра resolution") || strings.Contains(err.Error(), "Error calling tool") {
		t.Errorf("err = %v", err)
	}
}

func TestMCP_studioUploadAndGenerate(t *testing.T) {
	var gen map[string]any
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ai_studio_upload": func(a map[string]any) (string, bool) {
			if a["model"] != "24ai_gen_image" || !strings.HasPrefix(a["image"].(string), "data:image/jpeg;base64,") || a["filename"] != "card.jpg" {
				return "wrong arguments", true
			}
			return `{"id":"ref-0001","category":"image","mimeType":"image/jpeg","size":1000,"width":100,"height":300,"path":"https://images.24ai.tech/ai-studio/a/x.jpeg"}`, false
		},
		"ai_studio_generate": func(a map[string]any) (string, bool) {
			gen = a
			return `{"chat_id":"chat-0001","message_id":"msg-0001","status":"IN_QUEUE","cost":1}`, false
		},
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	id, err := m.StudioUpload(context.Background(), "24ai_gen_image", "data:image/jpeg;base64,AAAA", "card.jpg")
	if err != nil || id != "ref-0001" {
		t.Fatalf("upload = %q, %v", id, err)
	}
	j, err := m.StudioGenerate(context.Background(), "24ai_gen_image", "инфографика", 1, map[string]any{"resolution": "900x1200"}, []string{id})
	if err != nil || j.MessageID != "msg-0001" || j.Status != "IN_QUEUE" || j.Cost != 1 || j.Done() {
		t.Fatalf("generate = %+v, %v", j, err)
	}
	if gen["max_cost_tokens"] != float64(1) || gen["prompt"] != "инфографика" || gen["attachment_ids"].([]any)[0] != id {
		t.Errorf("generate args = %v", gen)
	}
}

// Генерация платная: обрыв или 504 не повторяются — она могла состояться (описание
// ai_studio_generate: «при ошибке или таймауте не запускай повторно»).
func TestMCP_studioGenerateNotRetried(t *testing.T) {
	calls := 0
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ai_studio_generate": func(map[string]any) (string, bool) { calls++; return "HTTP504", false },
	})
	_, err := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}.StudioGenerate(context.Background(), "m", "p", 3, nil, nil)
	if err == nil || calls != 1 {
		t.Fatalf("generate after 504: %v, %d calls; want one call and an error", err, calls)
	}
	if !strings.Contains(err.Error(), "не повторяйте") {
		t.Errorf("err = %v; want a warning not to repeat a paid run", err)
	}
}

// COMPLETED — настоящий ответ; IN_QUEUE — статус из ответа запуска; отказ не виден
// живьем: его поля fail_reason/fail_code есть в настоящем ответе (null), статус FAILED — догадка,
// поэтому отказ узнается по fail_reason при любом статусе.
func TestMCP_studioResult(t *testing.T) {
	answers := map[string]string{
		"done":  `{"message_id":"done","chat_id":"c","model":"24ai_gen_image","status":"COMPLETED","attachments":[{"id":"a1","path":"https://images.24ai.tech/ai-studio/a/r1.jpeg","thumbnailPath":"https://images.24ai.tech/ai-studio/a/r1_thumbnail.webp","mimeType":"image/jpeg","size":1000,"category":"image","width":900,"height":1200,"duration":null}],"fail_reason":null,"fail_code":null}`,
		"queue": `{"message_id":"queue","chat_id":"c","model":"24ai_gen_image","status":"IN_QUEUE","attachments":[],"fail_reason":null,"fail_code":null}`,
		"fail":  `{"message_id":"fail","chat_id":"c","model":"24ai_gen_image","status":"ERROR","attachments":[],"fail_reason":"NSFW","fail_code":"moderation"}`,
	}
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ai_studio_result": func(a map[string]any) (string, bool) { return answers[a["message_id"].(string)], false },
	})
	m := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}
	j, err := m.StudioResult(context.Background(), "done")
	if err != nil || !j.Done() || len(j.Images) != 1 || j.Images[0] != "https://images.24ai.tech/ai-studio/a/r1.jpeg" {
		t.Errorf("done = %+v, %v", j, err)
	}
	if j, err := m.StudioResult(context.Background(), "queue"); err != nil || j.Done() || len(j.Images) != 0 || j.Fail != "" {
		t.Errorf("queue = %+v, %v", j, err)
	}
	if j, err := m.StudioResult(context.Background(), "fail"); err != nil || !j.Done() || j.Fail != "NSFW" {
		t.Errorf("fail = %+v, %v; want done with the reason", j, err)
	}
}

// После отправки платного вызова любой сбой, кроме отказа инструмента, — «не знаем,
// состоялась ли» (ревью #629): ответ 500, пустой ответ, обрыв по сроку.
func TestMCP_studioGenerateUnsure(t *testing.T) {
	for _, c := range []struct {
		name, text string
		isErr      bool
		unsure     bool
	}{
		{"504", "HTTP504", false, true},
		{"empty", "", false, true},
		{"not json", "oops", false, true},
		{"tool refusal", "Error calling tool 'ai_studio_generate': Недостаточно токенов", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
				"ai_studio_generate": func(map[string]any) (string, bool) { return c.text, c.isErr },
			})
			_, err := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}.StudioGenerate(context.Background(), "m", "p", 3, nil, nil)
			if err == nil || errors.Is(err, mpstats.ErrStudioUnsure) != c.unsure {
				t.Errorf("err = %v, unsure %v", err, c.unsure)
			}
		})
	}
}

// Срок вышел, пока Студия думала: тоже «не знаем», а не «не запущено».
func TestMCP_studioGenerateTimeout(t *testing.T) {
	srv := fakeMCP(t, map[string]func(map[string]any) (string, bool){
		"ai_studio_generate": func(map[string]any) (string, bool) {
			time.Sleep(300 * time.Millisecond)
			return `{"message_id":"x"}`, false
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := mpstats.MCP{URL: srv.URL, Token: "t-1", HTTP: srv.Client()}.StudioGenerate(ctx, "m", "p", 3, nil, nil)
	if !errors.Is(err, mpstats.ErrStudioUnsure) {
		t.Errorf("err = %v, want ErrStudioUnsure", err)
	}
}

// Конечный статус без причины — тоже конец: опрос не крутится впустую (ревью #629).
func TestStudioJob_done(t *testing.T) {
	for _, c := range []struct {
		j    mpstats.StudioJob
		done bool
	}{
		{mpstats.StudioJob{Status: "COMPLETED"}, true},
		{mpstats.StudioJob{Status: "FAILED"}, true},
		{mpstats.StudioJob{Status: "CANCELLED"}, true},
		{mpstats.StudioJob{Status: "IN_QUEUE"}, false},
		{mpstats.StudioJob{Status: "IN_PROGRESS"}, false},
		{mpstats.StudioJob{Status: "IN_QUEUE", Fail: "NSFW"}, true},
	} {
		if c.j.Done() != c.done {
			t.Errorf("%+v.Done() = %v", c.j, !c.done)
		}
	}
}
