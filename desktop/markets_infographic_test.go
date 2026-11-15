package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// studioFake — MCP MPStats с ИИ Студией: ответы — по живому замеру 15.11; calls — что вызвано.
func studioFake(t *testing.T, balance, cost int) (s *folderSettings, ozURL, cab string, calls *[]string, args map[string]map[string]any) {
	t.Helper()
	s, _, _ = newServerSettings(t)
	calls, args = &[]string{}, map[string]map[string]any{}
	var mu sync.Mutex // цены моделей спрашиваются сразу
	mp := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, name)
		args[name] = a
		switch name {
		case "ai_studio_balance":
			return `{"balance":` + strconv.Itoa(balance) + `,"total_balance":20,"reserve":0}`, false
		case "ai_studio_estimate":
			return `{"model":"x","parameters":{},"cost":` + strconv.Itoa(cost) + `,"reference_count":1}`, false
		case "ai_studio_upload":
			return `{"id":"ref-1","category":"image","mimeType":"image/jpeg","size":4}`, false
		case "ai_studio_generate":
			return `{"chat_id":"c-1","message_id":"m-1","status":"IN_QUEUE","cost":` + strconv.Itoa(cost) + `}`, false
		case "ai_studio_result":
			return `{"message_id":"m-1","chat_id":"c-1","status":"COMPLETED","attachments":[{"path":"https://images.24ai.tech/ai-studio/a/m-1.jpeg","category":"image"}],"fail_reason":null,"fail_code":null}`, false
		}
		return "unknown", true
	})
	s.mpstatsURL, s.mcpURL = mp.URL, mp.URL+"/mcp"
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: mp.URL, HTTP: mp.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	oz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":[{"name":"Футболка женская","primary_image":"https://ir.ozone.ru/p.jpg","images":[],"attributes":[]}]}`))
	}))
	t.Cleanup(oz.Close)
	row, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	return s, oz.URL, row.ID, calls, args
}

// jpeg — начало настоящего JPEG: http.DetectContentType узнает image/jpeg.
var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}

func fakeFetch(seen *string) func(context.Context, string) ([]byte, error) {
	return func(_ context.Context, u string) ([]byte, error) { *seen = u; return jpeg, nil }
}

// Окно показывает модели с ценой и баланс до генерации; все вызовы бесплатные.
func TestInfographicOffer(t *testing.T) {
	s, _, _, calls, _ := studioFake(t, 19, 10)
	o, err := s.infographicOffer(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Balance != 19 || len(o.Models) != 3 || o.Models[0].Slug != "nano-banana-pro" || o.Models[0].Cost != 10 {
		t.Fatalf("offer %+v", o)
	}
	for _, c := range *calls {
		if c == "ai_studio_generate" || c == "ai_studio_upload" {
			t.Errorf("offer called %s", c)
		}
	}
}

// Генерация: главное фото карточки — референсом файлом, цена — с референсом,
// предел — та цена, на которую согласился человек.
func TestInfographicGenerate(t *testing.T) {
	s, oz, cab, calls, args := studioFake(t, 19, 10)
	var seen string
	j, err := s.infographicGenerate(context.Background(), nil, oz, cab, "1234567", "nano-banana-pro", "  нарисуй  ", 10, fakeFetch(&seen))
	if err != nil {
		t.Fatal(err)
	}
	if j.ID != "m-1" || j.Done {
		t.Errorf("job %+v", j)
	}
	if seen != "https://ir.ozone.ru/p.jpg" {
		t.Errorf("fetched %q, want the main photo of the card", seen)
	}
	if img, _ := args["ai_studio_upload"]["image"].(string); !strings.HasPrefix(img, "data:image/jpeg;base64,") {
		t.Errorf("upload image %q", img)
	}
	g := args["ai_studio_generate"]
	if g["max_cost_tokens"] != float64(10) || g["prompt"] != "нарисуй" || g["model"] != "nano-banana-pro" || g["attachment_ids"].([]any)[0] != "ref-1" {
		t.Errorf("generate args %v", g)
	}
	if strings.Join(*calls, ",") != "ai_studio_upload,ai_studio_estimate,ai_studio_balance,ai_studio_generate" {
		t.Errorf("calls %v", *calls)
	}
}

// Без трат: подорожало, токенов не хватает, модель не из списка, пустое задание.
func TestInfographicGenerate_refusals(t *testing.T) {
	for _, c := range []struct {
		name          string
		balance, cost int
		model, prompt string
		maxCost       int
		want          string
	}{
		{"no tokens", 2, 3, "seedream-5-lite", "p", 3, "токенов"},
		{"unknown model", 19, 1, "24ai_gen_image", "p", 1, "модель"},
		{"empty prompt", 19, 1, "nano-banana-2", "  ", 5, "задани"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, oz, cab, calls, _ := studioFake(t, c.balance, c.cost)
			var seen string
			_, err := s.infographicGenerate(context.Background(), nil, oz, cab, "1234567", c.model, c.prompt, c.maxCost, fakeFetch(&seen))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
			for _, name := range *calls {
				if name == "ai_studio_generate" {
					t.Error("generation started")
				}
			}
		})
	}
}

func TestInfographicResult(t *testing.T) {
	s, _, _, _, _ := studioFake(t, 19, 1)
	j, err := s.infographicResult(context.Background(), nil, "m-1")
	if err != nil || !j.Done || len(j.Images) != 1 {
		t.Fatalf("result %+v, %v", j, err)
	}
}

// Картинки окно скачивает только с CDN Ozon (фото товара) и ИИ Студии (результат), по https.
func TestStudioImageHost(t *testing.T) {
	for _, c := range []struct {
		url   string
		hosts []string
		ok    bool
	}{
		{"https://ir.ozone.ru/s3/multimedia-1/1.jpg", ozonImageHosts, true},
		{"https://cdn1.ozone.ru/multimedia/1.jpg", ozonImageHosts, true},
		{"http://ir.ozone.ru/1.jpg", ozonImageHosts, false},
		{"https://ozone.ru.evil.com/1.jpg", ozonImageHosts, false},
		{"https://evilozone.ru/1.jpg", ozonImageHosts, false},
		{"https://images.24ai.tech/ai-studio/a/1.jpeg", studioImageHosts, true},
		{"https://ir.ozone.ru/1.jpg", studioImageHosts, false},
		{"https://user:p@images.24ai.tech/1.jpeg", studioImageHosts, false},
	} {
		if got := imageHostOK(c.url, c.hosts); got != c.ok {
			t.Errorf("imageHostOK(%q) = %v, want %v", c.url, got, c.ok)
		}
	}
}

// С фото цена выше показанной: без трат, окно узнает новую цену и показывает ее
// на кнопке — следующий клик сходится (ревью #629: иначе модель нельзя запустить вовсе).
func TestInfographicGenerate_repriced(t *testing.T) {
	s, oz, cab, calls, _ := studioFake(t, 19, 12)
	var seen string
	j, err := s.infographicGenerate(context.Background(), nil, oz, cab, "1234567", "nano-banana-pro", "p", 10, fakeFetch(&seen))
	if err != nil || j.Repriced != 12 || j.ID != "" {
		t.Fatalf("job %+v, %v; want repriced 12 without a run", j, err)
	}
	for _, name := range *calls {
		if name == "ai_studio_generate" {
			t.Error("generation started")
		}
	}
}

// Редирект с разрешенного адреса на чужой — отказ, а не скачивание (ревью #629).
func TestFetchImage_redirect(t *testing.T) {
	evil := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(jpeg) }))
	t.Cleanup(evil.Close)
	good := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok.jpg" {
			_, _ = w.Write(jpeg)
			return
		}
		http.Redirect(w, r, evil.URL+"/x.jpg", http.StatusFound)
	}))
	t.Cleanup(good.Close)
	host := strings.TrimPrefix(good.URL, "https://")
	host = host[:strings.LastIndex(host, ":")]
	fetch := fetchImageVia(good.Client(), []string{host}, 1<<20, func(u string, hosts []string) bool {
		return strings.HasPrefix(u, good.URL) // в тесте оба сервера на 127.0.0.1: «свой» — по порту
	})
	if _, err := fetch(context.Background(), good.URL+"/ok.jpg"); err != nil {
		t.Fatalf("allowed: %v", err)
	}
	if _, err := fetch(context.Background(), good.URL+"/go"); err == nil {
		t.Error("redirect to another host followed")
	}
}
