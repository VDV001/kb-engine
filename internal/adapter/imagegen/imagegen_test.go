package imagegen_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/imagegen"
)

const pngB64 = "iVBORw0KGgoAAAANSUhEUg==" // сигнатура PNG: клиент сверяет байты с названным типом

func gateway(t *testing.T, status int, body string, seen *map[string]any, auth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		if seen != nil {
			_ = json.NewDecoder(r.Body).Decode(seen)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// Запрос — как у чата шлюза: модальности текст+картинка, ключ в заголовке,
// референсы — data URL; ответ — первая картинка choices[0].message.images (#678).
func TestGenerate(t *testing.T) {
	var seen map[string]any
	var auth string
	srv := gateway(t, 200, `{"choices":[{"message":{"images":[{"image_url":{"url":"data:image/png;base64,`+pngB64+`"}}]}}]}`, &seen, &auth)
	defer srv.Close()
	c := imagegen.Client{URL: srv.URL + "/v1", Model: "google/gemini-3.1-flash-image", Key: "k-secret", HTTP: srv.Client()}
	img, err := c.Generate(context.Background(), "инфографика", [][]byte{[]byte("ref")})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := base64.StdEncoding.DecodeString(pngB64)
	if string(img.Data) != string(want) || img.MIME != "image/png" {
		t.Errorf("image %q %q", img.Data, img.MIME)
	}
	if auth != "Bearer k-secret" {
		t.Errorf("auth %q", auth)
	}
	if seen["model"] != "google/gemini-3.1-flash-image" || len(seen["modalities"].([]any)) != 2 {
		t.Errorf("request %v", seen)
	}
	content := seen["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 || !strings.HasPrefix(content[1].(map[string]any)["image_url"].(map[string]any)["url"].(string), "data:") {
		t.Errorf("content %v — prompt then the reference as a data URL", content)
	}
}

// Отказы шлюза — словами для человека; ключ в текст не попадает.
func TestGenerateRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"no credit":  {402, `{"error":{"message":"insufficient credits"}}`, imagegen.ErrNoCredit},
		"key limit":  {403, `{"error":{"message":"key limit"}}`, imagegen.ErrKeyRefused},
		"bad key":    {401, `{}`, imagegen.ErrKeyRefused},
		"no image":   {200, `{"choices":[{"message":{"content":"только текст"}}]}`, imagegen.ErrNoImage},
		"empty":      {200, `{"choices":[]}`, imagegen.ErrNoImage},
		"not base64": {200, `{"choices":[{"message":{"images":[{"image_url":{"url":"https://evil/x.png"}}]}}]}`, imagegen.ErrNoImage},
	} {
		srv := gateway(t, c.status, c.body, nil, nil)
		cl := imagegen.Client{URL: srv.URL + "/v1", Model: "m", Key: "k-secret", HTTP: srv.Client()}
		_, err := cl.Generate(context.Background(), "x", nil)
		srv.Close()
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "k-secret") {
			t.Errorf("%s: the key is in the error", name)
		}
	}
}

// Ответ больше предела отбрасывается, а не копится в памяти.
func TestGenerateLimitsTheReply(t *testing.T) {
	srv := gateway(t, 200, `{"choices":[{"message":{"images":[{"image_url":{"url":"data:image/png;base64,`+strings.Repeat("A", 1<<20)+`"}}]}}]}`, nil, nil)
	defer srv.Close()
	c := imagegen.Client{URL: srv.URL + "/v1", Model: "m", Key: "k", HTTP: srv.Client(), MaxBytes: 1 << 16}
	if _, err := c.Generate(context.Background(), "x", nil); !errors.Is(err, imagegen.ErrTooBig) {
		t.Errorf("%v", err)
	}
}

// Цена вызова приходит от шлюза (providerMetadata.gateway.cost, строкой или
// числом); нет — пусто, а не ноль (вызов «без счета», а не бесплатный).
func TestGenerateCost(t *testing.T) {
	for name, c := range map[string]struct{ meta, want string }{
		"string": {`"providerMetadata":{"gateway":{"cost":"0.068"}},`, "0.068"},
		"number": {`"providerMetadata":{"gateway":{"cost":0.139}},`, "0.139"},
		"none":   {``, ""},
		"null":   {`"providerMetadata":{"gateway":{"cost":null}},`, ""},
	} {
		srv := gateway(t, 200, `{`+c.meta+`"choices":[{"message":{"images":[{"image_url":{"url":"data:image/png;base64,`+pngB64+`"}}]}}]}`, nil, nil)
		cl := imagegen.Client{URL: srv.URL + "/v1", Model: "m", Key: "k", HTTP: srv.Client()}
		img, err := cl.Generate(context.Background(), "x", nil)
		srv.Close()
		if err != nil || img.Cost != c.want {
			t.Errorf("%s: cost %q, %v", name, img.Cost, err)
		}
	}
}

// Шлюз не уводит ключ и данные редиректом: ни на чужой хост, ни на тот же по
// http (ревью #683, п.1).
func TestGenerateDoesNotFollowRedirects(t *testing.T) {
	hit := false
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := imagegen.Client{URL: srv.URL + "/v1", Model: "m", Key: "k-secret"} // HTTP по умолчанию
	if _, err := c.Generate(context.Background(), "x", [][]byte{[]byte("photo")}); err == nil {
		t.Error("a redirect was followed")
	}
	if hit {
		t.Error("the request reached the redirect target")
	}
}

// Цена списанного вызова читается, даже когда картинки нет (ревью #683, п.7).
func TestGenerateKeepsTheCostWithoutAnImage(t *testing.T) {
	srv := gateway(t, 200, `{"providerMetadata":{"gateway":{"cost":"0.068"}},"choices":[{"message":{"content":"только текст"}}]}`, nil, nil)
	defer srv.Close()
	c := imagegen.Client{URL: srv.URL + "/v1", Model: "m", Key: "k", HTTP: srv.Client()}
	img, err := c.Generate(context.Background(), "x", nil)
	if !errors.Is(err, imagegen.ErrNoImage) || img.Cost != "0.068" {
		t.Errorf("cost %q, %v", img.Cost, err)
	}
}

// Только png, jpeg и webp, и байты должны быть тем, чем названы: svg и html под
// видом картинки не принимаются (ревью #683, п.4).
func TestGenerateAcceptsOnlyRealRasterImages(t *testing.T) {
	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n0000"))
	for name, c := range map[string]struct {
		url string
		ok  bool
	}{
		"real png":      {png, true},
		"svg":           {"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg onload=alert(1)>")), false},
		"html as png":   {"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<html><script>1</script>")), false},
		"gif":           {"data:image/gif;base64," + base64.StdEncoding.EncodeToString([]byte("GIF89a000")), false},
		"mime mismatch": {"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n0000")), false},
	} {
		srv := gateway(t, 200, `{"choices":[{"message":{"images":[{"image_url":{"url":"`+c.url+`"}}]}}]}`, nil, nil)
		cl := imagegen.Client{URL: srv.URL + "/v1", Model: "m", Key: "k", HTTP: srv.Client()}
		_, err := cl.Generate(context.Background(), "x", nil)
		srv.Close()
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}
