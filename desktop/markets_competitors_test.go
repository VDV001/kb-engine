package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// «Конкуренты» (#613): топ продавцов ниши за дни до вчера и что изменилось к
// такому же периоду перед ними; год назад не нужен — нужно «что сейчас».
func TestCompetitorsView(t *testing.T) {
	s, _, _ := newServerSettings(t)
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/user/report_api_limit":
			_, _ = w.Write([]byte(`{"available":10,"use":0}`))
		case "/api/oz/get/category/sellers":
			asked = append(asked, q.Get("path")+" "+q.Get("d1")+"…"+q.Get("d2"))
			if q.Get("d1") == "2026-10-15" {
				_, _ = w.Write([]byte(`[{"supplier_id":1,"name":"Продавец А","items":120,"sales":1500,"revenue":1500000,"avg_price":1000},{"supplier_id":2,"name":"Новичок","items":10,"sales":100,"revenue":50000,"avg_price":500}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"supplier_id":1,"name":"Продавец А","items":100,"sales":1000,"revenue":1000000,"avg_price":1000}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.mpstatsURL = srv.URL
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	v, err := s.competitors(context.Background(), srv.Client(), " Одежда / Пижамы ", 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, " | ") != "Одежда/Пижамы 2026-10-15…2026-11-13 | Одежда/Пижамы 2026-09-15…2026-10-14" && strings.Join(asked, " | ") != "Одежда/Пижамы 2026-09-15…2026-10-14 | Одежда/Пижамы 2026-10-15…2026-11-13" {
		t.Errorf("asked %v: 30 days to yesterday and the 30 before", asked)
	}
	if v.Period != "15.10.2026 – 13.11.2026" || v.Prev != "15.09.2026 – 14.10.2026" || len(v.Rows) != 2 {
		t.Fatalf("view %+v", v)
	}
	if a := v.Rows[0]; a.Name != "Продавец А" || a.SalesChange != 0.5 || a.ItemsAdded != 20 || a.New {
		t.Errorf("А %+v", a)
	}
	if n := v.Rows[1]; !n.New {
		t.Errorf("Новичок %+v: new in the niche", n)
	}
	if _, err := s.competitors(context.Background(), srv.Client(), " ", 30, now); err == nil {
		t.Error("no niche read")
	}
}
