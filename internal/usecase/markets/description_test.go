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
		Description: "Подойдет для девочек и девушек. Мужская модель тоже есть.", Queries: titleNiche, Forms: titleForms})
	if len(got.Mismatch) != 1 || got.Mismatch[0].Found != "мужская" {
		t.Errorf("mismatch %+v, want only мужская", got.Mismatch)
	}
}

// Ревью #626: формы своего цвета («белой» при «белая») и оттенки («темное
// время») — не расхождение; отрицание («не мужская») — тоже; пол из
// характеристики «Мальчики» узнается, и девочки с мальчиками спорят, хотя оба детские.
func TestCheckDescription_review(t *testing.T) {
	cases := []struct {
		name, title, gender, color, desc string
		want                             []string
	}{
		{"формы своего цвета", "Футболка женская белая", "Женский", "белый", "Белая футболка, белой нитью, для светлых вечеров и темного времени.", nil},
		{"отрицание", "Футболка женская", "Женский", "", "Не мужская модель, не черная.", nil},
		{"мальчики против девочек", "Футболка детская", "Мальчики", "", "Футболка для девочек.", []string{"девочек"}},
		{"женщин против мальчиков", "Футболка", "Мальчики", "", "Удобна для женщин.", []string{"женщин"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := markets.CheckDescription(markets.DescriptionInput{Title: c.title, Gender: c.gender, Color: c.color, Description: c.desc})
			var found []string
			for _, m := range got.Mismatch {
				found = append(found, m.Found)
			}
			if !slices.Equal(found, c.want) {
				t.Errorf("mismatch %v, want %v", found, c.want)
			}
		})
	}
}

// Ревью #627: оттенки («темные») остаются цветом для «Названия» — их
// исключает только сверка описания; подпись расхождения — без пустых мест.
func TestDescription_shadesAndLabel(t *testing.T) {
	tc := markets.CheckTitle(markets.TitleInput{Title: "Футболка женская темные", Queries: titleNiche, Forms: titleForms})
	for _, w := range tc.Words {
		if w.Word == "темные" && w.Why != markets.WhyAttribute {
			t.Errorf("темные in the title: %+v, want an attribute", w)
		}
	}
	got := markets.CheckDescription(markets.DescriptionInput{Title: "Костюм спортивный женский", Gender: "Женский",
		Description: "Мужской костюм для мальчиков."})
	for _, m := range got.Mismatch {
		if m.Card != "женский" {
			t.Errorf("label %q, want «женский» once", m.Card)
		}
	}
	if len(got.Mismatch) != 2 {
		t.Errorf("mismatch %+v, want мужской and мальчиков", got.Mismatch)
	}
}
