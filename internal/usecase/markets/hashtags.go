package markets

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HashtagInput — хештеги карточки (атрибут Ozon «#Хештеги») и то, с чем их
// сравнивают (#610): название, свои бренды, запросы ниши, свои запросы товара.
type HashtagInput struct {
	Title, Hashtags string
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
	gender, color := map[string]bool{}, map[string]bool{}
	for _, t := range wordsOf(in.Title) {
		switch wordClass(t) {
		case "gender":
			gender[j.group(t)] = true
		case "color":
			color[j.group(t)] = true
		}
	}
	seen := map[string]bool{}
	tags := strings.Fields(in.Hashtags)
	out.TooMany = len(tags) > hashtagMax
	for _, tag := range tags {
		r := j.hashtag(tag, in.Queries, gender, seen)
		out.Tags = append(out.Tags, r)
		if r.Why != TagOK && r.Why != TagOwn {
			out.Remove = append(out.Remove, r)
		}
	}
	if ok && !out.TooMany {
		out.Add = j.newHashtags(in.Queries, main, gender, color, seen, min(hashtagAddMax, hashtagMax-len(tags)))
	}
	return out
}

// hashtag rates one tag; seen — наборы слов прежних хештегов, для дублей.
func (j *titleJudge) hashtag(tag string, qs []NicheQuery, gender, seen map[string]bool) HashtagRow {
	r := HashtagRow{Tag: tag}
	body, ok := strings.CutPrefix(tag, "#")
	gs := groupsOf(strings.Join(wordsOf(strings.ReplaceAll(body, "_", " ")), " "), j.group)
	key := setKey(gs)
	r.Why = j.tagFault(tag, body, ok, gs, gender, seen[key])
	seen[key] = true
	r.Demand = demand(qs, gs, j.group)
	if r.Why != TagOK {
		return r
	}
	if r.Demand == 0 {
		r.Why = TagNoDemand
		if q, found := j.ownWith(gs); found {
			r.Why, r.Query, r.Position = TagOwn, q.Text, q.Position
		}
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
func (j *titleJudge) newHashtags(qs []NicheQuery, main NicheQuery, gender, color, seen map[string]bool, n int) []HashtagRow {
	if n <= 0 {
		return nil
	}
	need := groupsOf(main.Text, j.group)
	cand := slices.Clone(qs)
	slices.SortStableFunc(cand, func(a, b NicheQuery) int { return cmp.Compare(queryWeight(b), queryWeight(a)) })
	var out []HashtagRow
	for _, q := range cand {
		gs := groupsOf(q.Text, j.group)
		tag := "#" + strings.Join(strings.FieldsFunc(foldWord(q.Text), func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) }), "_")
		if len(out) == n || !containsAll(gs, need) {
			continue
		}
		if seen[setKey(gs)] || utf8.RuneCountInString(tag) > hashtagMax || otherGender(gs, gender) || otherColor(gs, color) ||
			slices.ContainsFunc(gs, func(g string) bool { return j.brand[g] || j.nicheBrand(g) }) {
			continue
		}
		seen[setKey(gs)] = true
		out = append(out, HashtagRow{Tag: tag, Demand: demand(qs, gs, j.group), Why: TagNew})
	}
	slices.SortStableFunc(out, func(a, b HashtagRow) int { return cmp.Compare(b.Demand, a.Demand) }) // показ — по подписанному числу
	return out
}

// demand — запросов в месяц у запросов ниши со всеми словами gs.
func demand(qs []NicheQuery, gs []string, group func(string) string) int {
	if len(gs) == 0 {
		return 0
	}
	n := 0
	for _, q := range qs {
		if containsAll(groupsOf(q.Text, group), gs) {
			n += q.Count
		}
	}
	return n
}

// ownWith — свой запрос товара со всеми словами gs, по слову или основе.
func (j *titleJudge) ownWith(gs []string) (OwnQuery, bool) {
	for _, q := range j.ownList {
		have := groupsOf(q.Text, j.group)
		if containsAll(have, gs) {
			return q, true
		}
	}
	return OwnQuery{}, false
}

// otherGender: в хештеге пол, а у товара другой.
func otherGender(gs []string, gender map[string]bool) bool {
	if len(gender) == 0 {
		return false
	}
	for _, g := range gs {
		if wordClass(g) == "gender" && !gender[g] {
			return true
		}
	}
	return false
}

// otherColor: у товара назван цвет, а в запросе другой (живая проверка #610).
func otherColor(gs []string, color map[string]bool) bool {
	if len(color) == 0 {
		return false
	}
	return slices.ContainsFunc(gs, func(g string) bool { return wordClass(g) == "color" && !color[g] })
}

func setKey(gs []string) string {
	s := slices.Clone(gs)
	slices.Sort(s)
	return strings.Join(s, " ")
}
