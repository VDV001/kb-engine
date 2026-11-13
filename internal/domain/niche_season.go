package domain

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrUnknownMarketplace is returned for a marketplace the window does not know.
var ErrUnknownMarketplace = errors.New("площадка не узнана")

// NicheMarketplace — колонка площадок ниши: текст «WB, Ozon», а не число.
const NicheMarketplace NicheColumn = "marketplace"

// Marketplace — площадка продаж.
type Marketplace string

// Площадки.
const (
	MarketWB   Marketplace = "wb"
	MarketOzon Marketplace = "ozon"
)

var marketplaceNames = map[string]Marketplace{
	"wb": MarketWB, "wildberries": MarketWB, "вб": MarketWB, "вайлдберриз": MarketWB, "валдберис": MarketWB,
	"ozon": MarketOzon, "озон": MarketOzon,
}

// MonthSales — продажи ниши за месяц; Year 0 — год в заголовке не указан.
type MonthSales struct {
	Month time.Month
	Year  int
	Value float64
}

// GrowthLevel — как растет спрос ниши к прошлому году.
type GrowthLevel string

// Уровни роста: быстрее 15 % — быстро, ниже нуля — падает.
const (
	GrowthFast     GrowthLevel = "fast"
	GrowthModerate GrowthLevel = "moderate"
	GrowthFalling  GrowthLevel = "falling"
)

// NicheMissing — чего не хватает для полной оценки.
type NicheMissing string

// Чего может не хватать.
const (
	NicheMissingMonths    NicheMissing = "months"    // продаж по месяцам: нет сезона
	NicheMissingGrowth    NicheMissing = "growth"    // роста к году
	NicheMissingTop10     NicheMissing = "top10"     // доли топ-10
	NicheMissingPerSeller NicheMissing = "perSeller" // выручки (или продаж и цены) и продавцов
)

// Оценка ниши 0-100 (макет #580, решение владельца 12.11): веса частей.
// ponytail: веса зашиты. Потолок — человек с другим взглядом на нишу; путь —
// веса в настройках модуля, когда владелец назовет, что менять.
const (
	weightGrowth    = 0.4
	weightPerSeller = 0.3
	weightTop10     = 0.2
	weightSeason    = 0.1
	// Рост к году от -10 % (часть 0) до +45 % (часть 1).
	growthFloor = -0.1
	growthSpan  = 0.55
	// Топ-10 держат больше — войти труднее.
	crowdedTop10 = 0.45
	fastGrowth   = 0.15
	// Вне сезона часть сезона не ноль: ниша живет и вне пика.
	offSeason = 0.3
	// Сезон — пик в этом месяце или в двух следующих.
	seasonAhead = 2
	// Пик по месяцам ищется, если их хотя бы полгода.
	minMonthsForPeak = 6
)

// NicheScore — оценка ниши и факты, из которых она сложена. Ok — оценка
// есть: хватило роста, доли топ-10 и выручки на продавца; без месяцев часть
// сезона выпадает, а остальные веса делятся заново.
type NicheScore struct {
	Ok           bool
	Score        int
	Missing      []NicheMissing
	HasPerSeller bool
	PerSeller    float64 // выручка на продавца, ₽ в месяц
	Growth       GrowthLevel
	HasTop10     bool
	Crowded      bool
	HasPeak      bool
	Peak         time.Month
	InSeason     bool
	WeeksFrom    int // недель до начала месяца пика
	WeeksTo      int // недель до конца месяца пика
}

var monthTokens = map[string]time.Month{
	"январь": 1, "января": 1, "янв": 1, "февраль": 2, "февраля": 2, "фев": 2,
	"март": 3, "марта": 3, "мар": 3, "апрель": 4, "апреля": 4, "апр": 4,
	"май": 5, "мая": 5, "июнь": 6, "июня": 6, "июн": 6, "июль": 7, "июля": 7, "июл": 7,
	"август": 8, "августа": 8, "авг": 8, "сентябрь": 9, "сентября": 9, "сен": 9, "сент": 9,
	"октябрь": 10, "октября": 10, "окт": 10, "ноябрь": 11, "ноября": 11, "ноя": 11,
	"декабрь": 12, "декабря": 12, "дек": 12,
}

var (
	monthWord = regexp.MustCompile(`^(?:продажи,?)?([а-я]+)\.?,?(\d{2}|\d{4})?$`)
	monthYM   = regexp.MustCompile(`^(\d{4})[./](\d{1,2})$`)
	monthMY   = regexp.MustCompile(`^(\d{1,2})[./](\d{4})$`)
)

// RecognizeNicheMonth reads a month column header: «Ноябрь 2025», «ноя
// 2025», «Дек.25», «Продажи, окт 2026», «2026-03», «03.2026». Год 0 — не указан.
func RecognizeNicheMonth(header string) (time.Month, int, bool) {
	// FoldName убирает дефисы: «2026-03» станет «202603» — дефис меняем на точку раньше.
	h := FoldName(strings.ReplaceAll(header, "-", "."))
	if m := monthWord.FindStringSubmatch(h); m != nil {
		mon, ok := monthTokens[m[1]]
		if !ok {
			return 0, 0, false
		}
		return mon, yearOf(m[2]), true
	}
	var y, mo string
	if m := monthYM.FindStringSubmatch(h); m != nil {
		y, mo = m[1], m[2]
	} else if m := monthMY.FindStringSubmatch(h); m != nil {
		mo, y = m[1], m[2]
	} else {
		return 0, 0, false
	}
	n, _ := strconv.Atoi(mo)
	if n < 1 || n > 12 {
		return 0, 0, false
	}
	return time.Month(n), yearOf(y), true
}

func yearOf(s string) int {
	y, _ := strconv.Atoi(s)
	if y > 0 && y < 100 {
		y += 2000
	}
	return y
}

// ParseMarketplaces reads a marketplaces cell: «WB, Ozon», «озон / вб».
// Порядок — WB, затем Ozon; повтор не считается; пусто — площадок нет.
func ParseMarketplaces(cell string) ([]Marketplace, error) {
	parts := strings.FieldsFunc(strings.ToLower(cell), func(r rune) bool { return strings.ContainsRune(",;/+|", r) })
	var out []Marketplace
	for _, p := range parts {
		p = FoldName(p)
		if p == "" || p == "и" {
			continue
		}
		m, ok := marketplaceNames[p]
		if !ok {
			return nil, fmt.Errorf("%w: «%s»", ErrUnknownMarketplace, strings.TrimSpace(p))
		}
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b Marketplace) int { return strings.Compare(marketOrder(a), marketOrder(b)) })
	return out, nil
}

func marketOrder(m Marketplace) string {
	if m == MarketWB {
		return "0"
	}
	return "1" + string(m)
}

// WithMonths returns a copy with monthly sales in file order. Продажи не
// меньше нуля, один месяц одного года — один раз.
func (n *Niche) WithMonths(ms []MonthSales) (*Niche, error) {
	type key struct {
		m time.Month
		y int
	}
	seen := map[key]bool{}
	for _, s := range ms {
		if s.Month < time.January || s.Month > time.December {
			return nil, fmt.Errorf("%w: «%s» месяц %d", ErrInvalidNiche, n.name, s.Month)
		}
		if s.Value < 0 || math.IsNaN(s.Value) {
			return nil, fmt.Errorf("%w: «%s» продажи за месяц меньше нуля", ErrInvalidNiche, n.name)
		}
		k := key{s.Month, s.Year}
		if seen[k] {
			return nil, fmt.Errorf("%w: «%s» месяц %d дважды", ErrInvalidNiche, n.name, s.Month)
		}
		seen[k] = true
	}
	c := *n
	c.months = slices.Clone(ms)
	return &c, nil
}

// WithMarketplaces returns a copy with marketplaces.
func (n *Niche) WithMarketplaces(ms []Marketplace) (*Niche, error) {
	for _, m := range ms {
		if m != MarketWB && m != MarketOzon {
			return nil, fmt.Errorf("%w: %q", ErrUnknownMarketplace, m)
		}
	}
	c := *n
	c.markets = slices.Clone(ms)
	return &c, nil
}

// Months returns monthly sales in file order.
func (n *Niche) Months() []MonthSales { return slices.Clone(n.months) }

// Marketplaces returns where the niche sells; пусто — в файле не сказано.
func (n *Niche) Marketplaces() []Marketplace { return slices.Clone(n.markets) }

// PeakMonth returns the month of the highest sales; нужны хотя бы полгода.
func (n *Niche) PeakMonth() (time.Month, bool) {
	if len(n.months) < minMonthsForPeak {
		return 0, false
	}
	best := n.months[0]
	for _, s := range n.months[1:] {
		if s.Value > best.Value {
			best = s
		}
	}
	return best.Month, true
}

// perSeller is revenue per seller: выручка, а без нее продажи × цена.
func (n *Niche) perSeller() (float64, bool) {
	sellers, ok := n.Value(NicheSellers)
	if !ok || sellers <= 0 {
		return 0, false
	}
	if rev, ok := n.Value(NicheRevenue); ok {
		return rev / sellers, true
	}
	sales, ok1 := n.Value(NicheSales)
	price, ok2 := n.Value(NichePrice)
	if !ok1 || !ok2 {
		return 0, false
	}
	return sales * price / sellers, true
}

// ScoreNiches scores niches against each other on now: выручка на продавца
// сравнивается с лучшей среди ниш файла.
func ScoreNiches(ns []*Niche, now time.Time) []NicheScore {
	maxPer := 0.0
	for _, n := range ns {
		if p, ok := n.perSeller(); ok {
			maxPer = max(maxPer, p)
		}
	}
	out := make([]NicheScore, len(ns))
	for i, n := range ns {
		out[i] = scoreNiche(n, maxPer, now)
	}
	return out
}

func scoreNiche(n *Niche, maxPer float64, now time.Time) NicheScore {
	var s NicheScore
	sum, weight := 0.0, 0.0
	if g, ok := n.Value(NicheGrowth); ok {
		s.Growth = GrowthModerate
		if g > fastGrowth {
			s.Growth = GrowthFast
		} else if g < 0 {
			s.Growth = GrowthFalling
		}
		sum += weightGrowth * math.Max(0, math.Min(1, (g-growthFloor)/growthSpan))
		weight += weightGrowth
	} else {
		s.Missing = append(s.Missing, NicheMissingGrowth)
	}
	if p, ok := n.perSeller(); ok && maxPer > 0 {
		s.HasPerSeller, s.PerSeller = true, p
		sum += weightPerSeller * p / maxPer
		weight += weightPerSeller
	} else {
		s.Missing = append(s.Missing, NicheMissingPerSeller)
	}
	if t, ok := n.Value(NicheTop10); ok {
		s.HasTop10, s.Crowded = true, t > crowdedTop10
		sum += weightTop10 * (1 - t)
		weight += weightTop10
	} else {
		s.Missing = append(s.Missing, NicheMissingTop10)
	}
	if peak, ok := n.PeakMonth(); ok {
		s.HasPeak, s.Peak = true, peak
		ahead := (int(peak) - int(now.Month()) + 12) % 12
		s.InSeason = ahead <= seasonAhead
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, ahead, 0)
		end := start.AddDate(0, 1, 0) // конец последнего дня месяца
		s.WeeksFrom = max(0, int(start.Sub(now).Hours()/24/7))
		s.WeeksTo = max(0, int(end.Sub(now).Hours()/24/7))
		part := offSeason
		if s.InSeason {
			part = 1
		}
		sum += weightSeason * part
		weight += weightSeason
	} else {
		s.Missing = append(s.Missing, NicheMissingMonths)
	}
	// Без месяцев оценка остается: веса остальных частей делятся заново.
	s.Ok = s.Growth != "" && s.HasPerSeller && s.HasTop10
	if s.Ok {
		s.Score = int(math.Round(100 * sum / weight))
	}
	return s
}
