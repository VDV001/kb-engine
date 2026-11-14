package markets

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HashtagInput — хештеги карточки (атрибут Ozon «#Хештеги») и то, с чем их
// сравнивают (#610): название, свои бренды, запросы ниши, свои запросы товара.
type HashtagInput struct {
	Title, Hashtags string
	Gender, Color   string // характеристики карточки «Пол» и «Цвет» (ревью #626)
	Brands          []string
	Queries         []NicheQuery
	Forms           []WordForms
	Own             []OwnQuery
	NicheBrands     []string
}

// HashtagWhy — что с хештегом: оставить или почему убрать.
type HashtagWhy int

// Оставить: TagOK, TagOwn; убрать — остальные; TagNew — совет добавить.
const (
	TagOK        HashtagWhy = iota // есть спрос в запросах ниши
	TagOwn                         // спроса в нише нет, но товар по нему уже находят
	TagFormat                      // не по правилам Ozon: «#», буквы, цифры и «_»
	TagLong                        // длиннее 30 символов
	TagDuplicate                   // те же слова, что у хештега раньше
	TagBrand                       // бренд: Ozon просит без брендов
	TagOffTopic                    // другой пол, чем у товара
	TagNoDemand                    // в запросах ниши нет
	TagNew                         // частый запрос ниши, которого среди хештегов нет
)

// HashtagRow — хештег и спрос: Demand — запросов в месяц со всеми его словами.
type HashtagRow struct {
	Tag      string
	Demand   int
	Why      HashtagWhy
	Query    string // свой запрос товара (TagOwn)
	Position int
}

// HashtagCheck — оценка хештегов: главный запрос ниши, каждый хештег, что
// убрать и что добавить; TooMany — их больше, чем принимает Ozon.
type HashtagCheck struct {
	Main          NicheQuery
	Tags, Remove  []HashtagRow
	Add           []HashtagRow
	TooMany       bool
	Total, Ozon30 int
}

const (
	hashtagMax    = 30 // хештегов в карточке и символов в хештеге (справочник Ozon, 15.11)
	hashtagAddMax = 10 // советов добавить за раз
)

// CheckHashtags judges a card's hashtags by the niche queries (#610).
func CheckHashtags(in HashtagInput) HashtagCheck {
	out := HashtagCheck{Total: len(in.Queries), Ozon30: hashtagMax}
	main, ok := mainQuery(in.Queries)
	if ok {
		out.Main = main
	}
	j := newTitleJudge(TitleInput{Title: in.Title, Brands: in.Brands, Queries: in.Queries, Forms: in.Forms, Own: in.Own, NicheBrands: in.NicheBrands})
	qg := make([][]string, len(in.Queries)) // группы слов запросов — один раз на проверку (ревью #625)
	for i, q := range in.Queries {
		qg[i] = groupsOf(q.Text, j.group)
	}
	gender, color := map[string]bool{}, map[string]bool{}
	for _, t := range wordsOf(in.Title + " " + in.Gender + " " + in.Color) { // и характеристики карточки (ревью #626)
		switch wordClass(t) {
		case "gender":
			maps.Copy(gender, genderFamily(t))
		case "color":
			color[colorRoot(t)] = true
		}
	}
	seen := map[string]bool{}
	tags := strings.Fields(in.Hashtags)
	out.TooMany = len(tags) > hashtagMax
	for _, tag := range tags {
		r := j.hashtag(tag, in.Queries, qg, gender, seen)
		out.Tags = append(out.Tags, r)
		if r.Why != TagOK && r.Why != TagOwn {
			out.Remove = append(out.Remove, r)
		}
	}
	if room := hashtagMax - (len(tags) - len(out.Remove)); ok && room > 0 { // место — после уборки (ревью #625)
		out.Add = j.newHashtags(in.Queries, qg, main, gender, color, seen, min(hashtagAddMax, room))
	}
	return out
}

// hashtag rates one tag; seen — наборы слов прежних годных хештегов, для дублей.
func (j *titleJudge) hashtag(tag string, qs []NicheQuery, qg [][]string, gender, seen map[string]bool) HashtagRow {
	r := HashtagRow{Tag: tag}
	body, ok := strings.CutPrefix(tag, "#")
	gs := groupsOf(strings.Join(wordsOf(strings.ReplaceAll(body, "_", " ")), " "), j.group)
	key := setKey(gs)
	r.Why = j.tagFault(tag, body, ok, gs, gender, len(gs) > 0 && seen[key])
	r.Demand = demand(qs, qg, gs)
	if r.Why == TagOK && r.Demand == 0 {
		r.Why = TagNoDemand
		if q, found := j.ownWith(gs); found {
			r.Why, r.Query, r.Position = TagOwn, q.Text, q.Position
		}
	}
	if len(gs) > 0 && (r.Why == TagOK || r.Why == TagOwn || r.Why == TagNoDemand) {
		seen[key] = true // отбракованный не делает правильный дублем (ревью #625)
	}
	return r
}

// tagFault — почему хештег убрать; TagOK — не за что.
func (j *titleJudge) tagFault(tag, body string, hash bool, gs []string, gender map[string]bool, dup bool) HashtagWhy {
	switch {
	case !hash || body == "" || strings.ContainsFunc(body, func(c rune) bool { return c != '_' && !unicode.IsLetter(c) && !unicode.IsDigit(c) }):
		return TagFormat
	case utf8.RuneCountInString(tag) > hashtagMax:
		return TagLong
	case dup:
		return TagDuplicate
	case slices.ContainsFunc(gs, func(g string) bool { return j.brand[g] || j.nicheBrand(g) }):
		return TagBrand
	case otherGender(gs, gender):
		return TagOffTopic
	}
	return TagOK
}

// newHashtags — частые запросы со всеми словами главного, которых среди хештегов нет.
// Хештег — слова запроса как есть, со служебными («#футболка_для_спорта_женская»).
func (j *titleJudge) newHashtags(qs []NicheQuery, qg [][]string, main NicheQuery, gender, color, seen map[string]bool, n int) []HashtagRow {
	need := groupsOf(main.Text, j.group)
	order := make([]int, len(qs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(queryWeight(qs[b]), queryWeight(qs[a])) })
	var out []HashtagRow
	for _, i := range order {
		if len(out) == n {
			break
		}
		gs := qg[i]
		tag := "#" + strings.Join(strings.FieldsFunc(foldWord(qs[i].Text), func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) }), "_")
		if !containsAll(gs, need) || seen[setKey(gs)] || utf8.RuneCountInString(tag) > hashtagMax || otherGender(gs, gender) || otherColor(gs, color) ||
			slices.ContainsFunc(gs, func(g string) bool { return j.brand[g] || j.nicheBrand(g) }) {
			continue
		}
		seen[setKey(gs)] = true
		out = append(out, HashtagRow{Tag: tag, Demand: demand(qs, qg, gs), Why: TagNew})
	}
	slices.SortStableFunc(out, func(a, b HashtagRow) int { return cmp.Compare(b.Demand, a.Demand) }) // показ — по подписанному числу
	return out
}

// demand — запросов в месяц у запросов ниши со всеми словами gs; qg — их группы.
func demand(qs []NicheQuery, qg [][]string, gs []string) int {
	if len(gs) == 0 {
		return 0
	}
	n := 0
	for i, q := range qs {
		if containsAll(qg[i], gs) {
			n += q.Count
		}
	}
	return n
}

// ownWith — свой запрос товара со всеми словами gs, по слову или основе.
func (j *titleJudge) ownWith(gs []string) (OwnQuery, bool) {
	for _, q := range j.ownList {
		if containsAll(groupsOf(q.Text, j.group), gs) {
			return q, true
		}
	}
	return OwnQuery{}, false
}

// genderFamily — аудитории слова пола: f — женская, m — мужская, k — детская;
// «девочек» — f и k, «унисекс» — все.
func genderFamily(w string) map[string]bool {
	fam := map[string]bool{}
	for _, f := range []struct {
		root string
		to   []string
	}{{"женск", []string{"f"}}, {"женщин", []string{"f"}}, {"девоч", []string{"f", "k"}}, {"девуш", []string{"f"}},
		{"мужск", []string{"m"}}, {"мужчин", []string{"m"}}, {"мальчик", []string{"m", "k"}}, {"юнош", []string{"m"}},
		{"детск", []string{"k"}}, {"подростк", []string{"k"}}, {"унисекс", []string{"f", "m", "k"}}} {
		if strings.HasPrefix(w, f.root) {
			for _, t := range f.to {
				fam[t] = true
			}
		}
	}
	return fam
}

// audienceClash: слово пола спорит с аудиторией товара. Пол указан у обоих —
// спорит разный («девочек» и «Мальчики», хоть оба детские, ревью #626);
// слово только детское, а товар взрослый — тоже спорит.
func audienceClash(w string, card map[string]bool) bool {
	wf := genderFamily(w)
	if len(wf) == 0 || len(card) == 0 {
		return false
	}
	ws, cs := wf["f"] || wf["m"], card["f"] || card["m"]
	switch {
	case ws && cs:
		return (!wf["f"] || !card["f"]) && (!wf["m"] || !card["m"])
	case !ws:
		return !card["k"]
	}
	return false
}

// otherGender: в хештеге аудитория, которой у товара нет.
func otherGender(gs []string, gender map[string]bool) bool {
	return slices.ContainsFunc(gs, func(g string) bool { return audienceClash(g, gender) })
}

// otherColor: у товара назван цвет, а в запросе другой (живая проверка #610).
func otherColor(gs []string, color map[string]bool) bool {
	if len(color) == 0 {
		return false
	}
	return slices.ContainsFunc(gs, func(g string) bool { return wordClass(g) == "color" && !color[colorRoot(g)] })
}

func setKey(gs []string) string {
	s := slices.Clone(gs)
	slices.Sort(s)
	return strings.Join(s, " ")
}
