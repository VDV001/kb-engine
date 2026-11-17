package markets

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrModelRich is returned when the model's answer is not rich-content variants.
var ErrModelRich = errors.New("модель не дала вариантов рич-контента")

const (
	// MaxRichText — длина текста блока: под картинкой на телефоне больше не читают.
	MaxRichText = 300
	// maxRichTitle — заголовок в две строки, дальше Ozon обрезает.
	maxRichTitle = 60
	// maxRichBlocks — блоков в варианте: billboard — картинка во всю ширину.
	maxRichBlocks = 6
	// maxRichVariants — столько вариантов просит задание и показывает окно.
	maxRichVariants = 3
)

// RichInput — что модель знает о товаре для рич-контента (#671).
type RichInput struct {
	Title, Description string
	Praise, Complaints []string // выводы из отзывов (#612), если есть
	Photos             int      // фото карточки: блок ссылается на них номером
}

// RichBlock — блок варианта: заголовок, текст и номер фото карточки (1..).
type RichBlock struct {
	Title, Text string
	Photo       int
}

// RichVariant — вариант рич-контента целиком.
type RichVariant struct {
	Name   string
	Blocks []RichBlock
	// PhotosFixed — модель назвала фото, которого у карточки нет: окно взяло другое.
	PhotosFixed bool
}

// RichPhoto — фото карточки с размером: Ozon просит widthMobile и heightMobile.
type RichPhoto struct {
	URL           string
	Width, Height int
}

// RichSystem is the model's role for rich content.
const RichSystem = "Ты редактор карточек товаров Ozon. Пиши по данным карточки и отзывов, не выдумывай свойств товара. Отвечай только JSON без пояснений вокруг."

// RichPrompt builds the task: три варианта, у каждого блоки с заголовком,
// текстом и номером фото карточки.
func RichPrompt(in RichInput) string {
	var b strings.Builder
	b.WriteString("Предложи 3 варианта рич-контента карточки товара Ozon: в каждом 3-5 блоков «фото + заголовок + текст», по одному свойству товара в блоке. Опирайся только на данные ниже, не выдумывай.\n")
	fmt.Fprintf(&b, "Заголовок — до %d знаков, текст — до %d. Фото — номер фото карточки 1..%d.\n", maxRichTitle, MaxRichText, in.Photos)
	b.WriteString("Жалобы покупателей снимай честно: что учесть при выборе (например, размер), без обещаний, которых нет в карточке.\n\n")
	fmt.Fprintf(&b, "Название: %s\n", strings.TrimSpace(in.Title))
	if d := strings.TrimSpace(in.Description); d != "" {
		fmt.Fprintf(&b, "Описание: %s\n", clipRunes(strings.Join(strings.Fields(d), " "), 2000))
	}
	if len(in.Praise) > 0 {
		fmt.Fprintf(&b, "Покупатели хвалят: %s\n", strings.Join(in.Praise, "; "))
	}
	if len(in.Complaints) > 0 {
		fmt.Fprintf(&b, "Покупатели жалуются: %s\n", strings.Join(in.Complaints, "; "))
	}
	b.WriteString("\nОтвет — JSON: " + `{"variants":[{"name":"короткое имя варианта","blocks":[{"title":"заголовок","text":"текст","photo":номер}]}]}`)
	return b.String()
}

type richBlockJSON struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Photo int    `json:"photo"`
}

// ParseRichVariants reads the model's variants for a card with photos фото:
// блок без заголовка выпадает, фото вне 1..photos — первое, длины обрезаны.
func ParseRichVariants(text string, photos int) ([]RichVariant, error) {
	for i := strings.Index(text, "{"); i >= 0; {
		var raw struct {
			Variants []struct {
				Name   string          `json:"name"`
				Blocks []richBlockJSON `json:"blocks"`
			} `json:"variants"`
		}
		if json.NewDecoder(strings.NewReader(text[i:])).Decode(&raw) == nil && raw.Variants != nil {
			var out []RichVariant
			for _, v := range raw.Variants {
				if rv := richVariant(v.Name, v.Blocks, photos); len(rv.Blocks) > 0 && len(out) < maxRichVariants {
					out = append(out, rv)
				}
			}
			if len(out) == 0 {
				return nil, fmt.Errorf("%w: варианты без блоков", ErrModelRich)
			}
			return out, nil
		}
		next := strings.Index(text[i+1:], "{")
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return nil, fmt.Errorf("%w: в ответе нет JSON с variants", ErrModelRich)
}

// richVariant keeps the blocks with a title; фото вне 1..photos — первое еще
// не занятое в варианте (ревью #676: не одно главное фото во всех блоках).
func richVariant(name string, bs []richBlockJSON, photos int) RichVariant {
	v := RichVariant{Name: clipRunes(strings.TrimSpace(name), maxRichTitle)}
	used := map[int]bool{}
	for _, b := range bs {
		t := clipRunes(strings.TrimSpace(b.Title), maxRichTitle)
		if t == "" || len(v.Blocks) == maxRichBlocks {
			continue
		}
		p := b.Photo
		if p < 1 || p > photos {
			p, v.PhotosFixed = freePhoto(used, photos), true
		}
		used[p] = true
		v.Blocks = append(v.Blocks, RichBlock{Title: t, Text: clipRunes(strings.TrimSpace(b.Text), MaxRichText), Photo: p})
	}
	return v
}

// freePhoto is the first photo not used yet; все заняты — первое.
func freePhoto(used map[int]bool, photos int) int {
	for i := 1; i <= photos; i++ {
		if !used[i] {
			return i
		}
	}
	return 1
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// richItems is title or text of a block as Ozon stores it: items, size, align, color.
type richItems struct {
	Items []richItem `json:"items"`
	Size  string     `json:"size"`
	Align string     `json:"align"`
	Color string     `json:"color"`
}

type richItem struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
}

type richImg struct {
	Src              string `json:"src"`
	SrcMobile        string `json:"srcMobile"`
	Alt              string `json:"alt"`
	Position         string `json:"position"`
	PositionMobile   string `json:"positionMobile"`
	WidthMobile      int    `json:"widthMobile"`
	HeightMobile     int    `json:"heightMobile"`
	IsParandja       bool   `json:"isParandja"`
	IsParandjaMobile bool   `json:"isParandjaMobile"`
}

type richBlockOut struct {
	ImgLink string    `json:"imgLink"`
	Img     richImg   `json:"img"`
	Title   richItems `json:"title"`
	Text    richItems `json:"text"`
}

// RichJSON builds the variant as Ozon's «Rich-контент JSON» (атрибут 11254):
// форма — как у принятого рич-контента живых карточек (замер 17.11). Фото —
// карточки, с их размером; без размера — отказ: Ozon требует widthMobile и heightMobile.
func RichJSON(v RichVariant, photos []RichPhoto) ([]byte, error) {
	blocks := make([]richBlockOut, 0, len(v.Blocks))
	for _, b := range v.Blocks {
		if b.Photo < 1 || b.Photo > len(photos) {
			return nil, fmt.Errorf("блок «%s»: фото %d из %d", b.Title, b.Photo, len(photos))
		}
		p := photos[b.Photo-1]
		if p.Width <= 0 || p.Height <= 0 {
			return nil, fmt.Errorf("фото %d: размер не прочитан", b.Photo)
		}
		blocks = append(blocks, richBlockOut{
			Img:   richImg{Src: p.URL, SrcMobile: p.URL, Alt: b.Title, Position: "width_full", PositionMobile: "width_full", WidthMobile: p.Width, HeightMobile: p.Height},
			Title: richItems{Items: []richItem{{Type: "text", Content: b.Title}}, Size: "size4", Align: "left", Color: "color1"},
			Text:  richItems{Items: textItems(b.Text), Size: "size2", Align: "left", Color: "color1"},
		})
	}
	doc := map[string]any{
		"content": []map[string]any{{"widgetName": "raShowcase", "type": "billboard", "blocks": blocks}},
		"version": 0.3,
	}
	return json.MarshalIndent(doc, "", "  ")
}

// textItems is a block's text; пустой — «br», как у живых карточек (замер 17.11).
func textItems(text string) []richItem {
	if text == "" {
		return []richItem{{Type: "br"}}
	}
	return []richItem{{Type: "text", Content: text}}
}
