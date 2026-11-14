package markets_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Описание карточки (#611): расхождения с названием и характеристиками —
// первыми, затем частые слова ниши, которых в описании нет.
func TestCheckDescription(t *testing.T) {
	got := markets.CheckDescription(markets.DescriptionInput{
		Title:       "Футболка мужская черная с принтом",
		Gender:      "Мужской",
		Color:       "черный",
		Description: "Женская бежевая футболка оверсайз из мягкого хлопка.",
		Queries:     append(slices.Clone(titleNiche), markets.NicheQuery{Text: "футболка мужская", Count: 200_000, Items: 900}, markets.NicheQuery{Text: "футболка мужская принт", Count: 20_000, Items: 500}),
		Forms:       titleForms,
	})
	var kinds []string
	for _, m := range got.Mismatch {
		kinds = append(kinds, m.Kind+":"+m.Found)
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{"color:бежевая", "gender:женская"}) {
		t.Errorf("mismatch %v, want the other gender and color named", kinds)
	}
	if got.Main.Text != "футболка мужская" {
		t.Errorf("main %q", got.Main.Text)
	}
	var missing []string
	for _, w := range got.Missing {
		missing = append(missing, w.Word)
	}
	if !slices.Contains(missing, "мужская") || slices.Contains(missing, "женская") {
		t.Errorf("missing %v: the main query's words not in the description, no other gender", missing)
	}
}

// Описание совпадает с товаром — расхождений нет; пустое описание названо.
func TestCheckDescription_cleanAndEmpty(t *testing.T) {
	ok := markets.CheckDescription(markets.DescriptionInput{Title: "Футболка женская сиреневая", Color: "лавандовый",
		Description: "Женская сиреневая футболка, лавандовый оттенок.", Queries: titleNiche, Forms: titleForms})
	if len(ok.Mismatch) != 0 || ok.Empty {
		t.Errorf("clean: %+v", ok)
	}
	empty := markets.CheckDescription(markets.DescriptionInput{Title: "Футболка женская", Queries: titleNiche, Forms: titleForms})
	if !empty.Empty {
		t.Error("empty description not named")
	}
}

// Задание модели (#611): название, характеристики, расхождения и слова ниши;
// модель не выдумывает свойств, которых нет в карточке.
func TestDescriptionPrompt(t *testing.T) {
	in := markets.DescriptionInput{Title: "Футболка мужская черная", Gender: "Мужской", Color: "черный", Description: "Женская бежевая футболка.", Queries: titleNiche, Forms: titleForms}
	p := markets.DescriptionPrompt(in, markets.CheckDescription(in))
	for _, want := range []string{"Футболка мужская черная", "Мужской", "черный", "Женская бежевая футболка.", "женская"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if !strings.Contains(markets.DescriptionSystem, "не выдумывай") {
		t.Error("system prompt must forbid made-up properties")
	}
}

// Живая проверка #611: «для девочек» в описании женского товара — та же
// аудитория, не расхождение; «мужская» — расхождение.
func TestCheckDescription_genderByAudience(t *testing.T) {
	got := markets.CheckDescription(markets.DescriptionInput{Title: "Футболка женская", Gender: "Женский",
		Description: "Подойдет для девочек и девушек. Не мужская модель.", Queries: titleNiche, Forms: titleForms})
	if len(got.Mismatch) != 1 || got.Mismatch[0].Found != "мужская" {
		t.Errorf("mismatch %+v, want only мужская", got.Mismatch)
	}
}
