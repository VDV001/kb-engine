package markets

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// NicheQuery — поисковый запрос ниши из сервиса аналитики: частота за период
// и сколько товаров ниши в его выдаче.
type NicheQuery struct {
	Text         string
	Count, Items int
}

// WordForms — словоформы одного слова запросов ниши: «женская, женский, женские».
type WordForms struct {
	Word  string
	Forms []string
}

// OwnQuery — запрос, по которому находят сам товар, и средняя позиция в выдаче.
type OwnQuery struct {
	Text            string
	Count, Position int
}

// TitleInput — название карточки и то, с чем его сравнивают (#607).
type TitleInput struct {
	Title   string
	Brands  []string // бренд и продавец товара: их не убирают
	Queries []NicheQuery
	Forms   []WordForms
	Own     []OwnQuery
}

// TitleWhy — почему слово оставить, добавить или убрать.
type TitleWhy int

const (
	WhyKeep      TitleWhy = iota // частое в запросах главного
	WhyMain                      // слово главного запроса, а в названии его нет
	WhyFrequent                  // частое в запросах главного, а в названии его нет
	WhyRare                      // в запросах главного почти не встречается
	WhyBrand                     // бренд или продавец товара
	WhyAttribute                 // цвет или пол: свойство товара, хоть и редкое
	WhyOwn                       // редкое, но товар уже находят по запросу с ним
)

// TitleWord — слово названия или совет; Share — доля спроса запросов главного
// со всеми его словами, в которых это слово есть. Query и Position — запрос
// товара, по которому его находят (у WhyOwn).
type TitleWord struct {
	Word     string
	Share    float64
	Why      TitleWhy
	Query    string
	Position int
}

// TitleCheck — оценка названия: главный запрос ниши, сколько запросов всего и
// сколько со всеми словами главного (по ним доли), слова названия и советы.
type TitleCheck struct {
	Main           NicheQuery
	Total, Related int
	Words          []TitleWord
	Add, Remove    []TitleWord
}

const (
	titleAddShare  = 0.02 // слово чаще — предлагается добавить
	titleAddMax    = 5    // частых слов в «добавить», кроме слов главного
	titleRareShare = 0.01 // слово реже — предлагается убрать
)

// CheckTitle judges a card title by the niche queries (#607). Главный запрос —
// наибольшая частота × товаров ниши в выдаче: голая частота выводит наверх
// запросы, по которым товаров ниши почти не показывают (замер 14.11:
// «лонгслив женский» — 150 тыс. при одном товаре). Доли слов считаются по
// запросам со всеми словами главного, поэтому «мужская» не советуется к
// «футболке женской».
func CheckTitle(in TitleInput) TitleCheck {
	out := TitleCheck{Total: len(in.Queries)}
	form := make(map[string]string)
	for _, f := range in.Forms {
		for _, w := range f.Forms {
			if _, ok := form[foldWord(w)]; !ok {
				form[foldWord(w)] = foldWord(f.Word)
			}
		}
	}
	group := func(tok string) string {
		if g, ok := form[tok]; ok {
			return g
		}
		return tok
	}
	weight := func(q NicheQuery) float64 { return float64(q.Count) * float64(q.Items) }
	var main NicheQuery
	for _, q := range in.Queries {
		if weight(q) > weight(main) {
			main = q
		}
	}
	if weight(main) <= 0 {
		return out
	}
	out.Main = main
	need := groupsOf(main.Text, group)
	sum := make(map[string]float64)
	var total float64
	for _, q := range in.Queries {
		gs := groupsOf(q.Text, group)
		if !containsAll(gs, need) {
			continue
		}
		out.Related++
		total += weight(q)
		for _, g := range gs {
			sum[g] += weight(q)
		}
	}
	share := func(g string) float64 { return sum[g] / total }
	own := make(map[string]OwnQuery)
	for _, q := range in.Own {
		for _, g := range groupsOf(q.Text, group) {
			if _, ok := own[g]; !ok {
				own[g] = q
			}
		}
	}
	brand := make(map[string]bool)
	for _, b := range in.Brands {
		for _, t := range wordsOf(b) {
			brand[t] = true
		}
	}
	have := make(map[string]bool)
	classes := make(map[string]bool)
	for _, tok := range wordsOf(in.Title) {
		g := group(tok)
		if have[g] {
			continue
		}
		have[g] = true
		if c := wordClass(tok); c != "" {
			classes[c] = true
		}
		w := TitleWord{Word: tok, Share: share(g)}
		q, found := own[g]
		switch {
		case w.Share >= titleRareShare:
			w.Why = WhyKeep
		case brand[tok]:
			w.Why = WhyBrand
		case wordClass(tok) != "":
			w.Why = WhyAttribute
		case found:
			w.Why, w.Query, w.Position = WhyOwn, q.Text, q.Position
		default:
			w.Why = WhyRare
			out.Remove = append(out.Remove, w)
		}
		out.Words = append(out.Words, w)
	}
	for _, g := range need {
		if !have[g] {
			out.Add = append(out.Add, TitleWord{Word: g, Share: share(g), Why: WhyMain})
			have[g] = true
		}
	}
	var often []TitleWord
	for g := range sum {
		if have[g] || share(g) < titleAddShare || classes[wordClass(g)] && wordClass(g) != "" {
			continue // уже есть, редкое или другой цвет и пол к уже названным
		}
		often = append(often, TitleWord{Word: g, Share: share(g), Why: WhyFrequent})
	}
	slices.SortFunc(often, func(a, b TitleWord) int {
		return cmp.Or(cmp.Compare(b.Share, a.Share), strings.Compare(a.Word, b.Word))
	})
	out.Add = append(out.Add, often[:min(len(often), titleAddMax)]...)
	return out
}

// titleStop — служебные слова: по ним не судят.
var titleStop = map[string]bool{"для": true, "без": true, "или": true, "под": true, "над": true, "при": true, "про": true, "шт": true,
	"на": true, "из": true, "по": true, "от": true, "до": true, "за": true, "не": true}

// wordsOf splits text into folded words to judge: без чисел, однобуквенных и служебных.
func wordsOf(text string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(foldWord(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(t) < 2 || titleStop[t] || strings.IndexFunc(t, unicode.IsLetter) < 0 {
			continue
		}
		out = append(out, t)
	}
	return out
}

// groupsOf — разные слова текста, приведенные к слову запросов ниши.
func groupsOf(text string, group func(string) string) []string {
	var out []string
	for _, t := range wordsOf(text) {
		if g := group(t); !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

func containsAll(have, need []string) bool {
	for _, n := range need {
		if !slices.Contains(have, n) {
			return false
		}
	}
	return true
}

func foldWord(s string) string { return strings.ReplaceAll(strings.ToLower(s), "ё", "е") }

// ponytail: цвет и пол узнаются по короткому списку корней — редкий цвет
// («лососевый») сочтется обычным словом и может попасть в «убрать». Потолок —
// этот список; апгрейд — атрибуты карточки из Ozon Seller API (цвет, пол).
var (
	colorRoots = []string{"бел", "черн", "сер", "беж", "син", "голуб", "красн", "розов", "зелен", "желт", "оранжев", "фиолетов", "сиренев",
		"коричнев", "бордов", "молочн", "графитов", "лавандов", "мятн", "бирюзов", "изумрудн", "оливков", "песочн", "пудров", "кремов",
		"горчичн", "малинов", "вишнев", "терракотов", "золотист", "серебрист", "персиков", "коралл", "темн", "светл"}
	colorWords  = []string{"хаки", "фуксия", "бордо", "беж", "айвори", "мокко", "капучино", "шоколад"}
	genderRoots = []string{"женск", "мужск", "детск"}
	genderWords = []string{"унисекс", "девочек", "девочки", "девочка", "мальчиков", "мальчика", "мальчик", "подростковая", "подростковый"}
	adjEndings  = []string{"ого", "его", "ый", "ий", "ой", "ая", "яя", "ое", "ее", "ые", "ие", "ую", "юю", "ых", "их", "ом", "ем"}
)

// wordClass — «color», «gender» или "": у таких слов варианты исключают друг друга.
func wordClass(w string) string {
	is := func(roots, words []string) bool {
		if slices.Contains(words, w) {
			return true
		}
		for _, e := range adjEndings {
			if r, ok := strings.CutSuffix(w, e); ok && slices.Contains(roots, r) {
				return true
			}
		}
		return false
	}
	switch {
	case is(colorRoots, colorWords):
		return "color"
	case is(genderRoots, genderWords):
		return "gender"
	}
	return ""
}
