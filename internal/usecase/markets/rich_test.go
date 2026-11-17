package markets_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func TestRichPrompt(t *testing.T) {
	p := markets.RichPrompt(markets.RichInput{Title: "Футболка женская", Description: "Хлопок 100%", Praise: []string{"плотная ткань"}, Complaints: []string{"маломерит"}, Photos: 4})
	for _, want := range []string{"Футболка женская", "Хлопок 100%", "плотная ткань", "маломерит", "1..4", "не выдумывай"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
}

func TestParseRichVariants(t *testing.T) {
	long := strings.Repeat("я", markets.MaxRichText+50)
	text := "Вот:\n" + `{"variants":[{"name":"Про ткань","blocks":[{"title":"Плотный хлопок","text":"` + long + `","photo":2},{"title":"","text":"пусто","photo":1},{"title":"Размер","text":"Берите на размер больше","photo":9}]},` +
		`{"name":"Пустой","blocks":[]}]}`
	vs, err := markets.ParseRichVariants(text, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Name != "Про ткань" || len(vs[0].Blocks) != 2 {
		t.Fatalf("%+v — блок без заголовка выпал, вариант без блоков — тоже", vs)
	}
	b := vs[0].Blocks
	if b[0].Photo != 2 || len([]rune(b[0].Text)) > markets.MaxRichText || b[1].Photo != 1 {
		t.Errorf("blocks %+v — фото вне 1..4 — первое", b)
	}
	if _, err := markets.ParseRichVariants("нет", 4); !errors.Is(err, markets.ErrModelRich) {
		t.Errorf("not json: %v", err)
	}
}

// Форма — как у принятого Ozon рич-контента живых карточек (замер 17.11):
// raShowcase billboard, img с src/srcMobile/размерами, title и text через items.
func TestRichJSON(t *testing.T) {
	v := markets.RichVariant{Name: "в", Blocks: []markets.RichBlock{{Title: "Плотный хлопок", Text: "Не садится", Photo: 2}}}
	photos := []markets.RichPhoto{{URL: "https://cdn1.ozone.ru/a.jpg", Width: 900, Height: 1200}, {URL: "https://cdn1.ozone.ru/b.jpg", Width: 1000, Height: 1000}}
	raw, err := markets.RichJSON(v, photos)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Content []struct {
			WidgetName string `json:"widgetName"`
			Type       string `json:"type"`
			Blocks     []struct {
				ImgLink *string `json:"imgLink"`
				Img     struct {
					Src, SrcMobile, Alt, Position, PositionMobile string
					WidthMobile, HeightMobile                     int
				} `json:"img"`
				Title struct {
					Items []struct{ Type, Content string } `json:"items"`
					Size  string                           `json:"size"`
				} `json:"title"`
				Text struct {
					Items []struct{ Type, Content string } `json:"items"`
					Size  string                           `json:"size"`
				} `json:"text"`
			} `json:"blocks"`
		} `json:"content"`
		Version float64 `json:"version"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	if got.Version != 0.3 || len(got.Content) != 1 || got.Content[0].WidgetName != "raShowcase" || got.Content[0].Type != "billboard" {
		t.Fatalf("%s", raw)
	}
	bl := got.Content[0].Blocks[0]
	if bl.ImgLink == nil || bl.Img.Src != photos[1].URL || bl.Img.SrcMobile != photos[1].URL || bl.Img.WidthMobile != 1000 || bl.Img.HeightMobile != 1000 ||
		bl.Img.Position != "width_full" || bl.Img.Alt != "Плотный хлопок" {
		t.Errorf("img %+v", bl.Img)
	}
	if len(bl.Title.Items) != 1 || bl.Title.Items[0].Type != "text" || bl.Title.Items[0].Content != "Плотный хлопок" || bl.Title.Size != "size4" ||
		len(bl.Text.Items) != 1 || bl.Text.Items[0].Content != "Не садится" || bl.Text.Size != "size2" {
		t.Errorf("title %+v text %+v", bl.Title, bl.Text)
	}
	if _, err := markets.RichJSON(v, photos[:1]); err == nil {
		t.Error("photo 2 of 1 accepted")
	}
	if _, err := markets.RichJSON(markets.RichVariant{Blocks: []markets.RichBlock{{Title: "x", Photo: 1}}}, []markets.RichPhoto{{URL: "https://x/a.jpg"}}); err == nil {
		t.Error("a photo without size accepted — Ozon просит widthMobile и heightMobile")
	}
}

// Ревью #676: номер фото вне карточки — первое незанятое и пометка; не больше
// трех вариантов; пустой текст — «br», как у живых карточек.
func TestParseRichVariantsFixes(t *testing.T) {
	text := `{"variants":[{"name":"a","blocks":[{"title":"t1","text":"x","photo":1},{"title":"t2","text":"","photo":7},{"title":"t3","text":"y","photo":8}]},` +
		`{"name":"b","blocks":[{"title":"t","photo":1}]},{"name":"c","blocks":[{"title":"t","photo":1}]},{"name":"d","blocks":[{"title":"t","photo":1}]}]}`
	vs, err := markets.ParseRichVariants(text, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 {
		t.Errorf("%d variants, want 3", len(vs))
	}
	b := vs[0].Blocks
	if b[0].Photo != 1 || b[1].Photo != 2 || b[2].Photo != 3 || !vs[0].PhotosFixed || vs[1].PhotosFixed {
		t.Errorf("photos %+v fixed %v %v", b, vs[0].PhotosFixed, vs[1].PhotosFixed)
	}
	raw, err := markets.RichJSON(vs[0], []markets.RichPhoto{{URL: "https://a/1.jpg", Width: 1, Height: 1}, {URL: "https://a/2.jpg", Width: 1, Height: 1}, {URL: "https://a/3.jpg", Width: 1, Height: 1}})
	if err != nil || !strings.Contains(string(raw), `"type": "br"`) || strings.Count(string(raw), `"type": "text"`) != 5 {
		t.Errorf("empty text block: %s %v", raw, err)
	}
}
