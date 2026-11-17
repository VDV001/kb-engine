package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func TestReviewsView(t *testing.T) {
	var calls atomic.Int32
	s, _, _ := newServerSettings(t)
	srv := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		calls.Add(1)
		switch {
		case name == "ozon_sku" && a["report"] == "comments" && a["sku"] == float64(1234567):
			return `[{"date":"2026-09-10","valuation":2,"likes":0,"text":"Маломерит"},{"date":"2026-03-01","valuation":5,"likes":0,"text":""},` +
				`{"date":"2026-07-15","valuation":5,"likes":1,"text":"Ткань плотная"}]`, false
		case name == "ozon_sku" && a["report"] == "comments":
			return "", false // отзывов нет
		}
		return "wrong call", true
	})
	s.mpstatsURL, s.mcpURL = srv.URL, srv.URL+"/mcp"
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, err := s.reviewsCheck(ctx, srv.Client(), "1234567", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Total != 3 || v.WithText != 2 || v.From != "2026-03-01" || v.To != "2026-09-10" || v.Calls != 1 || len(v.Ratings) != 5 || v.Ratings[4] != 2 || v.Ratings[1] != 1 {
		t.Fatalf("%+v", v)
	}
	before := calls.Load()
	if v, err := s.reviewsCheck(ctx, srv.Client(), "1234567", false, time.Now()); err != nil || v.Calls != 0 || calls.Load() != before {
		t.Errorf("second read: calls %d, mcp %d, %v — want memory", v.Calls, calls.Load()-before, err)
	}
	if _, err := s.reviewsCheck(ctx, srv.Client(), "1234567", true, time.Now()); err != nil || calls.Load() == before {
		t.Errorf("fresh read did not ask MPStats: %v", err)
	}
	if e, err := s.reviewsCheck(ctx, srv.Client(), "7654321", false, time.Now()); err != nil || e.Total != 0 || len(e.Ratings) != 5 {
		t.Errorf("no reviews: %+v %v", e, err)
	}
	if _, err := s.reviewsCheck(ctx, srv.Client(), "x", false, time.Now()); err == nil || !strings.Contains(err.Error(), "SKU") {
		t.Errorf("bad sku: %v", err)
	}
}

func TestModelReviews(t *testing.T) {
	cd, m := localChat(t)
	rs := []markets.Review{
		{Day: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), Rating: 2, Text: "Маломерит"},
		{Day: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Rating: 5, Text: "Ткань плотная"},
	}
	if _, err := cd.modelReviews(rs, false); err == nil || m.n != 0 {
		t.Fatalf("без согласия: %v, вызовов %d", err, m.n)
	}
	if _, err := cd.modelReviews([]markets.Review{{Rating: 5}}, true); err == nil || m.n != 0 {
		t.Fatalf("без текстов: %v, вызовов %d — модели нечего читать", err, m.n)
	}
	m.reply = `{"praise":[{"point":"плотная ткань","reviews":[2,2]}],"complaints":[{"point":"маломерит","reviews":[1],"count":50}]}`
	f, err := cd.modelReviews(rs, true)
	if err != nil || m.n != 1 || f.Used != 2 || f.Model == "" {
		t.Fatalf("%+v, %v, вызовов %d", f, err, m.n)
	}
	if len(f.Praise) != 1 || f.Praise[0].Count != 1 || len(f.Complaints) != 1 || f.Complaints[0].Count != 1 {
		t.Errorf("findings %+v", f)
	}
	var prompt strings.Builder
	for _, msg := range m.asked[0] {
		prompt.WriteString(msg.Content)
	}
	if !strings.Contains(prompt.String(), "Ткань плотная") {
		t.Errorf("prompt:\n%s", prompt.String())
	}
}

// Тексты отзывов уходят модели только при «Чату: разрешить» у подключения
// MPStats — как у чата (решение владельца 17.11).
func TestReviewFindingsNeedShare(t *testing.T) {
	cd, m := localChat(t)
	m.reply = `{"praise":[{"point":"ткань","reviews":[1]}],"complaints":[]}`
	for _, share := range []bool{false, true} {
		s, _, _ := newServerSettings(t)
		srv := titleMCP(t, func(name string, a map[string]any) (string, bool) {
			return `[{"date":"2026-07-15","valuation":5,"likes":0,"text":"Ткань плотная"}]`, false
		})
		s.mpstatsURL, s.mcpURL = srv.URL, srv.URL+"/mcp"
		if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", share); err != nil {
			t.Fatal(err)
		}
		before := m.n
		f, err := s.reviewFindings(context.Background(), srv.Client(), cd, "1234567", true, time.Now())
		if !share && (!errors.Is(err, errReviewsNotShared) || m.n != before) {
			t.Errorf("no share: %v, model calls %d", err, m.n-before)
		}
		if share && (err != nil || len(f.Praise) != 1) {
			t.Errorf("share: %+v %v", f, err)
		}
	}
}
