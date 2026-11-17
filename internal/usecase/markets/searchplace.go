package markets

import "time"

type SearchPlace struct {
	Query      string
	Count, Avg int
	Last       int
	LastDay    time.Time
	Seen, Days int
}

func SearchPlaces([]OwnQuery, time.Time, int) []SearchPlace { return nil }

type SearchStats struct{ Words, WordsBefore, Avg, AvgBefore int }
