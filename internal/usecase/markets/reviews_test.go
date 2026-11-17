package markets_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func day(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }

var someReviews = []markets.Review{
	{Day: day("2026-05-02"), Rating: 5, Text: "Отличная ткань, не садится"},
	{Day: day("2026-09-10"), Rating: 2, Text: "Маломерит на размер"},
	{Day: day("2026-03-01"), Rating: 4},
	{Day: day("2026-10-01"), Rating: 1, Text: "  "},
	{Day: day("2026-07-15"), Rating: 5, Text: "Ткань плотная"},
}

func TestSummarizeReviews(t *testing.T) {
	s := markets.SummarizeReviews(someReviews)
	if s.Total != 5 || s.WithText != 3 || !s.From.Equal(day("2026-03-01")) || !s.To.Equal(day("2026-10-01")) {
		t.Fatalf("%+v", s)
	}
	if s.Ratings != [6]int{0, 1, 1, 0, 1, 2} || s.Avg != 3.4 {
		t.Errorf("ratings %v avg %v", s.Ratings, s.Avg)
	}
	if z := markets.SummarizeReviews(nil); z.Total != 0 || z.Avg != 0 || !z.From.IsZero() {
		t.Errorf("empty %+v", z)
	}
}

func TestReviewsPrompt(t *testing.T) {
	p, n := markets.ReviewsPrompt(someReviews)
	if n != 3 {
		t.Fatalf("sent %d reviews, want only the 3 with text", n)
	}
	// Новые первыми, номером — по нему модель ссылается на отзывы.
	if !strings.Contains(p, "1. [2 из 5, 10.09.2026] Маломерит на размер") || !strings.Contains(p, "3. [5 из 5, 02.05.2026]") {
		t.Errorf("prompt:\n%s", p)
	}
	if strings.Contains(p, "  \n") || !strings.Contains(p, "не выдумывай") {
		t.Errorf("prompt:\n%s", p)
	}
	many := make([]markets.Review, markets.MaxReviewsToModel+20)
	for i := range many {
		many[i] = markets.Review{Day: day("2026-01-01").AddDate(0, 0, i), Rating: 3, Text: strings.Repeat("я", 2000)}
	}
	p, n = markets.ReviewsPrompt(many)
	if n != markets.MaxReviewsToModel || strings.Contains(p, strings.Repeat("я", markets.MaxReviewRunes+1)) {
		t.Errorf("sent %d, clipped %v", n, !strings.Contains(p, strings.Repeat("я", markets.MaxReviewRunes+1)))
	}
}

// Число подтверждений считает окно по номерам, а не берет у модели.
func TestParseReviewFindings(t *testing.T) {
	text := "Вот:\n```json\n" + `{"praise":[{"point":"ткань","reviews":[3,1,3,99,0]},{"point":"пустой","reviews":[]},{"point":"цвет","reviews":[1,2,3]}],` +
		`"complaints":[{"point":"маломерит","reviews":[2],"count":40}]}` + "\n```"
	f, err := markets.ParseReviewFindings(text, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Praise) != 2 || f.Praise[0].Text != "цвет" || f.Praise[0].Count != 3 || f.Praise[1].Text != "ткань" || f.Praise[1].Count != 2 {
		t.Errorf("praise %+v", f.Praise)
	}
	if len(f.Complaints) != 1 || f.Complaints[0].Count != 1 {
		t.Errorf("complaints %+v — count модели не берется", f.Complaints)
	}
	if _, err := markets.ParseReviewFindings("не знаю", 3); !errors.Is(err, markets.ErrModelReviews) {
		t.Errorf("not json: %v", err)
	}
	if f, err := markets.ParseReviewFindings(`{"praise":[],"complaints":[]}`, 3); err != nil || f.Praise == nil || f.Complaints == nil {
		t.Errorf("empty: %+v %v", f, err)
	}
}
