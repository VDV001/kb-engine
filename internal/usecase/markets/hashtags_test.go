package markets_test

import (
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func hashtagRow(rs []markets.HashtagRow, tag string) (markets.HashtagRow, bool) {
	i := slices.IndexFunc(rs, func(r markets.HashtagRow) bool { return r.Tag == tag })
	if i < 0 {
		return markets.HashtagRow{}, false
	}
	return rs[i], true
}

// Хештеги карточки (#610): у каждого спрос по запросам ниши и причина, если
// убрать; правила Ozon — «#», буквы и цифры, «_» между словами, до 30 символов,
// не больше 30 штук, без брендов.
func TestCheckHashtags(t *testing.T) {
	niche := append([]markets.NicheQuery{
		{Text: "футболка женская оверсайз", Count: 12_000, Items: 300},
		{Text: "футболка мужская", Count: 110_000, Items: 50},
	}, titleNiche...)
	got := markets.CheckHashtags(markets.HashtagInput{
		Title:    "Футболка женская сиреневая",
		Hashtags: "#футболка_женская #женская_футболка #футболка_мужская #футболка-хлопок #zara_футболка #футболка_женская_с_очень_длинным_хвостом #лавандовая_футболка #футболка_женская_оверсайз",
		Brands:   []string{"PRIMER"}, Queries: niche, Forms: titleForms, NicheBrands: []string{"Zara"},
		Own: []markets.OwnQuery{{Text: "лавандовая футболка", Count: 50, Position: 8}},
	})
	cases := []struct {
		tag string
		why markets.HashtagWhy
	}{
		{"#футболка_женская", markets.TagOK},
		{"#женская_футболка", markets.TagDuplicate},
		{"#футболка_мужская", markets.TagOffTopic},
		{"#футболка-хлопок", markets.TagFormat},
		{"#zara_футболка", markets.TagBrand},
		{"#футболка_женская_с_очень_длинным_хвостом", markets.TagLong},
		{"#лавандовая_футболка", markets.TagOwn},
		{"#футболка_женская_оверсайз", markets.TagOK},
	}
	for _, c := range cases {
		r, ok := hashtagRow(got.Tags, c.tag)
		if !ok || r.Why != c.why {
			t.Errorf("%s: %+v (found %v), want why %v", c.tag, r, ok, c.why)
		}
	}
	if r, _ := hashtagRow(got.Tags, "#футболка_женская"); r.Demand <= 110_000 {
		t.Errorf("#футболка_женская demand %d: sum of queries with both words", r.Demand)
	}
	if r, _ := hashtagRow(got.Tags, "#лавандовая_футболка"); r.Query != "лавандовая футболка" || r.Position != 8 {
		t.Errorf("own proof %+v", r)
	}
	if len(got.Remove) != 5 {
		t.Errorf("remove %d: %+v, want format, long, duplicate, off topic, brand", len(got.Remove), got.Remove)
	}
	var add []string
	for _, r := range got.Add {
		add = append(add, r.Tag)
	}
	if !slices.Contains(add, "#футболка_женская_белая") || !slices.Contains(add, "#футболка_женская_хлопок") || slices.Contains(add, "#футболка_мужская") {
		t.Errorf("add %v: frequent niche queries not yet tagged, no other gender", add)
	}
}

// Больше 30 хештегов — Ozon не примет; добавлять нечего сверх предела.
func TestCheckHashtags_tooMany(t *testing.T) {
	tags := ""
	for i := range 31 {
		tags += " #тег" + string(rune('а'+i))
	}
	got := markets.CheckHashtags(markets.HashtagInput{Title: "Футболка женская", Hashtags: tags, Queries: titleNiche, Forms: titleForms})
	if !got.TooMany || len(got.Add) != 0 {
		t.Errorf("too many %v add %d, want true and nothing to add", got.TooMany, len(got.Add))
	}
}

// Пустые хештеги — все советы из ниши, без «убрать».
func TestCheckHashtags_empty(t *testing.T) {
	got := markets.CheckHashtags(markets.HashtagInput{Title: "Футболка женская", Queries: titleNiche, Forms: titleForms})
	if len(got.Tags) != 0 || len(got.Remove) != 0 || len(got.Add) == 0 || got.Main.Text != "футболка женская" {
		t.Errorf("%+v", got)
	}
}
