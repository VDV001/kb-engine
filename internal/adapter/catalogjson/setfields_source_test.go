package catalogjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// Источник записи — такое же поле каталога, как автор или заметка, и правится
// оно так же. Повод конкретный: семь записей от 04.08.2026 пришли из утреннего
// дайджеста, а несут source "bot-inbox", потому что проставлялось оно руками.
// Пока движок не умеет его писать, единственный способ починить — лезть в JSON
// мимо движка, а это ровно то, из-за чего в книге финансов однажды появилась
// строка без id.
func TestSetFields_WritesSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	const before = `{"entries":[
{"id":1439,"title":"Первая","source":"bot-inbox"},
{"id":1440,"title":"Вторая","source":"bot-inbox"},
{"id":9999,"title":"Чужая","source":"batch"}
]}`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := catalogjson.SetFields(path, []int{1439, 1440}, catalogjson.Changes{Source: "digest"})
	if err != nil {
		t.Fatalf("SetFields: %v", err)
	}
	if n != 2 {
		t.Fatalf("изменено записей = %d, ожидалось 2", n)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Разбираем JSON, а не сравниваем строки: движок пишет форматированный файл,
	// и ассерт по подстроке `"source":"digest"` падал на пробеле после двоеточия —
	// то есть проверял бы форматирование вместо поведения.
	var out struct {
		Entries []struct {
			ID     int    `json:"id"`
			Source string `json:"source"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("результат не разбирается как JSON: %v", err)
	}
	bySource := map[int]string{}
	for _, e := range out.Entries {
		bySource[e.ID] = e.Source
	}
	for _, id := range []int{1439, 1440} {
		if bySource[id] != "digest" {
			t.Errorf("у записи %d источник %q, ожидался digest", id, bySource[id])
		}
	}
	// Соседняя запись не тронута: правка адресная, а не по файлу.
	if bySource[9999] != "batch" {
		t.Errorf("правка задела запись, которую не просили: источник %q", bySource[9999])
	}
}
