package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// «Фото» (#608): свои фото и видео — из кабинета, лидеры ниши и их главные
// фото — из MPStats; советы — правилом.
func TestPhotosView(t *testing.T) {
	s, _, _ := newServerSettings(t)
	mp := titleMCP(t, func(name string, a map[string]any) (string, bool) {
		switch {
		case name == "ozon_category" && a["report"] == "products":
			return `{"data":[{"id":2,"name":"Лидер","revenue":5000,"rating":4.8,"comments":300,"picscount":9,"hasvideo":1},` +
				`{"id":3,"name":"Второй","revenue":4000,"rating":4.7,"comments":50,"picscount":7,"hasvideo":1},` +
				`{"id":4,"name":"Мусор","revenue":9000,"rating":0,"comments":0,"picscount":1,"hasvideo":0}]}`, false
		case name == "ozon_sku" && a["report"] == "full":
			return fmt.Sprintf(`{"niche":{"name":"Одежда/Футболки женские"},"seller":{"name":"PRIMER"},"photo":{"count":5,"list":[{"m":"https://ir.ozone.ru/%v.jpg"}]}}`, a["sku"]), false
		case name == "ozon_sku":
			return `[]`, false
		case a["report"] == "brands":
			return `{"data":[]}`, false
		}
		return titleNicheJSON, false
	})
	s.mpstatsURL, s.mcpURL = mp.URL, mp.URL+"/mcp"
	if _, err := s.addMPStats(context.Background(), checkEndpoints{MPStats: mp.URL, HTTP: mp.Client()}, "MPStats", "m-1", false); err != nil {
		t.Fatal(err)
	}
	oz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":[{"name":"Футболка женская","primary_image":"https://ir.ozone.ru/p.jpg","images":["https://ir.ozone.ru/a.jpg"],"attributes":[]}]}`))
	}))
	t.Cleanup(oz.Close)
	row, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.photosCheck(context.Background(), mp.Client(), oz.URL, row.ID, "1234567", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Own) != 2 || v.OwnVideo || len(v.Leaders) != 2 || v.Leaders[0].Photo != "https://ir.ozone.ru/2.jpg" || v.Median != 8 {
		t.Fatalf("view %+v", v)
	}
	kinds := map[string]bool{}
	for _, a := range v.Advice {
		kinds[a.Kind] = true
	}
	if !kinds["photos"] || !kinds["video"] || !kinds["rich"] {
		t.Errorf("advice %+v", v.Advice)
	}
}
