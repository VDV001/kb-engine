package catalogjson_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// Чтение каталога стоит на пути каждого запроса витрины: движок читает файл
// заново, чтобы правка каталога была видна без перезапуска. Цена этого решения
// до сих пор не измерялась.
//
// ⚠️ Каталог выдуман и однороден — настоящий несёт записи разной длины и
// заполненности, поэтому число здесь про разбор, а не про живую базу.
func writeCatalog(b *testing.B, n int) string {
	b.Helper()
	entries := make([]map[string]any, 0, n)
	for i := range n {
		entries = append(entries, map[string]any{
			"id":          i + 1,
			"kind":        "article",
			"title":       fmt.Sprintf("Запись %d", i+1),
			"description": "Разбор темы: устройство, замеры, где ломается",
			"category":    "articles",
			"lifecycle":   "active",
			"status":      "read",
			"tags":        []string{"go", "тестирование", "профилирование"},
			"url":         fmt.Sprintf("https://example.invalid/%d", i+1),
		})
	}
	raw, err := json.Marshal(map[string]any{
		"meta":    map[string]any{"categories": map[string]any{"articles": "Статьи"}},
		"entries": entries,
	})
	if err != nil {
		b.Fatal(err)
	}
	p := filepath.Join(b.TempDir(), "catalog.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		b.Fatal(err)
	}
	return p
}

func BenchmarkLoad(b *testing.B) {
	for _, n := range []int{100, 1500} {
		p := writeCatalog(b, n)
		b.Run(fmt.Sprintf("записей_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := catalogjson.Load(p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
