package markets_test

import (
	"math"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Запросы ниши «Футболки женские» в форме ответа MPStats (замер 14.11, числа выдуманы).
var titleNiche = []markets.NicheQuery{
	{Text: "лонгслив женский", Count: 150_000, Items: 1}, // частый, но товаров ниши в выдаче почти нет
	{Text: "футболка женская", Count: 110_000, Items: 780},
	{Text: "футболка мужская", Count: 110_000, Items: 50},
	{Text: "футболка женская белая", Count: 9_000, Items: 300},
	{Text: "футболка женская хлопок", Count: 8_000, Items: 300},
	{Text: "футболка женская оверсайз", Count: 12_000, Items: 300},
	{Text: "футболка женская базовая", Count: 400, Items: 100},
	{Text: "футболки женские черные", Count: 3_000, Items: 300},
}

var titleForms = []markets.WordForms{
	{Word: "женская", Forms: []string{"женская", "женский", "женские"}},
	{Word: "футболка", Forms: []string{"футболка", "футболки"}},
	{Word: "белая", Forms: []string{"белая", "белый"}},
	{Word: "черная", Forms: []string{"черная", "черные"}},
	{Word: "базовая", Forms: []string{"базовая", "базовый"}},
}

func titleWord(ws []markets.TitleWord, w string) (markets.TitleWord, bool) {
	i := slices.IndexFunc(ws, func(x markets.TitleWord) bool { return x.Word == w })
	if i < 0 {
		return markets.TitleWord{}, false
	}
	return ws[i], true
}

// Главный запрос — частота × товаров ниши в выдаче, а не голая частота:
// «лонгслив женский» чаще, но товаров ниши по нему один (#607).
func TestCheckTitle_mainQuery(t *testing.T) {
	got := markets.CheckTitle(markets.TitleInput{Title: "Футболка женская", Queries: titleNiche, Forms: titleForms})
	if got.Main.Text != "футболка женская" {
		t.Fatalf("main %q, want «футболка женская»", got.Main.Text)
	}
	if got.Total != len(titleNiche) || got.Related != 6 { // со всеми словами главного: без «мужской» и «лонгслива»
		t.Errorf("total %d related %d, want %d and 6", got.Total, got.Related, len(titleNiche))
	}
}

func TestCheckTitle_words(t *testing.T) {
	cases := []struct {
		name, title string
		brands      []string
		own         []markets.OwnQuery
		add         []string // по порядку
		notAdd      []string
		remove      []string
		why         map[string]markets.TitleWhy
	}{
		{
			name: "слово главного запроса — первым в «добавить»", title: "Футболка оверсайз 46",
			add: []string{"женская", "белая", "хлопок"}, why: map[string]markets.TitleWhy{"женская": markets.WhyMain, "оверсайз": markets.WhyKeep},
		},
		{
			name: "цвет уже есть — другие цвета не предлагаются и сам цвет не убирается", title: "Футболка женская сиреневая",
			add: []string{"оверсайз", "хлопок"}, notAdd: []string{"белая", "черная"},
			why: map[string]markets.TitleWhy{"сиреневая": markets.WhyAttribute},
		},
		{
			name: "редкое слово — в «убрать», бренд продавца остается", title: "Футболка женская базовая PRIMER",
			brands: []string{"PRIMER"}, remove: []string{"базовая"},
			why: map[string]markets.TitleWhy{"базовая": markets.WhyRare, "primer": markets.WhyBrand},
		},
		{
			name: "по слову товар уже находят — остается", title: "Футболка женская базовая",
			own:    []markets.OwnQuery{{Text: "базовый гардероб", Count: 47, Position: 9}},
			remove: []string{}, why: map[string]markets.TitleWhy{"базовая": markets.WhyOwn},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := markets.CheckTitle(markets.TitleInput{Title: c.title, Brands: c.brands, Queries: titleNiche, Forms: titleForms, Own: c.own})
			var add []string
			for _, w := range got.Add {
				add = append(add, w.Word)
			}
			if len(add) < len(c.add) || !slices.Equal(add[:len(c.add)], c.add) {
				t.Errorf("add %v, want it to start with %v", add, c.add)
			}
			for _, w := range c.notAdd {
				if slices.Contains(add, w) {
					t.Errorf("add %v has %q", add, w)
				}
			}
			if c.remove != nil {
				var rm []string
				for _, w := range got.Remove {
					rm = append(rm, w.Word)
				}
				if !slices.Equal(rm, c.remove) && (len(rm) != 0 || len(c.remove) != 0) {
					t.Errorf("remove %v, want %v", rm, c.remove)
				}
			}
			for w, why := range c.why {
				tw, ok := titleWord(got.Words, w)
				if why == markets.WhyMain {
					tw, ok = titleWord(got.Add, w)
				}
				if !ok || tw.Why != why {
					t.Errorf("%q: %+v (found %v), want why %v", w, tw, ok, why)
				}
			}
			if _, ok := titleWord(got.Words, "46"); ok {
				t.Error("a number of the title is judged")
			}
		})
	}
}

// Доля слова — от спроса запросов со всеми словами главного; у «по слову
// находят» — сам запрос товара как довод.
func TestCheckTitle_shareAndProof(t *testing.T) {
	got := markets.CheckTitle(markets.TitleInput{Title: "Футболка женская оверсайз базовая", Queries: titleNiche, Forms: titleForms,
		Own: []markets.OwnQuery{{Text: "базовый гардероб", Count: 47, Position: 9}}})
	var total float64
	for _, q := range titleNiche[1:] {
		if q.Text != "футболка мужская" {
			total += float64(q.Count * q.Items)
		}
	}
	o, _ := titleWord(got.Words, "оверсайз")
	if want := 12_000 * 300 / total; math.Abs(o.Share-want) > 1e-9 {
		t.Errorf("оверсайз share %v, want %v", o.Share, want)
	}
	b, _ := titleWord(got.Words, "базовая")
	if b.Query != "базовый гардероб" || b.Position != 9 {
		t.Errorf("базовая proof %+v, want own query with its position", b)
	}
}

// Без запросов ниши судить не по чему: ни главного, ни советов.
func TestCheckTitle_noQueries(t *testing.T) {
	got := markets.CheckTitle(markets.TitleInput{Title: "Футболка женская"})
	if got.Main.Text != "" || len(got.Add) != 0 || len(got.Remove) != 0 || got.Total != 0 {
		t.Errorf("%+v, want nothing to judge by", got)
	}
}
