package markets

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrModelReviews is returned when the model's answer is not findings.
var ErrModelReviews = errors.New("модель не дала выводов из отзывов")

const (
	// MaxReviewsToModel — сколько последних отзывов с текстом уходит модели:
	// больше — длинный и дорогой вызов, а выводы держатся на свежих.
	MaxReviewsToModel = 150
	// MaxReviewRunes — длина одного отзыва в задании модели.
	MaxReviewRunes = 600
)

// Review — отзыв о товаре (#612): день, оценка 1-5, текст (бывает пустым).
type Review struct {
	Day    time.Time
	Rating int
	Text   string
}

// ReviewSummary — отзывы без модели: сколько, сколько с текстом, за какой
// период, оценки по звездам (Ratings[1..5]) и средняя.
type ReviewSummary struct {
	Total, WithText int
	From, To        time.Time
	Ratings         [6]int
	Avg             float64
}

// SummarizeReviews counts the reviews; средняя — до десятых.
func SummarizeReviews(rs []Review) ReviewSummary {
	var s ReviewSummary
	sum, rated := 0, 0
	for _, r := range rs {
		s.Total++
		if strings.TrimSpace(r.Text) != "" {
			s.WithText++
		}
		if s.From.IsZero() || r.Day.Before(s.From) {
			s.From = r.Day
		}
		if r.Day.After(s.To) {
			s.To = r.Day
		}
		if r.Rating >= 1 && r.Rating <= 5 {
			s.Ratings[r.Rating]++
			sum += r.Rating
			rated++
		}
	}
	if rated > 0 {
		s.Avg = math.Round(float64(sum)/float64(rated)*10) / 10
	}
	return s
}

// ReviewsSystem is the model's role for review findings.
const ReviewsSystem = "Ты аналитик отзывов покупателей на маркетплейсе. Опирайся только на тексты отзывов, не выдумывай. Отвечай только JSON без пояснений вокруг."

// ReviewsPrompt builds the task from the reviews with text, новые первыми,
// номером с 1 — по нему модель ссылается на отзывы; n — сколько ушло.
func ReviewsPrompt(rs []Review) (prompt string, n int) {
	var with []Review
	for _, r := range rs {
		if strings.TrimSpace(r.Text) != "" {
			with = append(with, r)
		}
	}
	slices.SortStableFunc(with, func(a, b Review) int { return b.Day.Compare(a.Day) })
	with = with[:min(len(with), MaxReviewsToModel)]
	var b strings.Builder
	b.WriteString("Ниже отзывы покупателей о товаре, у каждого номер, оценка и дата. Опирайся только на них, не выдумывай.\n")
	b.WriteString("Выдели, что покупатели хвалят и на что жалуются: короткие пункты своими словами, по одному свойству товара в пункте (ткань, размер, цвет, упаковка, доставка…).\n")
	b.WriteString("У каждого пункта перечисли номера отзывов, где это сказано. Пункт без отзыва не пиши.\n\n")
	for i, r := range with {
		text := strings.Join(strings.Fields(r.Text), " ")
		if rr := []rune(text); len(rr) > MaxReviewRunes {
			text = string(rr[:MaxReviewRunes]) + "…"
		}
		mark := "без оценки" // как в сводке: вне шкалы — не «0 из 5» (ревью #674)
		if r.Rating >= 1 && r.Rating <= 5 {
			mark = fmt.Sprintf("%d из 5", r.Rating)
		}
		fmt.Fprintf(&b, "%d. [%s, %s] %s\n", i+1, mark, r.Day.Format("02.01.2006"), text)
	}
	b.WriteString("\nОтвет — JSON: " + `{"praise":[{"point":"что хвалят","reviews":[номера]}],"complaints":[{"point":"на что жалуются","reviews":[номера]}]}`)
	return b.String(), len(with)
}

// ReviewPoint — вывод и сколько отзывов его подтверждают.
type ReviewPoint struct {
	Text  string
	Count int
}

// ReviewFindings — «хвалят» и «жалуются», частые первыми.
type ReviewFindings struct{ Praise, Complaints []ReviewPoint }

type reviewPointJSON struct {
	Point   string `json:"point"`
	Reviews []any  `json:"reviews"` // числа или строки с числом: модель пишет по-разному
}

// ParseReviewFindings reads the model's answer for n numbered reviews. Число
// подтверждений — разные номера из 1..n, которые назвала модель; ее собственное
// «count» не читается: проверить его нечем, а номера — можно.
func ParseReviewFindings(text string, n int) (ReviewFindings, error) {
	// Первый объект с praise или complaints, с какой бы «{» он ни начинался:
	// модель пишет и пояснения со скобками вокруг (ревью #674).
	for i := strings.Index(text, "{"); i >= 0; {
		var raw struct {
			Praise     []reviewPointJSON `json:"praise"`
			Complaints []reviewPointJSON `json:"complaints"`
		}
		if json.NewDecoder(strings.NewReader(text[i:])).Decode(&raw) == nil && (raw.Praise != nil || raw.Complaints != nil) {
			return ReviewFindings{Praise: reviewPoints(raw.Praise, n), Complaints: reviewPoints(raw.Complaints, n)}, nil
		}
		next := strings.Index(text[i+1:], "{")
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return ReviewFindings{}, fmt.Errorf("%w: в ответе нет JSON с praise и complaints", ErrModelReviews)
}

// reviewPoints keeps the points backed by at least one real review number.
func reviewPoints(ps []reviewPointJSON, n int) []ReviewPoint {
	out := []ReviewPoint{} // не nil: в JSON — [], а не null
	for _, p := range ps {
		seen := map[int]bool{}
		for _, v := range p.Reviews {
			if i := reviewNumber(v); i >= 1 && i <= n {
				seen[i] = true
			}
		}
		if t := strings.TrimSpace(p.Point); t != "" && len(seen) > 0 {
			out = append(out, ReviewPoint{Text: t, Count: len(seen)})
		}
	}
	slices.SortStableFunc(out, func(a, b ReviewPoint) int { return cmp.Compare(b.Count, a.Count) })
	return out
}

// reviewNumber reads a review number given as 3 or "3"; иное — 0.
func reviewNumber(v any) int {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) {
			return int(x)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return i
		}
	}
	return 0
}
