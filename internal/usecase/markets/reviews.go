package markets

import (
	"errors"
	"time"
)

var ErrModelReviews = errors.New("stub")

const (
	MaxReviewsToModel = 150
	MaxReviewRunes    = 600
)

type Review struct {
	Day    time.Time
	Rating int
	Text   string
}

type ReviewSummary struct {
	Total, WithText int
	From, To        time.Time
	Ratings         [6]int
	Avg             float64
}

type ReviewPoint struct {
	Text  string
	Count int
}

type ReviewFindings struct{ Praise, Complaints []ReviewPoint }

func SummarizeReviews([]Review) ReviewSummary { return ReviewSummary{} }

func ReviewsPrompt([]Review) (string, int) { return "", 0 }

func ParseReviewFindings(string, int) (ReviewFindings, error) { return ReviewFindings{}, nil }
