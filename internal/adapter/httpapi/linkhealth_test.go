package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Экран не может показать то, чего ему не отдали. Скан пишет результат в
// каталог с 01.08, usecase умеет его свести — остаётся довезти до страницы,
// иначе повторится история миграции разборов: работа сделана, а до границы
// не доехала, и для дашборда её не существует.
func TestServer_linkHealth(t *testing.T) {
	rec := get(t, newTestServer(), "/api/link-health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Alive       int `json:"alive"`
		Moved       int `json:"moved"`
		Gone        int `json:"gone"`
		Undecidable int `json:"undecidable"`
		Unchecked   int `json:"unchecked"`
		WithURL     int `json:"with_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if body.WithURL == 0 {
		t.Fatalf("знаменатель нулевой, сводка не доехала: %s", rec.Body.String())
	}
	if sum := body.Alive + body.Moved + body.Gone + body.Undecidable + body.Unchecked; sum != body.WithURL {
		t.Errorf("сумма состояний %d не сходится со знаменателем %d: %s",
			sum, body.WithURL, rec.Body.String())
	}
}
