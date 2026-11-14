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
	// NicheBrands — бренды ниши из сервиса аналитики: их не советуют (#623)
	NicheBrands []string
}

// TitleWhy — почему слово оставить, добавить или убрать.
type TitleWhy int

// Причины: оставить, добавить (Main, Frequent), убрать (Rare) и почему не убирать.
const (
	WhyKeep       TitleWhy = iota // частое в запросах главного
	WhyMain                       // слово главного запроса, а в названии его нет
	WhyFrequent                   // частое в запросах главного, а в названии его нет
	WhyRare                       // в запросах главного почти не встречается
	WhyBrand                      // бренд или продавец товара
	WhyAttribute                  // цвет или пол: свойство товара, хоть и редкое
	WhyOwn                        // редкое, но товар уже находят по запросу с ним
	WhyNicheBrand                 // частое, но это бренд ниши: чужой бренд в названии — риск снятия карточки
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
	Dropped        []TitleWord // частые слова, не советуемые как бренды ниши
	NicheBrands    int         // сколько брендов ниши учтено
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
	main, ok := mainQuery(in.Queries)
	if !ok {
		return out
	}
	out.Main = main
	j := newTitleJudge(in)
	need := groupsOf(main.Text, j.group)
	out.Related = j.count(in.Queries, need)
	have, classes := j.judge(in.Title, &out)
	for _, g := range need {
		if !have[g] {
			out.Add = append(out.Add, TitleWord{Word: g, Share: j.share(g), Why: WhyMain})
			have[g] = true
		}
	}
	brands := nicheBrands(in.NicheBrands, j.brand)
	out.NicheBrands = len(brands.names)
	for _, w := range j.often(have, classes) {
		if brands.words[w.Word] {
			w.Why = WhyNicheBrand
			out.Dropped = append(out.Dropped, w)
			continue
		}
		if len(out.Add) < len(need)+titleAddMax {
			out.Add = append(out.Add, w)
		}
	}
	return out
}

func queryWeight(q NicheQuery) float64 { return float64(q.Count) * float64(q.Items) }

// mainQuery — запрос с наибольшей частотой × товаров ниши; false — судить не по чему.
func mainQuery(qs []NicheQuery) (NicheQuery, bool) {
	var main NicheQuery
	for _, q := range qs {
		if queryWeight(q) > queryWeight(main) {
			main = q
		}
	}
	return main, queryWeight(main) > 0
}

// titleJudge — словоформы, спрос слов в запросах главного, свои запросы товара и бренды.
type titleJudge struct {
	form  map[string]string
	stem  map[string]string // основа формы → слово запросов ниши: формы MPStats неполные
	sum   map[string]float64
	total float64
	own   map[string]OwnQuery // по слову запросов ниши и по основе слова
	brand map[string]bool
}

func newTitleJudge(in TitleInput) *titleJudge {
	j := &titleJudge{form: map[string]string{}, stem: map[string]string{}, sum: map[string]float64{}, own: map[string]OwnQuery{}, brand: map[string]bool{}}
	for _, f := range in.Forms {
		for _, w := range f.Forms {
			if _, ok := j.form[foldWord(w)]; !ok {
				j.form[foldWord(w)] = foldWord(f.Word)
			}
			if _, ok := j.stem[wordStem(foldWord(w))]; !ok {
				j.stem[wordStem(foldWord(w))] = foldWord(f.Word)
			}
		}
	}
	for _, q := range in.Own {
		for _, t := range wordsOf(q.Text) {
			for _, k := range []string{j.group(t), "~" + wordStem(t)} { // формы MPStats неполные: «базовый» ≠ «базовая» (замер 14.11)
				if _, ok := j.own[k]; !ok {
					j.own[k] = q
				}
			}
		}
	}
	for _, b := range in.Brands {
		for _, t := range wordsOf(b) {
			j.brand[t] = true
		}
	}
	return j
}

// group — слово запросов ниши, к которому относится словоформа tok.
func (j *titleJudge) group(tok string) string {
	if g, ok := j.form[tok]; ok {
		return g
	}
	if g, ok := j.stem[wordStem(tok)]; ok { // «футболки» при одной форме «футболка» (ревью #621)
		return g
	}
	return tok
}

// count sums the demand of words over queries with all words need; возвращает, сколько таких запросов.
func (j *titleJudge) count(qs []NicheQuery, need []string) int {
	n := 0
	for _, q := range qs {
		gs := groupsOf(q.Text, j.group)
		if !containsAll(gs, need) {
			continue
		}
		n++
		j.total += queryWeight(q)
		for _, g := range gs {
			j.sum[g] += queryWeight(q)
		}
	}
	return n
}

func (j *titleJudge) share(g string) float64 { return j.sum[g] / j.total }

// judge rates each word of title into out.Words and out.Remove; have — слова
// названия, classes — названные цвет и пол.
func (j *titleJudge) judge(title string, out *TitleCheck) (have, classes map[string]bool) {
	have, classes = map[string]bool{}, map[string]bool{}
	for _, tok := range wordsOf(title) {
		g := j.group(tok)
		if have[g] {
			continue
		}
		have[g] = true
		if c := wordClass(tok); c != "" {
			classes[c] = true
		}
		w := TitleWord{Word: tok, Share: j.share(g)}
		q, found := j.own[g]
		if !found {
			q, found = j.own["~"+wordStem(tok)]
		}
		switch {
		case w.Share >= titleRareShare:
			w.Why = WhyKeep
		case j.brand[tok]:
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
	return have, classes
}

// often — частые слова запросов главного, которых в названии нет, самые частые первыми.
func (j *titleJudge) often(have, classes map[string]bool) []TitleWord {
	var out []TitleWord
	for g := range j.sum {
		if c := wordClass(g); have[g] || j.share(g) < titleAddShare || c != "" && classes[c] {
			continue // уже есть, редкое или другой цвет и пол к уже названным
		}
		out = append(out, TitleWord{Word: g, Share: j.share(g), Why: WhyFrequent})
	}
	slices.SortFunc(out, func(a, b TitleWord) int {
		return cmp.Or(cmp.Compare(b.Share, a.Share), strings.Compare(a.Word, b.Word))
	})
	return out
}

// brandSet — бренды ниши: названия и слова, по которым их узнают в запросах.
type brandSet struct {
	names map[string]bool
	words map[string]bool
}

// nicheBrands collects the niche's brands but own ones: слово бренда как есть
// и латиница кириллицей («zara» → «зара»), у многословного — латинские слова от трех букв.
func nicheBrands(names []string, own map[string]bool) brandSet {
	b := brandSet{names: map[string]bool{}, words: map[string]bool{}}
	for _, n := range names {
		ws := wordsOf(n)
		if len(ws) == 0 || foldWord(strings.TrimSpace(n)) == "бренд не указан" || slices.ContainsFunc(ws, func(w string) bool { return own[w] }) {
			continue
		}
		b.names[strings.Join(ws, " ")] = true
		for _, w := range ws {
			if len(ws) > 1 && (!isLatin(w) || utf8.RuneCountInString(w) < 3) {
				continue
			}
			b.words[w] = true
			if isLatin(w) {
				b.words[translit(w)] = true
			}
		}
	}
	return b
}

func isLatin(w string) bool {
	return strings.IndexFunc(w, func(r rune) bool { return r > unicode.MaxASCII }) < 0 && strings.IndexFunc(w, unicode.IsLetter) >= 0
}

// ponytail: побуквенная транслитерация ловит «zara» → «зара», «skims» → «скимс»,
// но не произношение («nike» → «нике», а в запросах «найк»); потолок — эта
// таблица, апгрейд — словарь брендов с русскими написаниями.
var latinToCyr = strings.NewReplacer("sch", "щ", "sh", "ш", "ch", "ч", "zh", "ж", "kh", "х", "ts", "ц", "yu", "ю", "ya", "я", "yo", "е",
	"a", "а", "b", "б", "c", "к", "d", "д", "e", "е", "f", "ф", "g", "г", "h", "х", "i", "и", "j", "дж", "k", "к", "l", "л", "m", "м",
	"n", "н", "o", "о", "p", "п", "q", "к", "r", "р", "s", "с", "t", "т", "u", "у", "v", "в", "w", "в", "x", "кс", "y", "и", "z", "з")

func translit(w string) string { return latinToCyr.Replace(w) }

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

// stemEndings — окончания прилагательных и существительных, длинные первыми.
var stemEndings = []string{"ого", "его", "ами", "ями", "ый", "ий", "ой", "ая", "яя", "ое", "ее", "ые", "ие", "ую", "юю", "ых", "их", "ом", "ем",
	"ов", "ев", "ей", "ам", "ям", "ах", "ях", "ы", "и", "а", "я", "у", "ю", "е", "о"}

// wordStem — слово без окончания, если основа не короче четырех букв:
// «футболки», «футболка» → «футболк»; короткие слова («белая») не режутся.
// ponytail: грубая основа без словаря — редкие слова могут совпасть по основе;
// потолок — сравнение форм; апгрейд — морфологический словарь.
func wordStem(w string) string {
	for _, e := range stemEndings {
		if r, ok := strings.CutSuffix(w, e); ok && utf8.RuneCountInString(r) >= 4 {
			return r
		}
	}
	return w
}

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
