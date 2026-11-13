package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/mpstats"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// Инструменты MPStats в чате окна (отзыв 14.11): ниши, продажи категории по
// неделям и поиск пути категории — только у подключения, которому человек
// разрешил давать данные модели, как у кабинета Ozon.

const mpstatsToolsPrompt = `
У человека подключен MPStats — аналитика рынка Ozon по категориям. О рынке и нишах (сколько продает категория, рост к году, конкуренция, падает ли весь рынок или только товар человека) спрашивай инструменты mpstats_*. Путь категории как в MPStats («Одежда/Мужская одежда/Брюки»); не знаешь путь — найди его mpstats_categories. Цифры MPStats — оценка рынка по открытым данным, не кабинет человека: так и говори.`

// mpstatsChatDays — период ниш в чате: 30 дней до вчера, как «Ниша» по умолчанию.
const mpstatsChatDays = 30

// mpstatsMaxWeeks — сколько недель отдает ряд категории.
const mpstatsMaxWeeks = 26

// mpstatsMaxPaths — сколько путей отдает поиск категории.
const mpstatsMaxPaths = 30

var mpstatsToolSpecs = []chat.Tool{
	{Name: "mpstats_categories", Description: "Найти путь категории Ozon в MPStats по словам: «брюки мужские» → «Одежда/Мужская одежда/Брюки».",
		Parameters: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"слова из названия категории"}},"required":["q"]}`)},
	{Name: "mpstats_niche", Description: "Ниши внутри категории за 30 дней до вчера: продажи, выручка, продавцы, рост к прошлому году, доля топ-10 продавцов и оценка ниши 0-100.",
		Parameters: []byte(`{"type":"object","properties":{"path":{"type":"string","description":"путь категории, как в MPStats"}},"required":["path"]}`)},
	{Name: "mpstats_trend", Description: "Продажи всей категории по неделям (с понедельника) — понять, падает ли рынок или только товар человека.",
		Parameters: []byte(`{"type":"object","properties":{"path":{"type":"string","description":"путь категории, как в MPStats"},"weeks":{"type":"integer","minimum":1,"maximum":26}},"required":["path"]}`)},
}

// mpstatsExtra adapts MPStats to the chat.
type mpstatsExtra struct {
	conn desktopconfig.Connection
	cl   mpstats.Client
	now  func() time.Time
}

// mpstatsExtras gives the chat MPStats of the first shared connection, if the
// chat's sources (only, пусто — все) keep it; nil — нет.
func mpstatsExtras(s *folderSettings, hc *http.Client, now func() time.Time, only []string) []chat.Extra {
	for _, c := range s.snapshot().cfg.Connections {
		if c.Kind != desktopconfig.ConnMPStats || !c.Enabled || !c.Share || (len(only) > 0 && !slices.Contains(only, c.ID)) {
			continue
		}
		cl, err := s.mpstatsByID(c.ID, hc)
		if err != nil {
			continue // ключа нет — подключение покажет ошибку в «Подключениях»
		}
		return []chat.Extra{mpstatsExtra{conn: c, cl: cl, now: now}}
	}
	return nil
}

func (mpstatsExtra) Prompt() string     { return mpstatsToolsPrompt }
func (mpstatsExtra) Tools() []chat.Tool { return mpstatsToolSpecs }

type mpstatsArgs struct {
	Q, Path string
	Weeks   int
}

// Describe names the connection and the question.
func (x mpstatsExtra) Describe(name string, args []byte) (chat.Use, bool) {
	var a mpstatsArgs
	_ = json.Unmarshal(args, &a) // испорченные аргументы — шаг без подробностей, отказ скажет Exec
	what := map[string]string{"mpstats_categories": "ищет категорию «" + a.Q + "»", "mpstats_niche": "ниши «" + a.Path + "»", "mpstats_trend": "продажи по неделям «" + a.Path + "»"}[name]
	if what == "" {
		return chat.Use{}, false
	}
	return chat.Use{Kind: chat.UseConnection, ID: x.conn.ID, Name: "MPStats «" + x.conn.Name + "»", What: what}, true
}

// Exec runs an MPStats tool; отказ MPStats уходит модели словами.
func (x mpstatsExtra) Exec(ctx context.Context, name string, args []byte) (string, bool) {
	if !slices.ContainsFunc(mpstatsToolSpecs, func(t chat.Tool) bool { return t.Name == name }) {
		return "", false
	}
	var a mpstatsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "аргументы не разобраны: " + err.Error(), true
	}
	out, err := x.run(ctx, name, a)
	if err != nil {
		return "ошибка MPStats: " + err.Error(), true
	}
	return out, true
}

func (x mpstatsExtra) run(ctx context.Context, name string, a mpstatsArgs) (string, error) {
	if name == "mpstats_categories" {
		return x.categories(ctx, a.Q)
	}
	path := mpstatsPath(a.Path)
	if path == "" {
		return "", errors.New("нет path — путь категории, как в MPStats; найди его mpstats_categories")
	}
	to := dayOf(x.now()).AddDate(0, 0, -1)
	if name == "mpstats_niche" {
		tb, err := nichesWith(ctx, x.cl, path, to.AddDate(0, 0, 1-mpstatsChatDays), to, x.now())
		if err != nil {
			return "", err
		}
		return nicheText(tb, to.AddDate(0, 0, 1-mpstatsChatDays), to), nil
	}
	return x.trend(ctx, path, a.Weeks, to)
}

// categories finds the paths holding every word of q.
func (x mpstatsExtra) categories(ctx context.Context, q string) (string, error) {
	var words []string // FoldName вырезает пробелы: слова — до свертки
	for w := range strings.FieldsSeq(q) {
		words = append(words, domain.FoldName(w))
	}
	if len(words) == 0 {
		return "", errors.New("нет q — слов из названия категории")
	}
	paths, err := x.cl.Categories(ctx)
	if err != nil {
		return "", errors.New(mpstatsMessage(err))
	}
	var hit []string
	for _, p := range paths {
		f := domain.FoldName(p)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(f, w) }) {
			hit = append(hit, p)
		}
	}
	if len(hit) == 0 {
		return "категорий с этими словами нет — спроси человека, как категория называется в Ozon", nil
	}
	more := ""
	if len(hit) > mpstatsMaxPaths {
		more = fmt.Sprintf("\n…и еще %d — уточни слова", len(hit)-mpstatsMaxPaths)
		hit = hit[:mpstatsMaxPaths]
	}
	return strings.Join(hit, "\n") + more, nil
}

// trend sums the category's days by weeks from Monday, последние weeks недель до to.
func (x mpstatsExtra) trend(ctx context.Context, path string, weeks int, to time.Time) (string, error) {
	if weeks <= 0 {
		weeks = 8
	}
	weeks = min(weeks, mpstatsMaxWeeks)
	from := monday(to).AddDate(0, 0, -7*(weeks-1))
	days, err := x.cl.CategoryByDay(ctx, path, from, to)
	if err != nil {
		return "", errors.New(mpstatsMessage(err))
	}
	sums := map[time.Time]float64{}
	var order []time.Time
	for _, d := range days {
		w := monday(d.Day)
		if _, ok := sums[w]; !ok {
			order = append(order, w)
		}
		sums[w] += d.Sales
	}
	slices.SortFunc(order, func(a, b time.Time) int { return a.Compare(b) })
	var b strings.Builder
	fmt.Fprintf(&b, "MPStats · %s, продажи категории по неделям (с понедельника, шт), %s – %s:\n", path, from.Format(time.DateOnly), to.Format(time.DateOnly))
	for _, w := range order {
		fmt.Fprintf(&b, "%s: %g\n", w.Format(time.DateOnly), sums[w])
	}
	if len(order) == 0 {
		b.WriteString("продаж за эти дни MPStats не отдал\n")
	}
	if !monday(to).AddDate(0, 0, 6).Equal(to) {
		b.WriteString("последняя неделя неполная\n")
	}
	return b.String(), nil
}

// dayOf is midnight UTC of t's day: дни MPStats — в UTC.
func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// monday is the Monday of d's week.
func monday(d time.Time) time.Time {
	return d.AddDate(0, 0, -(int(d.Weekday())+6)%7)
}

// nicheText is the niche table in words for the model.
func nicheText(tb nicheTable, from, to time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, %s – %s:\n", tb.File, from.Format(time.DateOnly), to.Format(time.DateOnly))
	for _, n := range tb.Niches {
		var parts []string
		for _, f := range tb.Fields {
			v, ok := n.Values[f.ID]
			if !ok {
				continue
			}
			switch domain.NicheColumn(f.ID) {
			case domain.NicheGrowth:
				parts = append(parts, fmt.Sprintf("%s %+.0f%%", f.Label, v*100))
			case domain.NicheTop10:
				parts = append(parts, fmt.Sprintf("%s %.0f%%", f.Label, v*100))
			default:
				parts = append(parts, fmt.Sprintf("%s %.0f", f.Label, v))
			}
		}
		if n.Score.Ok {
			parts = append(parts, fmt.Sprintf("оценка %d/100", n.Score.Score))
		}
		fmt.Fprintf(&b, "- %s: %s\n", n.Name, strings.Join(parts, ", "))
	}
	if len(tb.Niches) == 0 {
		b.WriteString("ниш нет\n")
	}
	for _, p := range tb.Problems {
		b.WriteString("примечание: " + p.Reason + "\n")
	}
	return b.String()
}
