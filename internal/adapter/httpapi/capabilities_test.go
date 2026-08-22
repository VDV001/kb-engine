package httpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

// Статусная таблица возможностей (идея из T3MP3ST): честное деление на
// stable / experimental / roadmap. Источник ОДИН — Go-срез в движке; и вкладка
// About, и README читают его, иначе таблица разойдётся молча («число живёт
// дважды»).
func TestCapabilities_endpointServesTypedTable(t *testing.T) {
	rec := get(t, newTestServer(), "/api/capabilities")
	if rec.Code != 200 {
		t.Fatalf("код %d, ждали 200", rec.Code)
	}
	var caps []httpapi.Capability
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil {
		t.Fatalf("ответ не разобрать: %v", err)
	}
	if len(caps) == 0 {
		t.Fatal("пустая таблица возможностей")
	}
	// Каждая строка несёт один из трёх статусов — иначе «честное деление»
	// превращается в список без деления.
	allowed := map[string]bool{"stable": true, "experimental": true, "roadmap": true}
	for _, c := range caps {
		if c.Name == "" {
			t.Fatalf("возможность без имени: %+v", c)
		}
		if !allowed[c.Status] {
			t.Fatalf("возможность %q имеет статус %q вне {stable, experimental, roadmap}", c.Name, c.Status)
		}
	}
}

// Отрицательный контроль на инвариант источника: в наборе есть хотя бы по
// одному стабильному и одному не-стабильному — иначе таблица, где всё «stable»,
// не отличалась бы от отсутствия деления вовсе.
func TestCapabilities_notAllOneStatus(t *testing.T) {
	caps := httpapi.Capabilities()
	var stable, other int
	for _, c := range caps {
		if c.Status == "stable" {
			stable++
		} else {
			other++
		}
	}
	if stable == 0 || other == 0 {
		t.Fatalf("деление вырождено: stable=%d, прочих=%d", stable, other)
	}
}
