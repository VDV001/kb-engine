package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOzonRich is a cabinet whose card has two photos on the same server:
// 30×40 и 50×50 пикселей.
func fakeOzonRich(t *testing.T) *httptest.Server {
	t.Helper()
	pngOf := func(w, h int) []byte {
		var b bytes.Buffer
		_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
		return b.Bytes()
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/img/a.png":
			_, _ = w.Write(pngOf(30, 40))
		case "/img/b.png":
			_, _ = w.Write(pngOf(50, 50))
		case "/v4/product/info/attributes":
			if r.Header.Get("Api-Key") != "k-1" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"result":[{"name":"Футболка женская","primary_image":"` + srv.URL + `/img/a.png","images":["` + srv.URL + `/img/b.png"],` +
				`"attributes":[{"id":4191,"values":[{"value":"Хлопок 100%"}]}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRichVariantsView(t *testing.T) {
	s, _, _ := newServerSettings(t)
	oz := fakeOzonRich(t)
	row, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	cd, m := localChat(t)
	ctx := context.Background()
	// В тесте фото — на http-сервере теста: пускаем его, как в проде пускают ozone.ru.
	fetch := fetchImageVia(oz.Client(), []string{"127.0.0.1"}, maxStudioImage, func(u string, _ []string) bool { return strings.HasPrefix(u, oz.URL) })
	if _, err := s.richVariants(ctx, oz.Client(), cd, oz.URL, row.ID, "1234567", nil, nil, false, fetch); err == nil || m.n != 0 {
		t.Fatalf("без согласия: %v, вызовов %d", err, m.n)
	}
	m.reply = `{"variants":[{"name":"Про ткань","blocks":[{"title":"Плотный хлопок","text":"Не садится","photo":2},{"title":"Крой","text":"Свободный","photo":1}]}]}`
	v, err := s.richVariants(ctx, oz.Client(), cd, oz.URL, row.ID, "1234567", []string{"плотная ткань"}, []string{"маломерит"}, true, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Variants) != 1 || len(v.Variants[0].Blocks) != 2 || v.Variants[0].Blocks[0].Photo != 2 || v.Model == "" {
		t.Fatalf("%+v", v)
	}
	var doc struct {
		Content []struct {
			Blocks []struct {
				Img struct{ WidthMobile, HeightMobile int } `json:"img"`
			} `json:"blocks"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(v.Variants[0].JSON), &doc); err != nil {
		t.Fatal(err)
	}
	if b := doc.Content[0].Blocks; b[0].Img.WidthMobile != 50 || b[1].Img.HeightMobile != 40 {
		t.Errorf("sizes %+v — размеры настоящих фото", b)
	}
	var prompt strings.Builder
	for _, msg := range m.asked[0] {
		prompt.WriteString(msg.Content)
	}
	if !strings.Contains(prompt.String(), "Хлопок 100%") || !strings.Contains(prompt.String(), "маломерит") || !strings.Contains(prompt.String(), "1..2") {
		t.Errorf("prompt:\n%s", prompt.String())
	}
}

func TestRichSaveRefusesBrokenJSON(t *testing.T) {
	if err := checkRichJSON(`{"content":[],"version":0.3}`); err != nil {
		t.Errorf("good: %v", err)
	}
	for _, bad := range []string{"", "{", `{"version":0.3}`, `[1]`} {
		if checkRichJSON(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Ревью #676: фото — только с ozone.ru по https, как у инфографики (#629).
func TestRichPhotosOnlyFromOzon(t *testing.T) {
	s, _, _ := newServerSettings(t)
	oz := fakeOzonRich(t)
	row, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	cd, m := localChat(t)
	m.reply = `{"variants":[{"name":"в","blocks":[{"title":"т","text":"х","photo":1}]}]}`
	_, err = s.richVariants(context.Background(), oz.Client(), cd, oz.URL, row.ID, "1234567", nil, nil, true, fetchImage(ozonImageHosts, maxStudioImage))
	if err == nil || !strings.Contains(err.Error(), "ozone.ru") {
		t.Errorf("photo from a test host: %v", err)
	}
}

func TestRichFileName(t *testing.T) {
	if got := richFileName("123", `Ткань/крой: "главное"`); strings.ContainsAny(got, `/:"\`) || !strings.HasSuffix(got, ".json") || !strings.Contains(got, "123") {
		t.Errorf("%q", got)
	}
}
