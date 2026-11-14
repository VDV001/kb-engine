package markets

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

// DescriptionInput — описание карточки (аннотация Ozon) и то, с чем его
// сверяют (#611): название, характеристики «Пол» и «Цвет», запросы ниши.
type DescriptionInput struct {
	Title, Gender, Color, Description string
	Brands                            []string
	Queries                           []NicheQuery
	Forms                             []WordForms
	NicheBrands                       []string
}

// DescriptionMismatch — слово описания, которое спорит с товаром: другой пол
// или цвет, чем в названии и характеристиках.
type DescriptionMismatch struct {
	Kind  string // «gender» или «color»
	Found string // слово в описании
	Card  string // что у товара: слова названия и характеристик
}

// DescriptionCheck — расхождения первыми, затем частые слова ниши, которых в
// описании нет; Empty — описания нет совсем.
type DescriptionCheck struct {
	Main     NicheQuery
	Mismatch []DescriptionMismatch
	Missing  []TitleWord
	Empty    bool
	Length   int // символов в описании
}

// descriptionMissingMax — сколько недостающих слов называть.
const descriptionMissingMax = 10

// CheckDescription checks a card description against the card and the niche (#611).
func CheckDescription(in DescriptionInput) DescriptionCheck {
	out := DescriptionCheck{Length: len([]rune(strings.TrimSpace(in.Description)))}
	out.Empty = out.Length == 0
	card, cardWords := cardClasses(in)
	out.Mismatch = mismatches(in.Description, card, cardWords)
	main, ok := mainQuery(in.Queries)
	if !ok {
		return out
	}
	out.Main = main
	j := newTitleJudge(TitleInput{Title: in.Title, Brands: in.Brands, Queries: in.Queries, Forms: in.Forms, NicheBrands: in.NicheBrands})
	need := groupsOf(main.Text, j.group)
	j.count(in.Queries, need)
	have := map[string]bool{}
	for _, g := range groupsOf(in.Description, j.group) {
		have[g] = true
	}
	out.Missing = j.missing(need, have, card)
	return out
}

// cardClasses — аудитории (f, m, k) и корни цветов товара из названия и
// характеристик, и сами слова — для подписи.
func cardClasses(in DescriptionInput) (map[string]map[string]bool, map[string][]string) {
	card := map[string]map[string]bool{"gender": {}, "color": {}}
	words := map[string][]string{}
	for _, t := range wordsOf(in.Title + " " + in.Gender + " " + in.Color) {
		switch c := wordClass(t); c { // «Мужской», «черный» из характеристик — те же классы
		case "gender":
			maps.Copy(card[c], genderFamily(t))
			words[c] = append(words[c], t)
		case "color":
			card[c][colorRoot(t)] = true
			words[c] = append(words[c], t)
		}
	}
	return card, words
}

// mismatches — слова описания, которые спорят с товаром, каждое один раз.
func mismatches(desc string, card map[string]map[string]bool, words map[string][]string) []DescriptionMismatch {
	var out []DescriptionMismatch
	seen := map[string]bool{}
	toks := strings.FieldsFunc(foldWord(desc), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, t := range toks {
		if i > 0 && (toks[i-1] == "не" || toks[i-1] == "без") { // «не мужская» — не расхождение (ревью #626)
			continue
		}
		c := wordClass(t)
		if c == "" || len(card[c]) == 0 || fits(c, t, card[c]) || seen[c+t] {
			continue
		}
		seen[c+t] = true
		out = append(out, DescriptionMismatch{Kind: c, Found: t, Card: strings.Join(slices.Compact(words[c]), ", ")})
	}
	return out
}

// missing — слова главного запроса и частые слова его запросов, которых в
// описании нет; без брендов, другого пола и цвета.
func (j *titleJudge) missing(need []string, have map[string]bool, card map[string]map[string]bool) []TitleWord {
	var out []TitleWord
	for g := range j.sum {
		c := wordClass(g)
		if have[g] || j.nicheBrand(g) || j.brand[g] || c != "" && len(card[c]) > 0 && !fits(c, g, card[c]) {
			continue
		}
		why := WhyFrequent
		if slices.Contains(need, g) {
			why = WhyMain
		} else if j.share(g) < titleAddShare {
			continue
		}
		out = append(out, TitleWord{Word: g, Share: j.share(g), Why: why})
	}
	slices.SortFunc(out, func(a, b TitleWord) int {
		return cmp.Or(cmp.Compare(a.Why, b.Why), cmp.Compare(b.Share, a.Share), strings.Compare(a.Word, b.Word)) // слова главного — первыми
	})
	return out[:min(len(out), descriptionMissingMax)]
}

// fits: слово класса подходит товару — пол по аудитории («девочек» — женская
// и детская), цвет по основе слова (живая проверка #611).
func fits(class, w string, card map[string]bool) bool {
	if class == "gender" {
		return !audienceClash(w, card)
	}
	return card[colorRoot(w)]
}

// DescriptionSystem — роль модели для нового описания (#611).
const DescriptionSystem = `Ты пишешь описание карточки товара для Ozon по-русски.
Правила:
- описывай только то, что есть в названии и характеристиках; не выдумывай свойств, материалов, размеров и преимуществ, которых там нет;
- пол и цвет — строго как в характеристиках товара;
- естественно включи слова из списка «слова ниши», если они про этот товар; не перечисляй их списком;
- без чужих брендов, без капслока, без эмодзи, без обращений «Вы»/«ты» в каждом предложении;
- 600–1200 знаков, 2–4 абзаца, без заголовков и маркеров.
Ответь только текстом описания.`

// DescriptionPrompt — задание модели: товар, текущее описание, что не так и слова ниши.
func DescriptionPrompt(in DescriptionInput, c DescriptionCheck) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Название: %s\n", in.Title)
	if in.Gender != "" {
		fmt.Fprintf(&b, "Пол: %s\n", in.Gender)
	}
	if in.Color != "" {
		fmt.Fprintf(&b, "Цвет: %s\n", in.Color)
	}
	fmt.Fprintf(&b, "\nТекущее описание:\n%s\n", strings.TrimSpace(in.Description))
	if len(c.Mismatch) > 0 {
		b.WriteString("\nЧто в текущем описании спорит с товаром:\n")
		for _, m := range c.Mismatch {
			fmt.Fprintf(&b, "- %s в описании «%s», у товара — %s\n", map[string]string{"gender": "пол", "color": "цвет"}[m.Kind], m.Found, m.Card)
		}
	}
	if len(c.Missing) > 0 {
		words := make([]string, 0, len(c.Missing))
		for _, w := range c.Missing {
			words = append(words, w.Word)
		}
		fmt.Fprintf(&b, "\nСлова ниши, которых в описании нет: %s\n", strings.Join(words, ", "))
	}
	if c.Main.Text != "" {
		fmt.Fprintf(&b, "Главный поисковый запрос ниши: %s\n", c.Main.Text)
	}
	return b.String()
}
